package main

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

func (d *DiffResult) empty() bool {
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

	addedFiles := make(map[string]addInfo)
	removedFiles := make(map[string]remInfo)
	modifiedFiles := make(map[string]modInfo)
	var movedFiles []MovedFile

	for path, newInfo := range newSums {
		if oldInfo, ok := oldSums[path]; !ok {
			addedFiles[path] = addInfo{checksum: newInfo.Checksum}
		} else if oldInfo.Checksum != newInfo.Checksum {
			// Treat modified files like an add + remove for better tracking of moved files.
			// For example, if two files swap paths we want to treat that as two moves rather
			// than two modifications.
			addedFiles[path] = addInfo{checksum: newInfo.Checksum}
			removedFiles[path] = remInfo{checksum: oldInfo.Checksum}
			modifiedFiles[path] = modInfo{
				oldChecksum: oldInfo.Checksum,
				newChecksum: newInfo.Checksum,
			}
		}
	}
	for path, info := range oldSums {
		if _, ok := newSums[path]; !ok {
			removedFiles[path] = remInfo{checksum: info.Checksum}
		}
	}

	// If a file was added with the same checksum as a file that was removed, treat it
	// as a moved file
	for addedPath, addInfo := range addedFiles {
		for _, oldPath := range oldSumsReverse[addInfo.checksum] {
			if _, wasRemoved := removedFiles[oldPath]; wasRemoved {
				movedFiles = append(movedFiles, MovedFile{
					From: oldPath,
					To:   addedPath,
				})
				delete(addedFiles, addedPath)
				delete(removedFiles, oldPath)
				delete(modifiedFiles, oldPath)
			}
		}
	}

	// For any modified files that remain after handling moves, make sure we delete
	// the extra add + remove to avoid duplicate reporting.
	for path := range modifiedFiles {
		delete(addedFiles, path)
		delete(removedFiles, path)
	}

	added := make([]AddedFile, 0, len(addedFiles))
	for path, info := range addedFiles {
		added = append(added, AddedFile{Path: path, Checksum: info.checksum})
	}
	slices.SortFunc(added, cmpFunc)

	removed := make([]RemovedFile, 0, len(removedFiles))
	for path, info := range removedFiles {
		removed = append(removed, RemovedFile{Path: path, Checksum: info.checksum})
	}
	slices.SortFunc(removed, cmpFunc)

	modified := make([]ModifiedFile, 0, len(modifiedFiles))
	for path, info := range modifiedFiles {
		modified = append(modified, ModifiedFile{
			Path:        path,
			OldChecksum: info.oldChecksum,
			NewChecksum: info.newChecksum,
		})
	}
	slices.SortFunc(modified, cmpFunc)

	slices.SortFunc(movedFiles, cmpFunc)

	return &DiffResult{
		AddedFiles:    added,
		RemovedFiles:  removed,
		ModifiedFiles: modified,
		MovedFiles:    movedFiles,
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
