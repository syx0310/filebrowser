package indexing

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gtsteffaniak/filebrowser/backend/internal/utils"
	"github.com/gtsteffaniak/filebrowser/backend/pkg/settings"
)

func TestFixedScheduleIgnoresComplexityAndTimeGrid(t *testing.T) {
	for _, changed := range []bool{false, true} {
		idx := &Index{Source: settings.Source{Config: settings.SourceConfig{Indexing: settings.IndexingConfig{ScanInterval: "17m"}}}}
		last := time.Now().Truncate(time.Second).Add(-10*time.Second + 123*time.Millisecond)
		s := &Scanner{idx: idx, scanPath: "/", complexity: 10, currentSchedule: 9, lastScanned: last, filesChanged: changed}
		s.updateSchedule()
		want := last.Add(17 * time.Minute)
		if s.nextRun.Before(want) || s.nextRun.Sub(want) >= time.Second {
			t.Fatalf("fixed nextRun=%v, want %v rounded up to a second", s.nextRun, want)
		}
		if got := idx.takeDueScannerBatch(want.Add(-time.Second)); len(got) != 0 {
			t.Fatal("scanner was due before its fixed delay")
		}
		if got := idx.takeDueScannerBatch(s.nextRun); len(got) != 1 || got[0] != s {
			t.Fatal("fixed scanner was not enrolled in scheduler")
		}
	}
}

func TestFixedScheduleRestoresIntervalAndLongerCycle(t *testing.T) {
	persisted := persistedScanners("/", "/a/")
	for _, saved := range persisted {
		saved.LastScanned = time.Now().Add(-10 * time.Minute)
		saved.FullScanCounter = 7
	}
	idx := newRestoreTestIndex(t, "fixed_restore", []string{"a"}, persisted)
	idx.Config.Indexing = settings.IndexingConfig{ScanInterval: "90m", FullScanEvery: 8}
	before := idx.policyFingerprint()
	idx.Config.Indexing = settings.IndexingConfig{ScanInterval: "24h", FullScanEvery: 3}
	if idx.policyFingerprint() != before {
		t.Fatal("changing scheduling must not invalidate index contents")
	}
	if idx.restoreFullScanCounter(7) != 0 {
		t.Fatal("shortening the cycle must make an overdue full scan due")
	}
	idx.Config.Indexing = settings.IndexingConfig{ScanInterval: "90m", FullScanEvery: 8}
	idx.setupMultiScanner(false)
	idx.mu.RLock()
	for path, scanner := range idx.scanners {
		scanner.statsMu.RLock()
		want := persisted[path].LastScanned.Add(90 * time.Minute)
		if scanner.fullScanCounter != 7 || scanner.nextRun.Before(want) || scanner.nextRun.Sub(want) >= time.Second {
			t.Errorf("%s: restored counter=%d next=%v want=%v", path, scanner.fullScanCounter, scanner.nextRun, want)
		}
		scanner.statsMu.RUnlock()
	}
	idx.mu.RUnlock()
}

func TestFixedScheduleMissedWhileStoppedRunsPromptly(t *testing.T) {
	idx := &Index{Source: settings.Source{Config: settings.SourceConfig{Indexing: settings.IndexingConfig{ScanInterval: "90m"}}}}
	s := &Scanner{idx: idx, scanPath: "/", lastScanned: time.Now().Add(-2 * time.Hour)}
	idx.scanners = map[string]*Scanner{"/": s}
	idx.restoreScannerNextRuns()
	if s.nextRun.After(time.Now().Add(time.Second)) {
		t.Fatalf("overdue scanner delayed until %v", s.nextRun)
	}
	if got := idx.takeDueScannerBatch(time.Now().Add(time.Second)); len(got) != 1 {
		t.Fatal("overdue fixed scanner missing from calendar")
	}
}

func TestConfiguredFullScanCadence(t *testing.T) {
	for _, every := range []int{1, 3, 8} {
		t.Run(strconv.Itoa(every), func(t *testing.T) {
			idx, write, _ := quickFixture(t)
			idx.Config.Indexing = settings.IndexingConfig{ScanInterval: "2h", FullScanEvery: every}
			write("file.txt", "a")
			scanner := idx.scanners["/"]
			scanner.executeScan()
			rootInfo, err := os.Stat(idx.Path)
			if err != nil {
				t.Fatal(err)
			}
			write("file.txt", "abcdef")
			// An in-place write with unchanged directory metadata requires a full scan.
			if err := os.Chtimes(idx.Path, rootInfo.ModTime(), rootInfo.ModTime()); err != nil {
				t.Fatal(err)
			}
			for run := 1; run <= every; run++ {
				scanner.executeScan()
				items, err := idx.db.GetItemsByPaths(idx.Name, []string{"/file.txt"})
				if err != nil {
					t.Fatal(err)
				}
				want := int64(1)
				if run == every {
					want = 6
				}
				if items["/file.txt"] == nil || items["/file.txt"].Size != want {
					t.Fatalf("cycle=%d run=%d: file size did not follow full scan cadence", every, run+1)
				}
			}
		})
	}
}

func TestNewScannerDoesNotRestartExistingScannerEarly(t *testing.T) {
	idx, write, _ := quickFixture(t)
	idx.Config.Indexing = settings.IndexingConfig{ScanInterval: "6h", FullScanEvery: 5}
	write("file.txt", "a")
	root := idx.scanners["/"]
	root.executeScan()
	last, next := root.lastScanned, root.nextRun
	if err := os.Mkdir(filepath.Join(idx.Path, "new"), 0755); err != nil {
		t.Fatal(err)
	}
	root.checkForNewChildDirectories()
	child := idx.scanners["/new/"]
	if child == nil || idx.earliestSlotWake(time.Now()).After(time.Now().Add(time.Second)) {
		t.Fatal("new child must wake the scheduler without waiting for the root interval")
	}
	idx.schedulerAdaptiveTick()
	if !root.lastScanned.Equal(last) || !root.nextRun.Equal(next) {
		t.Fatal("new child bootstrap rescheduled the root before its fixed interval")
	}
	if child.lastScanned.IsZero() || child.nextRun.IsZero() {
		t.Fatal("new child was not scanned and scheduled")
	}
}

func TestIndexInfoReportsScheduleAndValidUnixSeconds(t *testing.T) {
	idx, write, _ := quickFixture(t)
	idx.Config.Indexing = settings.IndexingConfig{ScanInterval: "2h", FullScanEvery: 3}
	indexesMutex.Lock()
	previous := indexes[idx.Name]
	indexes[idx.Name] = idx
	indexesMutex.Unlock()
	t.Cleanup(func() {
		indexesMutex.Lock()
		if previous == nil {
			delete(indexes, idx.Name)
		} else {
			indexes[idx.Name] = previous
		}
		indexesMutex.Unlock()
		utils.DiskUsageCache.Delete("usageCache-" + idx.Name)
	})
	utils.DiskUsageCache.Set("usageCache-"+idx.Name, true)
	info, err := GetIndexInfo(idx.Name, false)
	if err != nil || info.LastIndexedUnix != 0 || info.Indexing != idx.Config.Indexing {
		t.Fatalf("unscanned source: %+v %v", info, err)
	}
	write("file.txt", "a")
	idx.scanners["/"].executeScan()
	info, err = GetIndexInfo(idx.Name, false)
	if err != nil || len(info.Scanners) != 1 {
		t.Fatalf("scanned source: %+v %v", info, err)
	}
	s := info.Scanners[0]
	if info.LastIndexedUnix != s.LastScanned.Unix() || s.LastIndexedUnix != s.LastScanned.Unix() || s.NextScanUnix <= s.LastIndexedUnix {
		t.Fatalf("inconsistent scan timestamps: %+v / %+v", info, s)
	}
	if info.UsedAsIndexed != 1 {
		t.Fatalf("indexed size remained stale after a completed scan: %d", info.UsedAsIndexed)
	}
	write("second.txt", "bc")
	idx.scanners["/"].executeScan()
	info, err = GetIndexInfo(idx.Name, false)
	if err != nil || info.UsedAsIndexed != 3 {
		t.Fatalf("quick scan did not update cached indexed usage: %+v %v", info, err)
	}
}
