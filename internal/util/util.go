package util

import (
	"os"
	"path/filepath"
	"strings"
)

// ExpandTilde resolves a leading ~ to the user's home directory
func ExpandTilde(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
