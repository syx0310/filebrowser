package indexing

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	indexerrors "github.com/gtsteffaniak/filebrowser/backend/internal/errors"
	"github.com/gtsteffaniak/filebrowser/backend/internal/utils"
	"github.com/gtsteffaniak/go-logger/logger"
)

func (s *Scanner) runQuickScan() {
	start := time.Now()
	s.scanFailed = false
	s.prepareScanState()
	s.withStatsLock(func() { s.filesChanged = false })
	paths := []string{s.scanPath}
	if s.scanPath != "/" {
		s.idx.folderSizesMu.RLock()
		for path := range s.idx.folderSizes {
			if path != s.scanPath && strings.HasPrefix(path, s.scanPath) {
				paths = append(paths, path)
			}
		}
		s.idx.folderSizesMu.RUnlock()
	}
	sort.Strings(paths) // parents first; removed subtrees are skipped below
	changed, failed := 0, 0
	for _, path := range paths {
		if s.idx.isStopped() {
			return
		}
		if path != s.scanPath {
			if _, exists := s.idx.GetFolderSize(path); !exists {
				continue
			}
		}
		didChange, err := s.checkFolderModtime(path)
		if err != nil {
			failed++
			logger.Warningf("[QUICK_SCAN] [%s] retain old index for %s: %v", s.idx.Name, path, err)
		} else if didChange {
			changed++
		}
	}
	if failed == 0 {
		s.completeQuickBaseline()
	}
	s.resetHardlinkTracking()
	logger.Infof("[QUICK_SCAN] [%s:%s] checked=%d changed=%d failed=%d duration=%v", s.idx.Name, s.scanPath, len(paths), changed, failed, time.Since(start).Round(time.Millisecond))
}

func (s *Scanner) checkFolderModtime(folderPath string) (bool, error) {
	folderPath = folderSizeLookupKey(folderPath)
	realPath := utils.JoinUnderSourceRoot(s.idx.Path, folderPath)
	info, err := os.Stat(realPath)
	if err != nil {
		if os.IsNotExist(err) && folderPath != "/" {
			return true, s.removeScannedDirectory(folderPath)
		}
		return false, err
	}
	if !info.IsDir() {
		return true, s.removeScannedDirectory(folderPath)
	}
	state, known := s.idx.directoryState(folderPath)
	if known && state.Valid && state.ModTimeNS == info.ModTime().UnixNano() {
		return false, nil
	}
	s.withStatsLock(func() { s.filesChanged = true })
	if s.idx.excludesHiddenPath(folderPath, IsHidden(realPath)) || s.idx.ShouldSkip(true, folderPath, IsHidden(realPath), false, true) {
		return true, s.removeScannedDirectory(folderPath)
	}
	dir, err := os.Open(realPath)
	if err != nil {
		return true, err
	}
	defer dir.Close()
	before, err := dir.Stat()
	if err != nil {
		return true, err
	}
	listing, err := s.idx.GetDirInfo(dir, before, folderPath, Options{
		IsRoutineScan: true, ShowHidden: s.idx.includeHidden(), SizeOnly: s.idx.sizeOnly(), SkipScanCounters: true,
	}, s)
	if err == indexerrors.ErrNotIndexed {
		return true, s.removeScannedDirectory(folderPath)
	}
	if err != nil {
		return true, err
	}
	// Discover new nested subtrees immediately. Existing child directories have
	// their own checkpoint and will be checked by the outer quick-scan loop.
	if s.scanPath != "/" {
		for _, child := range listing.Folders {
			childPath := folderPath + child.Name + "/"
			if _, exists := s.idx.directoryState(childPath); !exists {
				if _, err := s.checkFolderModtime(childPath); err != nil {
					return true, err
				}
			}
		}
	}
	// Recompute this listing with newly discovered child aggregates. DirectSize
	// preserves the existing allocation/hardlink accounting from the reader.
	listing.Size = listing.ScanState.DirectSize
	count, complete := listing.ScanState.DirectFiles, true
	for i := range listing.Folders {
		child := &listing.Folders[i]
		path := folderPath + child.Name + "/"
		child.Size = s.idx.GetFolderSizeForDisplay(path)
		child.FileCount = s.idx.folderCount(path)
		listing.Size += child.Size
		if child.FileCount == nil {
			complete = false
		} else {
			count += *child.FileCount
		}
	}
	listing.FileCount = nil
	if complete {
		listing.FileCount = &count
	}
	after, err := os.Stat(realPath)
	if err != nil {
		return true, err
	}
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		return true, fmt.Errorf("directory changed during listing; retry next pass")
	}
	items := s.idx.directoryItems(listing, true)
	s.idx.metadataMu.Lock()
	defer s.idx.metadataMu.Unlock()
	removed, err := s.idx.db.ReconcileDirectory(s.idx.Name, folderPath, items)
	if err != nil {
		return true, err
	}
	previousSize, hadSize := s.idx.GetFolderSize(folderPath)
	previousCount := s.idx.folderCount(folderPath)
	for _, path := range removed {
		if strings.HasSuffix(path, "/") {
			s.idx.dropDirectoryCache(path, false)
		}
	}
	s.idx.applyDirectoryStates(items)
	if hadSize {
		s.idx.updateFolderSizeAndParents(folderPath, uint64(listing.Size), previousSize, true)
		s.idx.updateFolderCountAndParents(folderPath, listing.FileCount, previousCount)
	} else {
		// A newly discovered directory is incorporated by its parent's listing;
		// propagating here as well would count the new subtree twice.
		s.idx.SetFolderSize(folderPath, uint64(listing.Size))
		s.idx.setFolderCount(folderPath, listing.FileCount)
	}
	return true, nil
}

func (s *Scanner) removeScannedDirectory(path string) error {
	return s.removeScannedDirectoryWithPropagation(path, true)
}

func (s *Scanner) removeScannedDirectoryWithPropagation(path string, propagate bool) error {
	s.idx.metadataMu.Lock()
	defer s.idx.metadataMu.Unlock()
	if err := s.idx.db.DeleteScannedSubtree(s.idx.Name, path); err != nil {
		return err
	}
	s.idx.dropDirectoryCache(path, propagate)
	s.withStatsLock(func() { s.filesChanged = true })
	if path == s.scanPath {
		s.removeSelf()
	}
	return nil
}

func (idx *Index) restoreDirectoryStates() error {
	states, err := idx.db.LoadDirectoryStates(idx.Name)
	if err != nil {
		return err
	}
	idx.folderSizesMu.Lock()
	idx.directoryStates = states
	idx.folderSizesMu.Unlock()
	return nil
}

// fullScanComplete retains the periodic SQL reconciliation on full scans, while
// quick scans use transactionally committed directory deltas exclusively.
func (s *Scanner) fullScanComplete(root bool) {
	s.flushBatch()
	if s.scanFailed {
		s.withStatsLock(func() { s.fullScanCounter = 0 })
		logger.Warningf("[FULL_SCAN] [%s] incomplete scan; keeping old entries and retrying a full pass", s.scanPath)
		return
	}
	s.purgeStaleEntries(root, false)
	if err := s.idx.db.PruneDirectoryStates(s.idx.Name, s.scanPath); err != nil {
		logger.Warningf("prune directory checkpoints: %v", err)
	}
	if !root {
		s.idx.folderSizesMu.RLock()
		var gone []string
		for path := range s.idx.folderSizes {
			if strings.HasPrefix(path, s.scanPath) && !s.fullSeen[path] {
				gone = append(gone, path)
			}
		}
		s.idx.folderSizesMu.RUnlock()
		sort.Strings(gone)
		for _, path := range gone {
			s.idx.dropDirectoryCache(path, false)
		}
	}

	s.syncStatsWithDB()
	s.withStatsLock(func() { s.baselineReady = true })
}

// Legacy indexes acquire a baseline on their next scheduled pass, without
// resetting the saved schedule or forcing a startup-wide filesystem walk.
func (s *Scanner) completeQuickBaseline() {
	s.statsMu.RLock()
	ready := s.baselineReady
	s.statsMu.RUnlock()
	if ready {
		return
	}
	var files, dirs uint64
	s.idx.folderSizesMu.RLock()
	_, complete := s.idx.directoryStates[s.scanPath]
	for path := range s.idx.folderSizes {
		if scannerForDirectory(path) != s.scanPath {
			continue
		}
		state, ok := s.idx.directoryStates[path]
		if !ok {
			complete = false
			break
		}
		files += state.DirectFiles
		if s.scanPath != "/" {
			dirs++
		}
	}
	s.idx.folderSizesMu.RUnlock()
	if complete {
		s.withStatsLock(func() { s.numFiles, s.numDirs, s.baselineReady = files, dirs, true })
	}
}
