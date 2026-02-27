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
	"sync"
	"time"
)

const (
	sumFile  = "arc.sum"
	confFile = "arc.conf"
)

type Config struct {
	HashType string `json:"hashType"`
	Folder   string `json:"folder"`
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

type fileHandle struct {
	path string
	name string
	info fs.FileInfo
}

type fileInfo struct {
	Checksum string    `json:"checksum"`
	ModTime  time.Time `json:"modTime"`
}

func main() {
	args := os.Args[1:]
	for _, arg := range args {
		fmt.Println(arg)
	}

	configData, err := os.ReadFile(confFile)
	if err != nil {
		log.Fatal(err)
	}
	var config Config
	if err := json.Unmarshal(configData, &config); err != nil {
		log.Fatal(err)
	}

	oldSums := make(map[string]fileInfo)
	sumPath := filepath.Join(config.Folder, sumFile)
	if bytes, err := os.ReadFile(sumPath); err == nil {
		if err := json.Unmarshal(bytes, &oldSums); err != nil {
			log.Fatal(err)
		}
	}

	hasher, err := newHasher(config.HashType)
	if err != nil {
		log.Fatal(err)
	}

	var lock sync.Mutex
	newSums := make(map[string]fileInfo)
	var totalBytes int64

	wg := sync.WaitGroup{}
	fhChan := make(chan fileHandle, config.Workers)

	startTime := time.Now()

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
				newSums[fh.path] = fileInfo{
					Checksum: checksum,
					ModTime:  fh.info.ModTime(),
				}
				totalBytes += fh.info.Size()
				lock.Unlock()
			}
		})
	}

	err = filepath.WalkDir(config.Folder, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() || entry.Name() == sumFile {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		fhChan <- fileHandle{
			path: path,
			name: entry.Name(),
			info: info,
		}

		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	close(fhChan)
	wg.Wait()

	fmt.Printf(
		"computed %d checksums over %.2fMB in %s\n",
		len(newSums),
		float64(totalBytes)/(1024*1024),
		time.Since(startTime).Round(time.Millisecond),
	)

	// Mapping of old checksums to file paths
	oldSumsReverse := make(map[string][]string, len(oldSums))
	for path, info := range oldSums {
		oldSumsReverse[info.Checksum] = append(oldSumsReverse[info.Checksum], path)
	}

	type move struct{ from, to string }

	addedFiles := make(map[string]string)
	removedFiles := make(map[string]string)
	modifiedFiles := make(map[string]struct{})

	for path, info := range newSums {
		if prev, ok := oldSums[path]; !ok {
			addedFiles[path] = info.Checksum
		} else if prev.Checksum != info.Checksum {
			// Treat modified files like an add + remove for better tracking of moved files.
			// For example, if two files swap paths we want to treat that as two moves rather
			// than two modifications.
			addedFiles[path] = info.Checksum
			removedFiles[path] = prev.Checksum
			modifiedFiles[path] = struct{}{}
		}
	}
	for path, info := range oldSums {
		if _, ok := newSums[path]; !ok {
			removedFiles[path] = info.Checksum
		}
	}

	// If a file was added with the same checksum as a file that was removed, treat it
	// as a moved file
	var movedFiles []move
	for addedPath, checksum := range addedFiles {
		for _, oldPath := range oldSumsReverse[checksum] {
			if _, wasRemoved := removedFiles[oldPath]; wasRemoved {
				movedFiles = append(movedFiles, move{oldPath, addedPath})
				delete(addedFiles, addedPath)
				delete(removedFiles, oldPath)
				delete(modifiedFiles, oldPath)
				break
			}
		}
	}

	// For any modified files that remain after handling moves, make sure we delete
	// the extra add + remove to avoid duplicate reporting.
	for path := range modifiedFiles {
		delete(addedFiles, path)
		delete(removedFiles, path)
	}

	for path := range addedFiles {
		fmt.Printf("ADDED: %s\n", path)
	}
	for _, m := range movedFiles {
		fmt.Printf("MOVED: %s -> %s\n", m.from, m.to)
	}
	for path := range removedFiles {
		fmt.Printf("REMOVED: %s\n", path)
	}
	for path := range modifiedFiles {
		fmt.Printf("MODIFIED: %s\n", path)
	}

	if len(addedFiles)+len(movedFiles)+len(removedFiles)+len(modifiedFiles) == 0 {
		fmt.Println("no changes detected")
		return
	}

	fmt.Printf("%d added, %d moved, %d removed, %d modified, \n",
		len(addedFiles), len(movedFiles), len(removedFiles), len(modifiedFiles))
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
