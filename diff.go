package main

type addInfo struct{ checksum string }
type remInfo struct{ checksum string }
type modInfo struct{ oldChecksum, newChecksum string }
type movInfo struct{ from, to string }

type diffResult struct {
	addedFiles    map[string]addInfo
	removedFiles  map[string]remInfo
	modifiedFiles map[string]modInfo
	movedFiles    []movInfo
}

func (d *diffResult) empty() bool {
	return len(d.addedFiles)+len(d.removedFiles)+len(d.modifiedFiles)+len(d.movedFiles) == 0
}

func computeDiff(oldSums, newSums map[string]fileInfo) *diffResult {
	// Mapping of old checksums to file paths
	oldSumsReverse := make(map[string][]string, len(oldSums))
	for path, info := range oldSums {
		oldSumsReverse[info.Checksum] = append(oldSumsReverse[info.Checksum], path)
	}

	addedFiles := make(map[string]addInfo)
	removedFiles := make(map[string]remInfo)
	modifiedFiles := make(map[string]modInfo)
	var movedFiles []movInfo

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
				movedFiles = append(movedFiles, movInfo{
					from: oldPath,
					to:   addedPath,
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

	return &diffResult{
		addedFiles:    addedFiles,
		removedFiles:  removedFiles,
		modifiedFiles: modifiedFiles,
		movedFiles:    movedFiles,
	}
}
