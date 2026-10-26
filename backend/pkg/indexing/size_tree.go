package indexing

import (
	"context"
	"errors"

	dbsql "github.com/gtsteffaniak/filebrowser/backend/internal/database/sql"
	"github.com/gtsteffaniak/filebrowser/backend/pkg/indexing/iteminfo"
)

var ErrIndexNotReady = errors.New("index is still initializing")

func (idx *Index) ReadSizeTree(ctx context.Context, path string, depth int, allowed func(*iteminfo.FileInfo) bool) (*dbsql.SizeTreeNode, error) {
	if idx.db == nil || idx.isInitializing() {
		return nil, ErrIndexNotReady
	}
	return idx.db.ReadSizeTree(ctx, idx.Name, path, depth, allowed)
}
