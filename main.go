package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
)

func main() {
	args := os.Args[1:]
	for _, arg := range args {
		fmt.Println(arg)
	}

	goroutines := 8

	wg := sync.WaitGroup{}
	pathChan := make(chan string, goroutines)

	for range goroutines {
		wg.Go(func() {
			for {
				path, ok := <-pathChan
				if !ok {
					break
				}

				file, err := os.Open(path)
				if err != nil {
					log.Fatal(err)
				}
				defer file.Close()

				hash := sha256.New()
				if _, err := io.Copy(hash, file); err != nil {
					log.Fatal(err)
				}
				fmt.Printf("%s - %s\n", hex.EncodeToString(hash.Sum(nil)), path)
			}
		})
	}

	err := filepath.WalkDir("/Users/shane/vat/photos", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			pathChan <- path
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	close(pathChan)
	wg.Wait()
}
