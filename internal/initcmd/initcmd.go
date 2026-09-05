package initcmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/srhickma/arc/internal/config"
)

// Run executes "arc init": it writes a starter arc.conf into dir
func Run(dir string) {
	configPath, found := config.FindConfig(dir)
	if found {
		log.Fatalf("directory is already configured by %s", configPath)
	}

	configPath = filepath.Join(dir, config.FileName)
	if err := os.WriteFile(configPath, []byte(config.Template), 0644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s\n", configPath)
}
