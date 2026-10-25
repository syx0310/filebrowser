package indexing

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	dbsql "github.com/gtsteffaniak/filebrowser/backend/internal/database/sql"
	"github.com/gtsteffaniak/filebrowser/backend/pkg/settings"
)

// Kept compatible with the preceding release for a same-host before/after run.
// The full pass builds a real baseline; only repeated unchanged quick passes
// are timed. This is a warm-cache fixture, not a cold ZFS/HDD benchmark.
func BenchmarkQuickScanLargeTree(b *testing.B) {
	for _, backend := range []string{"sqlite", "postgres"} {
		b.Run(backend, func(b *testing.B) {
			previous := settings.Config.Server.IndexSqlConfig
			defer func() { settings.Config.Server.IndexSqlConfig = previous }()
			settings.Config.Server.IndexSqlConfig.Mode = "size"
			settings.Config.Server.IndexSqlConfig.IncludeHidden = true
			var db *dbsql.IndexDB
			var err error
			if backend == "postgres" {
				dsn := os.Getenv("FILEBROWSER_TEST_POSTGRES_DSN")
				if dsn == "" {
					b.Skip("test PostgreSQL DSN not set")
				}
				db, _, err = dbsql.NewPostgresIndexDB(dsn, 1000)
			} else {
				db, _, err = dbsql.NewIndexDB("quick_benchmark", "OFF", 1000, 32, true)
			}
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			root := b.TempDir()
			for d := 0; d < 1000; d++ {
				path := filepath.Join(root, "tree", fmt.Sprintf("d%04d", d))
				if err := os.MkdirAll(path, 0755); err != nil {
					b.Fatal(err)
				}
				for f := 0; f < 10; f++ {
					if err := os.WriteFile(filepath.Join(path, fmt.Sprintf("f%d", f)), []byte("benchmark"), 0644); err != nil {
						b.Fatal(err)
					}
				}
			}
			source := fmt.Sprintf("bench-%d", time.Now().UnixNano())
			defer db.Exec("DELETE FROM index_items WHERE source = ?", source)
			defer db.Exec("DELETE FROM index_directory_state WHERE source = ?", source)
			idx := &Index{mock: true, Source: settings.Source{Name: source, Path: root, Config: settings.SourceConfig{UseLogicalSize: true}}, db: db,
				folderSizes: map[string]uint64{}, folderCounts: map[string]uint64{}, folderSizesUnsynced: map[string]struct{}{},
				scanUpdatedPaths: map[string]bool{}, scanners: map[string]*Scanner{}}
			scanner := idx.createScanner("/tree/")
			idx.scanners["/tree/"] = scanner
			idx.activeScannerPath = "/tree/"
			scanner.runChildScan(false)
			if scanner.numFiles != 10000 || scanner.numDirs != 1001 {
				b.Fatalf("baseline=%d files/%d dirs", scanner.numFiles, scanner.numDirs)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				scanner.runQuickScanChild()
			}
			b.StopTimer()
			if scanner.numFiles != 10000 || scanner.numDirs != 1001 {
				b.Fatalf("quick scan changed counters")
			}
		})
	}
}
