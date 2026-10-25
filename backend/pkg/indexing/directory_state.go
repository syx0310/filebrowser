package indexing

import (
	"sort"
	"strings"

	"github.com/gtsteffaniak/filebrowser/backend/internal/utils"
	"github.com/gtsteffaniak/filebrowser/backend/pkg/indexing/iteminfo"
)

func (idx *Index) directoryState(path string) (iteminfo.DirectoryScanState, bool) {
	idx.folderSizesMu.RLock()
	defer idx.folderSizesMu.RUnlock()
	s, ok := idx.directoryStates[folderSizeLookupKey(path)]
	return s, ok
}

func scannerForDirectory(path string) string {
	if path == "/" {
		return "/"
	}
	return "/" + strings.Split(strings.TrimPrefix(path, "/"), "/")[0] + "/"
}

func (idx *Index) adjustScannerStats(path string, files, dirs int64) {
	owner := scannerForDirectory(path)
	idx.mu.RLock()
	scanner := idx.scanners[owner]
	idx.mu.RUnlock()
	if scanner == nil {
		return
	}
	scanner.statsMu.Lock()
	defer scanner.statsMu.Unlock()
	if scanner.fullScanRunning || !scanner.baselineReady {
		return
	} // validated at completion of the full pass
	adjust := func(n uint64, delta int64) uint64 {
		if delta < 0 && uint64(-delta) > n {
			return 0
		}
		return uint64(int64(n) + delta)
	}
	scanner.numFiles = adjust(scanner.numFiles, files)
	if owner != "/" {
		scanner.numDirs = adjust(scanner.numDirs, dirs)
	}
}

// Called under metadataMu, after the transaction has committed. Counters change
// only when the completed directory baseline changes, so replay is idempotent.
func (idx *Index) applyDirectoryStates(items []*iteminfo.FileInfo) {
	for _, item := range items {
		if item.ScanState == nil {
			continue
		}
		state := *item.ScanState
		path := folderSizeLookupKey(item.Path)
		idx.folderSizesMu.Lock()
		if idx.directoryStates == nil {
			idx.directoryStates = make(map[string]iteminfo.DirectoryScanState)
		}
		previous, existed := idx.directoryStates[path]
		if !state.Valid {
			if existed {
				previous.Valid = false
				idx.directoryStates[path] = previous
			}
			idx.folderSizesMu.Unlock()
			continue
		}
		idx.directoryStates[path] = state
		idx.folderSizesMu.Unlock()
		dirs := int64(0)
		if !existed {
			dirs = 1
		}
		idx.adjustScannerStats(path, int64(state.DirectFiles)-int64(previous.DirectFiles), dirs)
	}
}

func (idx *Index) dropDirectoryCache(path string, propagate bool) {
	path = folderSizeLookupKey(path)
	if propagate {
		if size, ok := idx.GetFolderSize(path); ok {
			idx.updateFolderSizeAndParents(path, 0, size, false)
		}
		zero := uint64(0)
		idx.updateFolderCountAndParents(path, &zero, idx.folderCount(path))
	}
	idx.folderSizesMu.Lock()
	removed := make(map[string]iteminfo.DirectoryScanState)
	for key, state := range idx.directoryStates {
		if strings.HasPrefix(key, path) {
			removed[key] = state
			delete(idx.directoryStates, key)
		}
	}
	for key := range idx.folderSizes {
		if strings.HasPrefix(key, path) {
			delete(idx.folderSizes, key)
			delete(idx.folderCounts, key)
			delete(idx.folderSizesUnsynced, key)
		}
	}
	idx.folderSizesMu.Unlock()
	for key, state := range removed {
		idx.adjustScannerStats(key, -int64(state.DirectFiles), -1)
	}
}

// One directory-only pass at startup restores authoritative counters, avoiding
// scans of file rows and stale counters from a crash before app-state Save().
func (idx *Index) restoreScannerCounters() {
	type counts struct{ files, dirs uint64 }
	byScanner := map[string]counts{}
	idx.folderSizesMu.RLock()
	for path, state := range idx.directoryStates {
		owner := scannerForDirectory(path)
		c := byScanner[owner]
		c.files += state.DirectFiles
		if owner != "/" {
			c.dirs++
		}
		byScanner[owner] = c
	}
	idx.folderSizesMu.RUnlock()
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for path, scanner := range idx.scanners {
		c := byScanner[path]
		_, baseline := idx.directoryState(path)
		idx.folderSizesMu.RLock()
		for directory := range idx.folderSizes {
			if scannerForDirectory(directory) == path {
				if _, exists := idx.directoryStates[directory]; !exists {
					baseline = false
					break
				}
			}
		}
		idx.folderSizesMu.RUnlock()
		scanner.withStatsLock(func() {
			scanner.baselineReady = baseline
			if baseline {
				scanner.numFiles, scanner.numDirs = c.files, c.dirs
			}
		})
	}
}

// Recover parent rollups from committed direct-directory summaries. This covers
// a crash after a directory transaction but before the deferred parent DB sync;
// no file rows need to be counted or loaded during startup.
func (idx *Index) restoreDirectoryAggregates() {
	idx.folderSizesMu.RLock()
	if len(idx.directoryStates) == 0 {
		idx.folderSizesMu.RUnlock()
		return
	}
	paths := make([]string, 0, len(idx.folderSizes))
	sizes, counts := make(map[string]uint64, len(idx.folderSizes)), make(map[string]uint64, len(idx.folderCounts))
	for path, size := range idx.folderSizes {
		paths = append(paths, path)
		sizes[path] = size
	}
	for path, count := range idx.folderCounts {
		counts[path] = count
	}
	for path, state := range idx.directoryStates {
		if _, ok := sizes[path]; !ok {
			paths = append(paths, path)
		}
		sizes[path] = uint64(max(state.DirectSize, 0))
		counts[path] = state.DirectFiles
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	for _, path := range paths {
		if path == "/" {
			continue
		}
		parent := folderSizeLookupKey(utils.GetParentDirectoryPath(path))
		if _, exists := idx.directoryStates[parent]; !exists {
			continue
		}
		size := sizes[path]
		if !idx.Config.UseLogicalSize {
			size = max(size, 4096)
		}
		sizes[parent] += size
		if count, known := counts[path]; known {
			if _, parentKnown := counts[parent]; parentKnown {
				counts[parent] += count
			}
		} else {
			delete(counts, parent)
		}
	}
	idx.folderSizesMu.RUnlock()
	idx.folderSizesMu.Lock()
	defer idx.folderSizesMu.Unlock()
	for path, size := range sizes {
		count, known := counts[path]
		oldCount, oldKnown := idx.folderCounts[path]
		if idx.folderSizes[path] != size || known != oldKnown || count != oldCount {
			idx.folderSizesUnsynced[path] = struct{}{}
		}
	}
	idx.folderSizes, idx.folderCounts = sizes, counts
}
