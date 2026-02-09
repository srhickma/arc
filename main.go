package main

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	hashType string
	folder   string
	workers  int
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

func main() {
	args := os.Args[1:]
	for _, arg := range args {
		fmt.Println(arg)
	}

	config := &Config{
		hashType: "sha256",
		folder:   "/mnt/vat/photos",
		workers:  8,
	}

	hasher, err := newHasher(config.hashType)
	if err != nil {
		log.Fatal(err)
	}

	wg := sync.WaitGroup{}
	fhChan := make(chan fileHandle, config.workers)

	for range config.workers {
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
				fmt.Printf("%s - %s\n", checksum, fh.path)
			}
		})
	}

	err = filepath.WalkDir(config.folder, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}

			fhChan <- fileHandle{
				path: path,
				name: entry.Name(),
				info: info,
			}
		}

		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	close(fhChan)
	wg.Wait()
}
