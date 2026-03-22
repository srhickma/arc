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
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	sumFile  = "arc.sum"
	confFile = "arc.conf"
)

type RawConfig struct {
	HashType string   `json:"hashType"`
	Workers  int      `json:"workers"`
	Ignore   []string `json:"ignore"`
}

func (c *RawConfig) Process() (*Config, error) {
	var ignorePatterns []*regexp.Regexp
	for _, pattern := range c.Ignore {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid ignore pattern %q: %w", pattern, err)
		}
		ignorePatterns = append(ignorePatterns, re)
	}

	return &Config{
		HashType:       c.HashType,
		Workers:        c.Workers,
		IgnorePatterns: ignorePatterns,
	}, nil
}

type Config struct {
	HashType       string
	Workers        int
	IgnorePatterns []*regexp.Regexp
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

		// Compute path relative to target directory for ignore pattern comparison
		pathInTarget := strings.TrimPrefix(strings.TrimPrefix(path, targetDir), string(filepath.Separator))

		if entry.IsDir() {
			// Add a trailing separator to the directory path so that we can use it to efficently target
			// directories with ignore patterns. Without this, either we have to use a more general pattern
			// which could also match files, or we will unecessarily walk the ignored directory before
			// filtering out its contents.
			pathInTarget += string(filepath.Separator)

			for _, pattern := range config.IgnorePatterns {
				if pattern.MatchString(pathInTarget) {
					return fs.SkipDir
				}
			}
			return nil
		}

		if pathInTarget == sumFile || pathInTarget == confFile {
			return nil
		}

		for _, pattern := range config.IgnorePatterns {
			if pattern.MatchString(pathInTarget) {
				return nil
			}
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

func colored(text, color string) string {
	return color + text + "\033[0m"
}

func coloredCount[T any](slice []T, label, color string) string {
	text := fmt.Sprintf("%d %s", len(slice), label)
	if len(slice) > 0 {
		return colored(text, color)
	}
	return text
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
	var rawConfig RawConfig
	if err := json.Unmarshal(configData, &rawConfig); err != nil {
		log.Fatal(err)
	}
	config, err := rawConfig.Process()
	if err != nil {
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

	newSums, numChecksums, totalBytes, err := computeChecksums(targetDir, config, oldSums, deep)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf(
		"finished checking %d file(s) in %s; %d checksum(s) calculated over %.2fMB\n",
		len(newSums),
		time.Since(startTime).Round(time.Millisecond),
		numChecksums,
		float64(totalBytes)/(1024*1024),
	)

	diff := computeDiff(oldSums, newSums)

	const (
		colorGreen  = "\033[32m"
		colorYellow = "\033[33m"
		colorRed    = "\033[31m"
	)

	for _, added := range diff.AddedFiles {
		fmt.Printf("   %s %s\n", colored("ADDED:", colorGreen), added.Path)
	}
	for _, moved := range diff.MovedFiles {
		fmt.Printf("   %s %s -> %s\n", colored("MOVED:", colorYellow), moved.From, moved.To)
	}
	for _, removed := range diff.RemovedFiles {
		fmt.Printf(" %s %s\n", colored("REMOVED:", colorRed), removed.Path)
	}
	for _, modified := range diff.ModifiedFiles {
		fmt.Printf("%s %s - %s -> %s\n", colored("MODIFIED:", colorRed), modified.Path, modified.OldChecksum, modified.NewChecksum)
	}

	if diff.Empty() {
		fmt.Println("no changes detected")
		return
	}

	fmt.Printf(
		"%s, %s, %s, %s\n",
		coloredCount(diff.AddedFiles, "added", colorGreen),
		coloredCount(diff.MovedFiles, "moved", colorYellow),
		coloredCount(diff.RemovedFiles, "removed", colorRed),
		coloredCount(diff.ModifiedFiles, "modified", colorRed),
	)

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
