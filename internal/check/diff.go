package check

import (
	"cmp"
	"slices"
)

type DiffResult struct {
	AddedFiles    []AddedFile
	RemovedFiles  []RemovedFile
	ModifiedFiles []ModifiedFile
	MovedFiles    []MovedFile
}

func (d *DiffResult) Empty() bool {
	return len(d.AddedFiles)+len(d.RemovedFiles)+len(d.ModifiedFiles)+len(d.MovedFiles) == 0
}

func computeDiff(oldSums, newSums map[string]fileInfo) *DiffResult {
	type addInfo struct{ checksum string }
	type remInfo struct{ checksum string }
	type modInfo struct{ oldChecksum, newChecksum string }

	// Mapping of old checksums to file paths
	oldSumsReverse := make(map[string][]string, len(oldSums))
	for path, info := range oldSums {
		oldSumsReverse[info.Checksum] = append(oldSumsReverse[info.Checksum], path)
	}

	added := make(map[string]addInfo)
	removed := make(map[string]remInfo)
	modified := make(map[string]modInfo)
	var moved []MovedFile

	for path, newInfo := range newSums {
		if oldInfo, ok := oldSums[path]; !ok {
			added[path] = addInfo{checksum: newInfo.Checksum}
		} else if oldInfo.Checksum != newInfo.Checksum {
			// Treat modified files like an add + remove for better tracking of moved files.
			// For example, if two files swap paths we want to treat that as two moves rather
			// than two modifications.
			added[path] = addInfo{checksum: newInfo.Checksum}
			removed[path] = remInfo{checksum: oldInfo.Checksum}
			modified[path] = modInfo{
				oldChecksum: oldInfo.Checksum,
				newChecksum: newInfo.Checksum,
			}
		}
	}
	for path, info := range oldSums {
		if _, ok := newSums[path]; !ok {
			removed[path] = remInfo{checksum: info.Checksum}
		}
	}

	// If a file was added with the same checksum as a file that was removed, treat it
	// as a moved file
	for addedPath, addInfo := range added {
		for _, oldPath := range oldSumsReverse[addInfo.checksum] {
			if _, wasRemoved := removed[oldPath]; wasRemoved {
				moved = append(moved, MovedFile{
					From: oldPath,
					To:   addedPath,
				})
				delete(added, addedPath)
				delete(removed, oldPath)
				delete(modified, oldPath)
			}
		}
	}

	// For any modified files that remain after handling moves, make sure we delete
	// the extra add + remove to avoid duplicate reporting.
	for path := range modified {
		delete(added, path)
		delete(removed, path)
	}

	addedFiles := make([]AddedFile, 0, len(added))
	for path, info := range added {
		addedFiles = append(addedFiles, AddedFile{Path: path, Checksum: info.checksum})
	}
	slices.SortFunc(addedFiles, cmpFunc)

	removedFiles := make([]RemovedFile, 0, len(removed))
	for path, info := range removed {
		removedFiles = append(removedFiles, RemovedFile{Path: path, Checksum: info.checksum})
	}
	slices.SortFunc(removedFiles, cmpFunc)

	modifiedFiles := make([]ModifiedFile, 0, len(modified))
	for path, info := range modified {
		modifiedFiles = append(modifiedFiles, ModifiedFile{
			Path:        path,
			OldChecksum: info.oldChecksum,
			NewChecksum: info.newChecksum,
		})
	}
	slices.SortFunc(modifiedFiles, cmpFunc)

	slices.SortFunc(moved, cmpFunc)

	return &DiffResult{
		AddedFiles:    addedFiles,
		RemovedFiles:  removedFiles,
		ModifiedFiles: modifiedFiles,
		MovedFiles:    moved,
	}
}

type AddedFile struct {
	Path     string
	Checksum string
}

func (f AddedFile) Cmp(other AddedFile) int {
	return cmp.Compare(f.Path, other.Path)
}

type RemovedFile struct {
	Path     string
	Checksum string
}

func (f RemovedFile) Cmp(other RemovedFile) int {
	return cmp.Compare(f.Path, other.Path)
}

type ModifiedFile struct {
	Path        string
	OldChecksum string
	NewChecksum string
}

func (f ModifiedFile) Cmp(other ModifiedFile) int {
	return cmp.Compare(f.Path, other.Path)
}

type MovedFile struct {
	From string
	To   string
}

func (f MovedFile) Cmp(other MovedFile) int {
	return cmp.Compare(f.From, other.From)
}

type comparable[T any] interface {
	Cmp(T) int
}

func cmpFunc[T comparable[T]](a, b T) int {
	return a.Cmp(b)
}
