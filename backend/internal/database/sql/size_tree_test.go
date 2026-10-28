package sql

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gtsteffaniak/filebrowser/backend/pkg/indexing/iteminfo"
)

func TestSizeTreeSnapshot(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var db *IndexDB
			var err error
			if backend == "postgres" {
				dsn := os.Getenv("FILEBROWSER_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("no PostgreSQL test DSN")
				}
				db, _, err = NewPostgresIndexDB(dsn, 100)
			} else {
				db, _, err = NewIndexDB("size-tree", "OFF", 100, 32, true)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			source := fmt.Sprintf("size-tree-%d", time.Now().UnixNano())
			defer db.Exec("DELETE FROM index_items WHERE source = ?", source)
			makeItem := func(path string, size int64, dir bool) *iteminfo.FileInfo {
				typ := "blob"
				if dir {
					typ = "directory"
				}
				parts := strings.Split(strings.Trim(path, "/"), "/")
				return &iteminfo.FileInfo{Path: path, ItemInfo: iteminfo.ItemInfo{Name: parts[len(parts)-1], Size: size, Type: typ}}
			}
			items := []*iteminfo.FileInfo{makeItem("/", 10000, true), makeItem("/a/", 9000, true), makeItem("/a/nested/", 8000, true), makeItem("/a/nested/deep", 8000, false), makeItem("/root-file", 1000, false)}
			if err := db.BulkInsertItems(source, items); err != nil {
				t.Fatal(err)
			}
			tree, err := db.ReadSizeTree(context.Background(), source, "/", 2, SizeTreeDefaultChildrenLimit, nil)
			if err != nil || tree.Size != 10000 || len(tree.Children) != 2 {
				t.Fatalf("tree=%+v err=%v", tree, err)
			}
			folder := tree.Children[0]
			if !folder.Expanded || len(folder.Children) != 1 || folder.Children[0].Expanded || len(folder.Children[0].Children) > 0 {
				t.Fatalf("depth not bounded: %+v", folder)
			}
			largest, err := db.ReadSizeTree(context.Background(), source, "/", 2, 1, nil)
			if err != nil || len(largest.Children) != 1 || largest.Children[0].Path != "/a/" || !largest.HasMore {
				t.Fatalf("largest child was not selected before limiting: %+v %v", largest, err)
			}
			filtered, err := db.ReadSizeTree(context.Background(), source, "/", 2, SizeTreeDefaultChildrenLimit, func(i *iteminfo.FileInfo) bool { return i.Path != "/a/" })
			if err != nil || len(filtered.Children) != 1 || filtered.Children[0].Path != "/root-file" || !filtered.HasMore {
				t.Fatalf("filter leaked a subtree: %+v %v", filtered, err)
			}
			for i := 0; i < 100; i++ {
				items = append(items, makeItem(fmt.Sprintf("/wide-%03d/", i), 100, true))
				for j := 0; j < 100; j++ {
					items = append(items, makeItem(fmt.Sprintf("/wide-%03d/file-%03d", i, j), 1, false))
				}
			}
			if err := db.BulkInsertItems(source, items); err != nil {
				t.Fatal(err)
			}
			tree, err = db.ReadSizeTree(context.Background(), source, "/", 2, SizeTreeDefaultChildrenLimit, nil)
			if err != nil {
				t.Fatal(err)
			}
			nodes, expanded := 0, 0
			var visit func(*SizeTreeNode)
			visit = func(n *SizeTreeNode) {
				nodes++
				if n.Expanded {
					expanded++
				}
				for _, c := range n.Children {
					visit(c)
				}
			}
			visit(tree)
			if nodes > SizeTreeNodeLimit || expanded > sizeTreeDirectoryLimit || !tree.HasMore {
				t.Fatalf("budget nodes=%d expanded=%d", nodes, expanded)
			}
			tree, err = db.ReadSizeTree(context.Background(), source, "/", 2, 200, nil)
			if err != nil {
				t.Fatal(err)
			}
			nodes, expanded = 0, 0
			visit(tree)
			if nodes != SizeTreeNodeBudget(200) || expanded > sizeTreeDirectoryLimit {
				t.Fatalf("configured budget nodes=%d want=%d expanded=%d", nodes, SizeTreeNodeBudget(200), expanded)
			}
			many := []*iteminfo.FileInfo{makeItem("/many/", 1200*1201/2, true)}
			for i := 1; i <= 1200; i++ {
				many = append(many, makeItem(fmt.Sprintf("/many/file-%04d", i), int64(i), false))
			}
			if err := db.BulkInsertItems(source, many); err != nil {
				t.Fatal(err)
			}
			for _, limit := range []int{1, SizeTreeDefaultChildrenLimit, 200, SizeTreeMaxChildrenLimit} {
				tree, err = db.ReadSizeTree(context.Background(), source, "/many/", 2, limit, nil)
				if err != nil || len(tree.Children) != limit || !tree.HasMore {
					t.Fatalf("limit=%d tree=%+v err=%v", limit, tree, err)
				}
				for i, child := range tree.Children {
					if child.Size != int64(1200-i) {
						t.Fatalf("limit=%d position=%d size=%d is not descending", limit, i, child.Size)
					}
				}
			}
			for _, limit := range []int{-1, 0, SizeTreeMaxChildrenLimit + 1} {
				if _, err := db.ReadSizeTree(context.Background(), source, "/", 2, limit, nil); err == nil {
					t.Fatalf("accepted invalid limit %d", limit)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := db.ReadSizeTree(ctx, source, "/", 2, SizeTreeDefaultChildrenLimit, nil); err == nil {
				t.Fatal("ignored cancellation")
			}
		})
	}
}

func TestSizeTreeNodeBudget(t *testing.T) {
	for _, tc := range []struct{ children, nodes int }{{1, 600}, {80, 600}, {200, 1500}, {1000, 6000}} {
		if got := SizeTreeNodeBudget(tc.children); got != tc.nodes {
			t.Fatalf("children=%d nodes=%d want=%d", tc.children, got, tc.nodes)
		}
	}
}
