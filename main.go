package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/srhickma/arc/internal/check"
	"github.com/srhickma/arc/internal/initcmd"
	"github.com/srhickma/arc/internal/restic"
	"github.com/srhickma/arc/internal/util"
)

func init() {
	log.SetFlags(0)
	log.SetPrefix("fatal: ")
}

const usage = `arc - restic wrapper with client-side integrity protection

usage:
  arc [flags] restic[:profile] [restic args...]
  arc [flags] check [--deep]
  arc [flags] init

global flags:
  --dir <path>   directory to operate in (default ".")
  --dry-run      read-only mode; print commands to run but don't run them
`

func main() {
	args := os.Args[1:]

	dir := "."
	dryRun := false

loop:
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dir":
			i++
			if i >= len(args) {
				log.Fatal("--dir requires a path")
			}
			dir = args[i]
		case "--dry-run":
			dryRun = true
		case "-h", "--help", "help":
			fmt.Print(usage)
			return
		default:
			args = args[i:]
			break loop
		}
	}

	dir, err := filepath.Abs(util.ExpandTilde(dir))
	if err != nil {
		log.Fatal(err)
	}

	if len(args) == 0 {
		fmt.Print(usage)
		os.Exit(2)
	}

	subcmd, profile, hasProfile := strings.Cut(args[0], ":")
	args = args[1:]

	if hasProfile && subcmd != "restic" {
		log.Fatalf("%q does not take a :profile suffix", subcmd)
	}

	switch subcmd {
	case "check":
		check.Run(dir, args, dryRun)
	case "restic":
		restic.Run(dir, profile, args, dryRun)
	case "init":
		initcmd.Run(dir)
	default:
		log.Fatalf("unknown command %q", subcmd)
	}
}
