package initcmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/srhickma/arc/internal/config"
)

// Run executes "arc init": it writes a starter arc.conf into dir.
func Run(dir string) {
	d, err := config.ResolveDir(dir)
	if err != nil {
		log.Fatal(err)
	}

	path := filepath.Join(d, config.FileName)
	if _, err := os.Stat(path); err == nil {
		log.Fatalf("%s already exists", path)
	}
	if err := os.WriteFile(path, []byte(config.Template), 0644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s\n", path)
}
