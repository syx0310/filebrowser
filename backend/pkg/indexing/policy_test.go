package indexing

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	dbsql "github.com/gtsteffaniak/filebrowser/backend/internal/database/sql"
	"github.com/gtsteffaniak/filebrowser/backend/pkg/settings"
)

func TestSizeModeAndHiddenAccounting(t *testing.T) {
	previous := settings.Config.Server.IndexSqlConfig
	t.Cleanup(func() { settings.Config.Server.IndexSqlConfig = previous })
	for _, hidden := range []bool{false, true} {
		t.Run(map[bool]string{false: "exclude-hidden", true: "include-hidden"}[hidden], func(t *testing.T) {
			settings.Config.Server.IndexSqlConfig.Mode = "size"
			settings.Config.Server.IndexSqlConfig.IncludeHidden = hidden
			root := t.TempDir()
			files := map[string]string{"visible.txt": "123", ".hidden": "12345", "docs/readme.txt": "1234567", "docs/.cache/blob": "12345678901", ".top/content": "1234567890123", "image.unknown": "\x89PNG\r\n\x1a\n"}
			modified := time.Unix(1700000100, 0)
			for name, data := range files {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, modified, modified); err != nil {
					t.Fatal(err)
				}
			}
			db, _, err := dbsql.NewIndexDB("policy", "OFF", 10, 32, true)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			idx := &Index{Source: settings.Source{Name: "policy", Path: root, Config: settings.SourceConfig{UseLogicalSize: true}}, db: db, folderSizes: map[string]uint64{}, folderSizesUnsynced: map[string]struct{}{}, scanUpdatedPaths: map[string]bool{}, scanners: map[string]*Scanner{}}
			// Exercise the real split-scanner setup: top-level hidden directories
			// must use the same policy as hidden directories deeper in the tree.
			scanner := idx.createScanner("/")
			top := scanner.getTopLevelDirs()
			wantTop := 1
			if hidden {
				wantTop = 2
			}
			if len(top) != wantTop {
				t.Fatalf("top-level directories=%v", top)
			}
			total, _, err := idx.indexDirectory("/", Options{Recursive: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantSize := int64(3 + 7 + 8)
			wantFiles := uint64(3)
			if hidden {
				wantSize += 5 + 11 + 13
				wantFiles += 3
			}
			if total != wantSize {
				t.Fatalf("size=%d want %d", total, wantSize)
			}
			if count := idx.folderCount("/"); count == nil || *count != wantFiles {
				t.Fatalf("recursive count=%v want %d", count, wantFiles)
			}
			_, count, err := db.GetRecursiveCount(idx.Name, "/")
			if err != nil || count != wantFiles {
				t.Fatalf("file count=%d want %d: %v", count, wantFiles, err)
			}
			info, err := db.GetItem(idx.Name, "/image.unknown")
			if err != nil {
				t.Fatal(err)
			}
			if info.Type != "blob" || info.HasPreview || !info.ModTime.Equal(modified) || info.Size != 8 {
				t.Fatalf("basic metadata=%+v", info)
			}
			fsInfo, err := os.Stat(filepath.Join(root, "image.unknown"))
			if err != nil {
				t.Fatal(err)
			}
			created := getCreatedTime(fsInfo, filepath.Join(root, "image.unknown"))
			if created != nil && (info.Created == nil || info.Created.Unix() != created.Unix()) {
				t.Fatalf("created time lost: got=%v want=%v", info.Created, created)
			}
			for _, show := range []bool{false, true} {
				listing, err := idx.GetFileInfo(FileInfoRequest{IndexPath: "/", Expand: true, ShowHidden: show})
				if err != nil {
					t.Fatal(err)
				}
				for _, f := range listing.Files {
					if f.Hidden && !show {
						t.Fatal("hidden item exposed in listing")
					}
				}
				if listing.Size != wantSize {
					t.Fatalf("UI showHidden=%v changed size to %d (want %d)", show, listing.Size, wantSize)
				}
				if listing.FileCount == nil || *listing.FileCount != wantFiles {
					t.Fatalf("listing count=%v want %d", listing.FileCount, wantFiles)
				}
				if err := idx.SyncFolderSizesToDB(); err != nil {
					t.Fatal(err)
				}
				sizes, err := db.LoadFolderSizes(idx.Name)
				if err != nil || sizes["/"] != uint64(wantSize) {
					t.Fatalf("persisted size=%v err=%v", sizes, err)
				}
				_, count, err := db.GetRecursiveCount(idx.Name, "/")
				if err != nil || count != wantFiles {
					t.Fatalf("UI listing changed indexed count=%d: %v", count, err)
				}
				basic, err := db.GetItem(idx.Name, "/image.unknown")
				if err != nil || basic.Type != "blob" || basic.HasPreview {
					t.Fatalf("UI persisted probes: %+v %v", basic, err)
				}
			}
			// Browsing an excluded hidden directory must not reintroduce its
			// normal-named children into the index via the API write-through path.
			if _, err := idx.GetFileInfo(FileInfoRequest{IndexPath: "/.top/", Expand: true, ShowHidden: true}); err != nil {
				t.Fatal(err)
			}
			_, count, err = db.GetRecursiveCount(idx.Name, "/")
			if err != nil || count != wantFiles {
				t.Fatalf("browsing hidden directory changed count=%d: %v", count, err)
			}
			if err := os.WriteFile(filepath.Join(root, "docs/new.txt"), []byte("new"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := idx.RefreshDirectory("/docs/", false); err != nil {
				t.Fatal(err)
			}
			if count := idx.folderCount("/"); count == nil || *count != wantFiles+1 {
				t.Fatalf("parent count not updated: %v", count)
			}
			if err := idx.SyncFolderSizesToDB(); err != nil {
				t.Fatal(err)
			}
			idx.folderSizesMu.Lock()
			idx.folderCounts = nil
			idx.folderSizes = map[string]uint64{}
			idx.folderSizesMu.Unlock()
			idx.restoreFolderSizes()
			if count := idx.folderCount("/"); count == nil || *count != wantFiles+1 {
				t.Fatalf("count not restored: %v", count)
			}
			if size, _ := idx.GetFolderSize("/"); size != uint64(wantSize+3) {
				t.Fatalf("parent size not updated: %d", size)
			}
		})
	}
}
