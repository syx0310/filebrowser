package indexing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	dbsql "github.com/gtsteffaniak/filebrowser/backend/internal/database/sql"
	"github.com/gtsteffaniak/filebrowser/backend/pkg/settings"
)

func TestBackgroundStartupAllowsBrowsingBeforeIndexOpen(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	source := &settings.Source{Name: "pending-test", Path: root, Config: settings.SourceConfig{UseLogicalSize: true}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	done := startBackgroundInitialization(ctx, []*settings.Source{source}, nil, func() (*dbsql.IndexDB, bool, error) {
		<-release
		return nil, false, errors.New("test unavailable index")
	})
	t.Cleanup(func() { indexesMutex.Lock(); delete(indexes, source.Name); indexesMutex.Unlock() })
	idx := GetIndex(source.Name)
	if idx == nil {
		t.Fatal("source must be registered before opening index")
	}
	info, err := GetIndexInfo(source.Name, false)
	if err != nil || info.Initialization != "opening" || info.Status != INDEXING {
		t.Fatalf("pending status: %+v %v", info, err)
	}
	listing, err := idx.GetFileInfo(FileInfoRequest{IndexPath: "/", Expand: true})
	if err != nil || len(listing.Files) != 1 || listing.Files[0].Name != "hello.txt" {
		t.Fatalf("pending browsing: %+v %v", listing, err)
	}
	cancel()
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("startup ignored cancellation")
	}
}

func TestDirectoryRollupsRecoverAfterInterruptedParentSync(t *testing.T) {
	idx, write, pass := quickFixture(t)
	write("a/b/file", "abc")
	pass(true)
	write("a/b/new", "12345")
	if _, err := idx.scanners["/a/"].checkFolderModtime("/a/b/"); err != nil {
		t.Fatal(err)
	}
	// Child transaction has committed; parent size/count rows are still old.
	idx.restoreFolderSizes()
	if err := idx.restoreDirectoryStates(); err != nil {
		t.Fatal(err)
	}
	idx.restoreDirectoryAggregates()
	idx.restoreScannerCounters()
	assertQuickTotals(t, idx, 2, 8)
	pass(false)
	assertQuickTotals(t, idx, 2, 8)
}

func TestFullScanSameSecondDeletion(t *testing.T) {
	idx, write, pass := quickFixture(t)
	write("a/file", "abc")
	pass(true)
	if err := os.Remove(filepath.Join(idx.Path, "a/file")); err != nil {
		t.Fatal(err)
	}
	pass(true)
	assertQuickTotals(t, idx, 0, 0)
}

func TestLegacyQuickScanBuildsBaselineWithoutDoubleCounting(t *testing.T) {
	idx, write, pass := quickFixture(t)
	write("a/file", "abc")
	write("a/b/another", "12")
	pass(true)
	if _, err := idx.db.Exec("DELETE FROM index_directory_state WHERE source = ?", idx.Name); err != nil {
		t.Fatal(err)
	}
	idx.directoryStates = nil
	idx.restoreScannerCounters()
	pass(false)
	assertQuickTotals(t, idx, 2, 5)
	pass(false)
	assertQuickTotals(t, idx, 2, 5)
}

func TestFullScanTopDirectoryDeletion(t *testing.T) {
	idx, write, pass := quickFixture(t)
	write("a/file", "abc")
	write("b/file", "12345")
	write("root", "12")
	pass(true)
	if err := os.RemoveAll(filepath.Join(idx.Path, "a")); err != nil {
		t.Fatal(err)
	}
	pass(true)
	assertQuickTotals(t, idx, 2, 7)
}

func TestBrowsingWhileDirectoryCacheRestoreHoldsSQLConnection(t *testing.T) {
	idx, write, pass := quickFixture(t)
	write("file.txt", "abc")
	pass(true)
	settings.Config.Server.IndexSqlConfig.Mode = "full"
	idx.Initialization = "restoring"
	rows, err := idx.db.Query("SELECT path FROM index_items")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	done := make(chan error, 1)
	go func() { _, err := idx.GetFileInfo(FileInfoRequest{IndexPath: "/", Expand: true}); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		rows.Close()
		<-done
		t.Fatal("browsing waited for the index restore connection")
	}
}

func TestQuickScanAllocatedSizeEmptyDirectoryTransitions(t *testing.T) {
	idx, write, pass := quickFixture(t)
	idx.Config.UseLogicalSize = false
	if err := os.Mkdir(filepath.Join(idx.Path, "a"), 0755); err != nil {
		t.Fatal(err)
	}
	pass(true)
	if got, _ := idx.GetFolderSize("/"); got != 4096 {
		t.Fatalf("empty child size=%d", got)
	}
	write("a/file", "abc")
	info, err := os.Stat(filepath.Join(idx.Path, "a/file"))
	if err != nil {
		t.Fatal(err)
	}
	want := max(uint64(getDiskUsage(info, false)), 4096)
	pass(false)
	assertQuickTotals(t, idx, 1, want)
	if err := os.Remove(filepath.Join(idx.Path, "a/file")); err != nil {
		t.Fatal(err)
	}
	pass(false)
	assertQuickTotals(t, idx, 0, 4096)
	if err := os.Remove(filepath.Join(idx.Path, "a")); err != nil {
		t.Fatal(err)
	}
	pass(false)
	assertQuickTotals(t, idx, 0, 0)
}
