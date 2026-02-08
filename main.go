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
)

func main() {
	args := os.Args[1:]
	for _, arg := range args {
		fmt.Println(arg)
	}

	err := filepath.WalkDir("/mnt/vat/photos", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			hash := sha256.New()
			if _, err := io.Copy(hash, file); err != nil {
				return err
			}

			fmt.Printf("%s - %s\n", path, hex.EncodeToString(hash.Sum(nil)))
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
}
