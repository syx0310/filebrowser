package sql

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gtsteffaniak/filebrowser/backend/pkg/indexing/iteminfo"
)

// Set FILEBROWSER_TEST_POSTGRES_DSN to a dedicated test database to exercise
// exactly the same operations against PostgreSQL as against SQLite.
func TestIndexBackendContract(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var db *IndexDB
			var err error
			if backend == "postgres" {
				dsn := os.Getenv("FILEBROWSER_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("FILEBROWSER_TEST_POSTGRES_DSN is not set")
				}
				db, _, err = NewPostgresIndexDB(dsn, 2)
			} else {
				defer pushTestIndexConfig(t, t.TempDir(), testIndexSQLConfig("probe"))()
				db, _, err = NewIndexDB("contract", "OFF", 2, 32, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			source := fmt.Sprintf("contract-%d", time.Now().UnixNano())
			defer db.Exec("DELETE FROM index_items WHERE source = ?", source)
			defer db.Exec("DELETE FROM index_sources WHERE source = ?", source)
			created := time.Unix(1700000000, 0)
			item := func(path string, size int64, dir bool) *iteminfo.FileInfo {
				typ := "application/octet-stream"
				if dir {
					typ = "directory"
				}
				parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
				return &iteminfo.FileInfo{Path: path, ItemInfo: iteminfo.ItemInfo{Name: parts[len(parts)-1], Size: size, Type: typ, Created: &created, ModTime: created.Add(time.Hour), Hidden: strings.HasPrefix(parts[len(parts)-1], ".")}}
			}
			if changed, err := db.PrepareSource(source, "initial", true); err != nil || !changed {
				t.Fatalf("prepare source: %v %v", changed, err)
			}
			items := []*iteminfo.FileInfo{item("/", 0, true), item("/dir/", 0, true), item("/dir/Alpha.txt", 1<<35, false), item("/dir/乙.txt", 10, false), item("/dir/.secret", 10, false), item("/dir/Alpha.txt", 1<<36, false)}
			if err := db.BulkInsertItems(source, items); err != nil {
				t.Fatal(err)
			}
			got, err := db.GetItem(source, "/dir/Alpha.txt")
			if err != nil || got.Size != 1<<36 || got.Created == nil || !got.Created.Equal(created) || !got.ModTime.Equal(created.Add(time.Hour)) {
				t.Fatalf("metadata roundtrip: %+v %v", got, err)
			}
			if err := db.InsertItem(source, "/root.txt", item("/root.txt", 7, false)); err != nil {
				t.Fatal(err)
			}
			if n, err := db.GetDirectFileCount(source, "/"); err != nil || n != 1 {
				t.Fatalf("root count: %d %v", n, err)
			}
			if dirs, files, err := db.GetRecursiveCount(source, "/dir/"); err != nil || dirs != 1 || files != 3 {
				t.Fatalf("recursive count: %d %d %v", dirs, files, err)
			}
			if children, err := db.GetDirectoryChildren(source, "/dir/"); err != nil || len(children) != 3 {
				t.Fatalf("children: %d %v", len(children), err)
			}
			if found, err := db.GetItemsByPaths(source, []string{"/dir/Alpha.txt", "/root.txt"}); err != nil || len(found) != 2 {
				t.Fatalf("lookup paths: %d %v", len(found), err)
			}
			if updated, err := db.UpdateFolderSizesIfChanged(source, map[string]uint64{"/": 1<<36 + 27, "/dir/": 1<<36 + 20}); err != nil || updated != 2 {
				t.Fatalf("size rollup: %d %v", updated, err)
			}
			if sizes, err := db.LoadFolderSizes(source); err != nil || sizes["/dir/"] != 1<<36+20 {
				t.Fatalf("load sizes: %v %v", sizes, err)
			}
			if n, err := db.UpdateFolderCountsIfChanged(source, map[string]uint64{"/": 4, "/dir/": 3}); err != nil || n != 2 {
				t.Fatalf("folder counts: %d %v", n, err)
			}
			if dir, err := db.GetItem(source, "/dir/"); err != nil || dir.FileCount == nil || *dir.FileCount != 3 {
				t.Fatalf("directory count roundtrip: %+v %v", dir, err)
			}
			if _, counts, err := db.LoadFolderStats(source); err != nil || counts["/"] != 4 {
				t.Fatalf("persisted counts: %v %v", counts, err)
			}
			rows, err := db.SearchItems(source, "/dir/", false, []string{"*alpha*"}, false, false)
			if err != nil {
				t.Fatal(err)
			}
			if n := countContractRows(t, rows); n != 1 {
				t.Fatalf("name search count=%d", n)
			}
			rows, err = db.SearchItemsMultiSource([]string{source}, map[string]string{source: "/dir/"}, false, []string{"*.txt", "*Alpha*"}, true, true)
			if err != nil {
				t.Fatal(err)
			}
			if n := countContractRows(t, rows); n != 1 {
				t.Fatalf("multi search count=%d", n)
			}
			if sizes, counts, err := db.GetSizeGroupsForDuplicates(source, 1, "/dir/"); err != nil || len(sizes) != 1 || counts[10] != 2 {
				t.Fatalf("duplicate groups: %v %v %v", sizes, counts, err)
			}
			if files, err := db.GetFilesBySize(source, 10, "/dir/"); err != nil || len(files) != 2 {
				t.Fatalf("files by size: %d %v", len(files), err)
			}
			if _, err := db.Exec("UPDATE index_items SET last_updated = 1 WHERE source = ? AND path = ?", source, "/dir/.secret"); err != nil {
				t.Fatal(err)
			}
			if n, err := db.DeleteStaleFilesInDirs(source, []string{"/dir/"}, 2); err != nil || n != 1 {
				t.Fatalf("stale files: %d %v", n, err)
			}
			if err := db.InsertItem(source, "/dir/old/", item("/dir/old/", 0, true)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("UPDATE index_items SET last_updated = 1 WHERE source = ? AND path = ?", source, "/dir/old/"); err != nil {
				t.Fatal(err)
			}
			if n, err := db.DeleteStaleFolders(source, "/dir/", 2, false); err != nil || n != 1 {
				t.Fatalf("stale directories: %d %v", n, err)
			}
			if _, err := db.Exec("UPDATE index_items SET last_updated = 1 WHERE source = ? AND path = ?", source, "/root.txt"); err != nil {
				t.Fatal(err)
			}
			if n, err := db.DeleteStaleEntries(source, "/", 2, true); err != nil || n != 1 {
				t.Fatalf("stale root: %d %v", n, err)
			}
			if err := db.DeleteItem(source, "/dir/", true); err != nil {
				t.Fatal(err)
			}
			if _, err := db.GetItem(source, "/dir/Alpha.txt"); err != sql.ErrNoRows {
				t.Fatalf("recursive delete: %v", err)
			}
			if changed, err := db.PrepareSource(source, "initial", true); err != nil || changed {
				t.Fatalf("unchanged policy: %v %v", changed, err)
			}
			if changed, err := db.PrepareSource(source, "changed", true); err != nil || !changed {
				t.Fatalf("changed policy: %v %v", changed, err)
			}
			if _, err := db.GetItem(source, "/"); err != sql.ErrNoRows {
				t.Fatalf("old policy data retained: %v", err)
			}
			if err := db.ShrinkMemory(); err != nil {
				t.Fatal(err)
			}
			if err := db.Optimize(); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExplainQueryPlan("SELECT path FROM index_items WHERE source = ?", source); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func countContractRows(t *testing.T, rows *sql.Rows) int {
	t.Helper()
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPostgresBinding(t *testing.T) {
	got := bindPostgres(`SELECT '?' AS "?", 'it''s ?' WHERE source = ? AND path = ?`)
	want := `SELECT '?' AS "?", 'it''s ?' WHERE source = $1 AND path = $2`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPostgresGlobParity(t *testing.T) {
	dsn := os.Getenv("FILEBROWSER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("FILEBROWSER_TEST_POSTGRES_DSN is not set")
	}
	pg, _, err := NewPostgresIndexDB(dsn, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	defer pushTestIndexConfig(t, t.TempDir(), testIndexSQLConfig("probe"))()
	sq, _, err := NewIndexDB("glob-parity", "OFF", 10, 32, true)
	if err != nil {
		t.Fatal(err)
	}
	defer sq.Close()
	for _, pattern := range []string{"*", "*.txt", "a?b", "[a-z]*", "[^a]*", "[]]", "[[]", "[-a]", "[", "[]", `a\b`, "*中文*", "*a.b*", "*a+b*"} {
		for _, name := range []string{"", "abc", "a-b", "z.txt", "中文.txt", "]", "[", "-", `a\b`, "a\nb", "a.b", "a+b"} {
			var want, got bool
			if err := sq.QueryRow("SELECT ? GLOB ?", name, pattern).Scan(&want); err != nil {
				t.Fatal(err)
			}
			if err := pg.QueryRow("SELECT ?::text ~ ?::text", name, postgresGlob(pattern)).Scan(&got); err != nil {
				t.Fatalf("pattern=%q: %v", pattern, err)
			}
			if got != want {
				t.Errorf("pattern=%q name=%q: postgres=%v sqlite=%v", pattern, name, got, want)
			}
		}
	}
}
