package indexing

import (
	"context"
	"time"

	dbsql "github.com/gtsteffaniak/filebrowser/backend/internal/database/sql"
	"github.com/gtsteffaniak/filebrowser/backend/pkg/settings"
	"github.com/gtsteffaniak/go-logger/logger"
	"golang.org/x/net/webdav"
)

// Sources remain browsable while the rebuildable index is being opened. Pending
// objects keep db=nil for their lifetime and are replaced, never mutated to a
// live database pointer while requests are using them.
func pendingIndex(source *settings.Source) *Index {
	return &Index{Source: *source, ReducedIndex: ReducedIndex{IdxName: source.Name, Status: INDEXING, Initialization: "opening"},
		folderSizes: map[string]uint64{}, folderSizesUnsynced: map[string]struct{}{}, WebdavLock: webdav.NewMemLS()}
}

func (idx *Index) isInitializing() bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.Initialization != ""
}

func StartBackgroundInitialization(ctx context.Context, sources []*settings.Source, onNew func()) <-chan struct{} {
	return startBackgroundInitialization(ctx, sources, onNew, openSharedIndexDB)
}

func startBackgroundInitialization(ctx context.Context, sources []*settings.Source, onNew func(), open func() (*dbsql.IndexDB, bool, error)) <-chan struct{} {
	indexesMutex.Lock()
	for _, source := range sources {
		indexes[source.Name] = pendingIndex(source)
	}
	indexesMutex.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if ctx.Err() != nil {
				return
			}
			start := time.Now()
			logger.Info("[INDEX_STARTUP] opening index in background; HTTP and filesystem browsing remain available")
			db, isNew, err := open()
			if ctx.Err() != nil {
				if db != nil {
					_ = db.Close()
				}
				return
			}
			if err != nil {
				logger.Errorf("[INDEX_STARTUP] index unavailable, retrying in 30s: %v", err)
				indexesMutex.Lock()
				for _, source := range sources {
					idx := indexes[source.Name]
					idx.mu.Lock()
					idx.Status = UNAVAILABLE
					idx.Initialization = "failed"
					idx.mu.Unlock()
				}
				indexesMutex.Unlock()
				for _, source := range sources {
					_ = GetIndex(source.Name).SendSourceUpdateEvent()
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(30 * time.Second):
					continue
				}
			}
			setIndexDB(db)
			if isNew && onNew != nil {
				onNew()
			}
			for _, source := range sources {
				if ctx.Err() != nil {
					return
				}
				Initialize(source, false, isNew)
			}
			logger.Infof("[INDEX_STARTUP] database opened in %v; restoring source caches in background", time.Since(start).Round(time.Millisecond))
			return
		}
	}()
	return done
}

func (idx *Index) failInitialization(err error) {
	logger.Errorf("[INDEX_STARTUP] [%s] restore failed, retrying in 30s: %v", idx.Name, err)
	idx.mu.Lock()
	idx.Initialization, idx.Status = "failed", UNAVAILABLE
	idx.mu.Unlock()
	_ = idx.SendSourceUpdateEvent()
	time.AfterFunc(30*time.Second, func() {
		if idx.isStopped() {
			return
		}
		idx.mu.Lock()
		idx.Initialization, idx.Status = "restoring", INDEXING
		idx.mu.Unlock()
		idx.setupMultiScanner(false)
	})
}
