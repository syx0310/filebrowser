package indexing

import "github.com/gtsteffaniak/filebrowser/backend/internal/utils"

func (idx *Index) folderCount(path string) *uint64 {
	idx.folderSizesMu.RLock()
	defer idx.folderSizesMu.RUnlock()
	if count, ok := idx.folderCounts[folderSizeLookupKey(path)]; ok {
		return &count
	}
	return nil
}

func (idx *Index) setFolderCount(path string, count *uint64) {
	if count == nil {
		return
	}
	idx.folderSizesMu.Lock()
	defer idx.folderSizesMu.Unlock()
	if idx.folderCounts == nil {
		idx.folderCounts = make(map[string]uint64)
	}
	idx.folderCounts[folderSizeLookupKey(path)] = *count
	idx.folderSizesUnsynced[folderSizeLookupKey(path)] = struct{}{}
}

func (idx *Index) updateFolderCountAndParents(path string, count *uint64, previous *uint64) {
	idx.setFolderCount(path, count)
	if count == nil || previous == nil || *count == *previous {
		return
	}
	idx.folderSizesMu.Lock()
	defer idx.folderSizesMu.Unlock()
	for path != "/" {
		parent := utils.GetParentDirectoryPath(path)
		if parent == "" {
			break
		}
		path = folderSizeLookupKey(parent)
		old, known := idx.folderCounts[path]
		if !known {
			continue
		}
		if *count >= *previous {
			old += *count - *previous
		} else {
			delta := *previous - *count
			if old >= delta {
				old -= delta
			} else {
				old = 0
			}
		}
		idx.folderCounts[path] = old
		idx.folderSizesUnsynced[path] = struct{}{}
	}
}
