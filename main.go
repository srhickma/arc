package main

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	sumFile  = "arc.sum"
	confFile = "arc.conf"
)

type Config struct {
	HashType string `json:"hashType"`
	Workers  int    `json:"workers"`
}

type hasher struct {
	hashFactory func() hash.Hash
}

func newHasher(hashType string) (*hasher, error) {
	var hashFactory func() hash.Hash
	switch hashType {
	case "sha256":
		hashFactory = sha256.New
	case "sha1":
		hashFactory = sha1.New
	case "md5":
		hashFactory = md5.New
	default:
		return nil, fmt.Errorf("unsupported hash type %s", hashType)
	}

	return &hasher{
		hashFactory: hashFactory,
	}, nil
}

func (h *hasher) computeChecksum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("error opening %s: %w", filePath, err)
	}
	defer file.Close()

	hash := h.hashFactory()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("error reading %s: %w", filePath, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type fileInfo struct {
	Checksum string    `json:"checksum"`
	ModTime  time.Time `json:"modTime"`
}

func resolvePath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}

	return filepath.Abs(path)
}

func computeChecksums(targetDir string, config *Config, oldSums map[string]fileInfo, deep bool) (map[string]fileInfo, int, int64, error) {
	hasher, err := newHasher(config.HashType)
	if err != nil {
		return nil, 0, 0, err
	}

	var lock sync.Mutex
	sums := make(map[string]fileInfo)
	var numChecksums int
	var totalBytes int64

	type fileHandle struct {
		path string
		name string
		info fs.FileInfo
	}
	fhChan := make(chan fileHandle, config.Workers)

	wg := sync.WaitGroup{}

	for range config.Workers {
		wg.Go(func() {
			for {
				fh, ok := <-fhChan
				if !ok {
					break
				}

				checksum, err := hasher.computeChecksum(fh.path)
				if err != nil {
					log.Fatal(err) // TODO propagate the error instead
				}

				lock.Lock()
				sums[fh.path] = fileInfo{
					Checksum: checksum,
					ModTime:  fh.info.ModTime(),
				}
				numChecksums++
				totalBytes += fh.info.Size()
				lock.Unlock()
			}
		})
	}

	err = filepath.WalkDir(targetDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() || entry.Name() == sumFile || entry.Name() == confFile {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		if !deep {
			if old, ok := oldSums[path]; ok && !old.ModTime.IsZero() && old.ModTime.Equal(info.ModTime()) {
				lock.Lock()
				sums[path] = old
				lock.Unlock()
				return nil
			}
		}

		fhChan <- fileHandle{
			path: path,
			name: entry.Name(),
			info: info,
		}

		return nil
	})

	close(fhChan)
	wg.Wait()

	return sums, numChecksums, totalBytes, nil
}

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

func main() {
	args := os.Args[1:]

	deep := false
	var posArgs []string
	for _, arg := range args {
		if arg == "--deep" {
			deep = true
		} else {
			posArgs = append(posArgs, arg)
		}
	}

	if len(posArgs) < 1 {
		log.Fatal("usage: arc <dir> [--deep]")
	}

	targetDir, err := resolvePath(posArgs[0])
	if err != nil {
		log.Fatal(err)
	}

	configPath := filepath.Join(targetDir, confFile)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Fatalf("config file not found: %s", configPath)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatal(err)
	}
	var config Config
	if err := json.Unmarshal(configData, &config); err != nil {
		log.Fatal(err)
	}

	oldSums := make(map[string]fileInfo)
	sumPath := filepath.Join(targetDir, sumFile)
	if bytes, err := os.ReadFile(sumPath); err == nil {
		if err := json.Unmarshal(bytes, &oldSums); err != nil {
			log.Fatal(err)
		}
	}

	if deep {
		fmt.Println("starting deep check (all files) ...")
	} else {
		fmt.Println("starting shallow check (modified files) ...")
	}

	startTime := time.Now()

	newSums, numChecksums, totalBytes, err := computeChecksums(targetDir, &config, oldSums, deep)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf(
		"finished comparison of %d file(s) in %s; %d checksum(s) calculated over %.2fMB\n",
		len(newSums),
		time.Since(startTime).Round(time.Millisecond),
		numChecksums,
		float64(totalBytes)/(1024*1024),
	)

	diff := computeDiff(oldSums, newSums)

	for path := range diff.addedFiles {
		fmt.Printf("   ADDED: %s\n", path)
	}
	for _, info := range diff.movedFiles {
		fmt.Printf("   MOVED: %s -> %s\n", info.from, info.to)
	}
	for path := range diff.removedFiles {
		fmt.Printf(" REMOVED: %s\n", path)
	}
	for path, info := range diff.modifiedFiles {
		fmt.Printf("MODIFIED: %s - %s -> %s\n", path, info.oldChecksum, info.newChecksum)
	}

	if diff.empty() {
		fmt.Println("no changes detected")
		return
	}

	fmt.Printf("%d added, %d moved, %d removed, %d modified\n",
		len(diff.addedFiles), len(diff.movedFiles), len(diff.removedFiles), len(diff.modifiedFiles))

	fmt.Printf("write new checksums to %s? [y/N]: ", sumFile)
	var response string
	fmt.Scanln(&response)
	if response != "y" && response != "Y" {
		fmt.Println("skipping write")
		return
	}

	data, err := json.MarshalIndent(newSums, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(sumPath, data, 0644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %d checksums to %s\n", len(newSums), sumFile)
}
