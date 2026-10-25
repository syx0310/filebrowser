package sql

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gtsteffaniak/filebrowser/backend/pkg/indexing/iteminfo"
)

func TestDirectoryStateBackendContract(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var db *IndexDB
			var err error
			if backend == "postgres" {
				dsn := os.Getenv("FILEBROWSER_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("test PostgreSQL DSN not set")
				}
				db, _, err = NewPostgresIndexDB(dsn, 100)
			} else {
				db, _, err = NewIndexDB("state_contract", "OFF", 100, 32, true)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			source := fmt.Sprintf("directory-contract-%d", time.Now().UnixNano())
			defer db.Exec("DELETE FROM index_items WHERE source = ?", source)
			defer db.Exec("DELETE FROM index_directory_state WHERE source = ?", source)
			root := &iteminfo.FileInfo{Path: "/", ItemInfo: iteminfo.ItemInfo{Type: "directory", ModTime: time.Now()}, ScanState: &iteminfo.DirectoryScanState{ModTimeNS: 100, DirectFiles: 1, DirectSize: 3, Valid: true}}
			file := &iteminfo.FileInfo{Path: "/old", ItemInfo: iteminfo.ItemInfo{Name: "old", Type: "blob", Size: 3}}
			if _, err := db.ReconcileDirectory(source, "/", []*iteminfo.FileInfo{root, file}); err != nil {
				t.Fatal(err)
			}
			states, err := db.LoadDirectoryStates(source)
			if err != nil || states["/"].DirectFiles != 1 || states["/"].ModTimeNS != 100 {
				t.Fatalf("states=%v err=%v", states, err)
			}
			// A read-through invalidation retains the authoritative previous counters.
			root.ScanState = &iteminfo.DirectoryScanState{}
			if err := db.BulkInsertItems(source, []*iteminfo.FileInfo{root}); err != nil {
				t.Fatal(err)
			}
			states, err = db.LoadDirectoryStates(source)
			if err != nil || states["/"].Valid || states["/"].DirectFiles != 1 {
				t.Fatalf("invalidation=%v %v", states, err)
			}
			root.ScanState = &iteminfo.DirectoryScanState{ModTimeNS: 200, Valid: true}
			removed, err := db.ReconcileDirectory(source, "/", []*iteminfo.FileInfo{root})
			if err != nil || len(removed) != 1 || removed[0] != "/old" {
				t.Fatalf("removed=%v err=%v", removed, err)
			}
			_, files, err := db.GetRecursiveCount(source, "/")
			if err != nil || files != 0 {
				t.Fatalf("files=%d err=%v", files, err)
			}
			if err := db.DeleteScannedSubtree(source, "/"); err != nil {
				t.Fatal(err)
			}
			states, err = db.LoadDirectoryStates(source)
			if err != nil || len(states) != 0 {
				t.Fatalf("deleted checkpoints=%v %v", states, err)
			}
		})
	}
}

func TestTransientIntegrityErrorPreservesIndex(t *testing.T) {
	defer pushTestIndexConfig(t, t.TempDir(), testIndexSQLConfig("probe"))()
	db, _, err := NewIndexDB("preserve", "OFF", 100, 32, false)
	if err != nil {
		t.Fatal(err)
	}
	path := db.Path()
	db.Close()
	if err := db.checkAccessibilityProbe(); err == nil {
		t.Fatal("closed connection should fail")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("transient failure removed the index: %v", err)
	}
}
