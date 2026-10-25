package indexing

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	dbsql "github.com/gtsteffaniak/filebrowser/backend/internal/database/sql"
	"github.com/gtsteffaniak/filebrowser/backend/pkg/indexing/iteminfo"
	"github.com/gtsteffaniak/filebrowser/backend/pkg/settings"
)

func quickFixture(t *testing.T) (*Index, func(string, string), func(bool)) {
	t.Helper()
	previous := settings.Config.Server.IndexSqlConfig
	t.Cleanup(func() { settings.Config.Server.IndexSqlConfig = previous })
	settings.Config.Server.IndexSqlConfig.Mode = "size"
	settings.Config.Server.IndexSqlConfig.IncludeHidden = true
	root := t.TempDir()
	db, _, err := dbsql.NewIndexDB("quick", "OFF", 100, 32, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	idx := &Index{mock: true, Source: settings.Source{Name: "quick", Path: root, Config: settings.SourceConfig{UseLogicalSize: true}},
		db: db, folderSizes: map[string]uint64{}, folderCounts: map[string]uint64{}, folderSizesUnsynced: map[string]struct{}{},
		scanUpdatedPaths: map[string]bool{}, scanners: map[string]*Scanner{}}
	idx.scanners["/"] = idx.createScanner("/")
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	pass := func(full bool) {
		t.Helper()
		idx.activeScannerPath = "/"
		idx.scanners["/"].runRootScan(!full)
		var paths []string
		for path := range idx.scanners {
			if path != "/" {
				paths = append(paths, path)
			}
		}
		sort.Strings(paths)
		for _, path := range paths {
			s := idx.scanners[path]
			idx.activeScannerPath = path
			s.runChildScan(!full && !s.lastScanned.IsZero())
		}
		idx.activeScannerPath = ""
		if err := idx.PostScan(); err != nil {
			t.Fatal(err)
		}
	}
	return idx, write, pass
}

func assertQuickTotals(t *testing.T, idx *Index, files, size uint64) {
	t.Helper()
	if got := idx.folderCount("/"); got == nil || *got != files {
		t.Fatalf("root count=%v, want %d", got, files)
	}
	if got, _ := idx.GetFolderSize("/"); got != size {
		t.Fatalf("root size=%d, want %d", got, size)
	}
	_, actual, err := idx.db.GetRecursiveCount(idx.Name, "/")
	if err != nil || actual != files {
		t.Fatalf("SQL files=%d want=%d err=%v", actual, files, err)
	}
	for path, scanner := range idx.scanners {
		var dirs, count uint64
		if path == "/" {
			count, err = idx.db.GetDirectFileCount(idx.Name, path)
		} else {
			dirs, count, err = idx.db.GetRecursiveCount(idx.Name, path)
		}
		if err != nil || scanner.numFiles != count || scanner.numDirs != dirs {
			t.Fatalf("%s counters=(%d,%d) SQL=(%d,%d) err=%v", path, scanner.numDirs, scanner.numFiles, dirs, count, err)
		}
	}
}

func TestQuickScanDeltaAndRecovery(t *testing.T) {
	idx, write, pass := quickFixture(t)
	write("root.txt", "abc")
	write("a/one", "12345")
	write("a/nested/.hidden", "1234567")
	write("b/two", "123456789")
	pass(true)
	assertQuickTotals(t, idx, 4, 24)
	// With a completed baseline, checking unchanged directories cannot touch SQL.
	db := idx.db
	idx.db = nil
	for _, s := range idx.scanners {
		s.runQuickScan()
	}
	idx.db = db
	assertQuickTotals(t, idx, 4, 24)
	write("a/new/deep/item", "1234")
	write("a/extra", "12")
	if err := os.Remove(filepath.Join(idx.Path, "b/two")); err != nil {
		t.Fatal(err)
	}
	pass(false)
	assertQuickTotals(t, idx, 5, 21)
	// Repeat must not double-count any directory or file.
	pass(false)
	assertQuickTotals(t, idx, 5, 21)
	if err := os.Rename(filepath.Join(idx.Path, "a/new"), filepath.Join(idx.Path, "b/moved")); err != nil {
		t.Fatal(err)
	}
	pass(false)
	assertQuickTotals(t, idx, 5, 21)
	if err := os.RemoveAll(filepath.Join(idx.Path, "a/nested")); err != nil {
		t.Fatal(err)
	}
	pass(false)
	assertQuickTotals(t, idx, 4, 14)
	// Restore without trusting application-db scanner counters, as after a crash.
	if err := idx.restoreDirectoryStates(); err != nil {
		t.Fatal(err)
	}
	idx.restoreFolderSizes()
	for _, s := range idx.scanners {
		s.numFiles, s.numDirs = 9999, 9999
	}
	idx.restoreScannerCounters()
	pass(false)
	assertQuickTotals(t, idx, 4, 14)
	// A timestamp going backwards still requires reconciliation.
	write("a/backdated", "x")
	old := time.Unix(1600000000, 123456789)
	if err := os.Chtimes(filepath.Join(idx.Path, "a"), old, old); err != nil {
		t.Fatal(err)
	}
	pass(false)
	assertQuickTotals(t, idx, 5, 15)
}

func TestQuickScanFailedTransactionDoesNotAdvanceBaseline(t *testing.T) {
	idx, write, pass := quickFixture(t)
	write("a/file", "abc")
	pass(true)
	before, _ := idx.directoryState("/a/")
	write("a/new", "12345")
	if _, err := idx.db.Exec(`CREATE TRIGGER reject_quick BEFORE INSERT ON index_items WHEN NEW.name = 'new' BEGIN SELECT RAISE(ABORT, 'test write failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := idx.scanners["/a/"].checkFolderModtime("/a/")
	if err == nil {
		t.Fatal("expected write failure")
	}
	after, _ := idx.directoryState("/a/")
	if before != after {
		t.Fatal("failed transaction advanced checkpoint")
	}
	assertQuickTotals(t, idx, 1, 3)
	if _, err := idx.db.Exec(`DROP TRIGGER reject_quick`); err != nil {
		t.Fatal(err)
	}
	pass(false)
	assertQuickTotals(t, idx, 2, 8)
}

func TestQuickScanAPIInvalidationAndFullReconciliation(t *testing.T) {
	idx, write, pass := quickFixture(t)
	write("a/file", "abc")
	pass(true)
	write("a/new", "12345")
	if _, err := idx.GetFileInfo(FileInfoRequest{IndexPath: "/a/", Expand: true, ShowHidden: true}); err != nil {
		t.Fatal(err)
	}
	state, _ := idx.directoryState("/a/")
	if state.Valid {
		t.Fatal("API write-through must invalidate its old scan baseline")
	}
	pass(false)
	assertQuickTotals(t, idx, 2, 8)
	// In-place file changes do not have to change the directory mtime.
	write("a/file", "abcdefghij")
	pass(false)
	pass(true)
	assertQuickTotals(t, idx, 2, 15)
}

func TestQuickScanReadFailurePreservesIndex(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission test needs an unprivileged user")
	}
	idx, write, pass := quickFixture(t)
	write("a/file", "abc")
	pass(true)
	state, _ := idx.directoryState("/a/")
	state.Valid = false
	idx.directoryStates["/a/"] = state
	path := filepath.Join(idx.Path, "a")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0755) })
	_, err := idx.scanners["/a/"].checkFolderModtime("/a/")
	if err == nil {
		t.Fatal("expected read failure")
	}
	assertQuickTotals(t, idx, 1, 3)
}

func BenchmarkQuickScanUnchanged(b *testing.B) {
	root := b.TempDir()
	idx := &Index{Source: settings.Source{Name: "benchmark", Path: root}, folderSizes: map[string]uint64{}, directoryStates: map[string]iteminfo.DirectoryScanState{}, scanUpdatedPaths: map[string]bool{}}
	for i := 0; i < 1000; i++ {
		path := filepath.Join(root, fmt.Sprintf("d%04d", i))
		if err := os.Mkdir(path, 0755); err != nil {
			b.Fatal(err)
		}
		info, _ := os.Stat(path)
		key := "/" + filepath.Base(path) + "/"
		idx.folderSizes[key] = 0
		idx.directoryStates[key] = iteminfo.DirectoryScanState{ModTimeNS: info.ModTime().UnixNano(), Valid: true}
	}
	s := &Scanner{idx: idx, scanPath: "/"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for path := range idx.directoryStates {
			if _, err := s.checkFolderModtime(path); err != nil {
				b.Fatal(err)
			}
		}
	}
}
