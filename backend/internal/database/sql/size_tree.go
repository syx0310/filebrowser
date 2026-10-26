package sql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/gtsteffaniak/filebrowser/backend/pkg/indexing/iteminfo"
)

const SizeTreeNodeLimit = 600
const sizeTreeDirectoryLimit = 24
const sizeTreeChildrenLimit = 80

// SizeTreeNode is an indexed directory rollup or file. A parent's size includes
// its descendants: callers must never add parent and child sizes together.
type SizeTreeNode struct {
	Name      string          `json:"name"`
	Path      string          `json:"path"`
	Size      int64           `json:"size"`
	IsDir     bool            `json:"isDir"`
	FileCount *uint64         `json:"fileCount,omitempty"`
	Modified  string          `json:"modified,omitempty"`
	Created   string          `json:"created,omitempty"`
	Expanded  bool            `json:"expanded"`
	HasMore   bool            `json:"hasMore"`
	Children  []*SizeTreeNode `json:"children,omitempty"`
}

func sizeTreeNode(item *iteminfo.FileInfo) *SizeTreeNode {
	node := &SizeTreeNode{Name: item.Name, Path: item.Path, Size: max(0, item.Size), IsDir: item.IsDir || item.Type == "directory", FileCount: item.FileCount}
	if !item.ModTime.IsZero() {
		node.Modified = item.ModTime.Format(time.RFC3339)
	}
	if item.Created != nil && !item.Created.IsZero() {
		node.Created = item.Created.Format(time.RFC3339)
	}
	return node
}

// ReadSizeTree reads a bounded two-level snapshot using source/parent_path
// lookups, without walking files, enumerating subtrees or counting SQL rows.
// Existing parent indexes bound sorting to the requested directories; no new
// full-library index build is required. Context cancellation bounds DB work.
func (db *IndexDB) ReadSizeTree(ctx context.Context, source, rootPath string, depth int, allowed func(*iteminfo.FileInfo) bool) (*SizeTreeNode, error) {
	if depth < 1 || depth > 2 {
		return nil, fmt.Errorf("depth must be 1 or 2")
	}
	opts := &sql.TxOptions{ReadOnly: true}
	if db.postgres {
		opts.Isolation = sql.LevelRepeatableRead
	}
	tx, err := db.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	bind := func(query string) string {
		if db.postgres {
			return bindPostgres(query)
		}
		return query
	}
	const columns = "path, name, size, created_time, mod_time, type, is_dir, is_hidden, has_preview, file_count"
	item, err := scanItem(tx.QueryRowContext(ctx, bind("SELECT "+columns+" FROM index_items WHERE source = ? AND path = ?"), source, rootPath))
	if err != nil {
		return nil, err
	}
	root := sizeTreeNode(item)
	if !root.IsDir {
		return nil, sql.ErrNoRows
	}
	remaining := SizeTreeNodeLimit - 1
	expanded := 0
	var expand func(*SizeTreeNode, int) error
	expand = func(parent *SizeTreeNode, levels int) error {
		if remaining == 0 || expanded >= sizeTreeDirectoryLimit {
			return nil
		}
		limit := min(sizeTreeChildrenLimit, remaining)
		rows, err := tx.QueryContext(ctx, bind("SELECT "+columns+" FROM index_items WHERE source = ? AND parent_path = ? AND path <> ? ORDER BY size DESC, path ASC LIMIT ?"), source, parent.Path, parent.Path, limit+1)
		if err != nil {
			return err
		}
		parent.Expanded = true
		expanded++
		seen := 0
		for rows.Next() {
			child, err := scanItem(rows)
			if err != nil {
				rows.Close()
				return err
			}
			seen++
			if seen > limit {
				parent.HasMore = true
				break
			}
			if allowed != nil && !allowed(child) {
				parent.HasMore = true
				continue
			}
			parent.Children = append(parent.Children, sizeTreeNode(child))
			remaining--
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if levels > 1 {
			for _, child := range parent.Children {
				if child.IsDir {
					if err := expand(child, levels-1); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := expand(root, depth); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return root, nil
}
