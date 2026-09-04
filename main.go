package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/srhickma/arc/internal/check"
	"github.com/srhickma/arc/internal/config"
	"github.com/srhickma/arc/internal/initcmd"
	"github.com/srhickma/arc/internal/restic"
)

func init() {
	log.SetFlags(0)
	log.SetPrefix("fatal: ")
}

const usage = `arc - client-side integrity protection and a restic wrapper

usage:
  arc [dir] [--dry-run] check [--deep]
  arc [dir] [--dry-run] restic[:profile] <subcommand> [restic args...]
  arc [dir] init

[dir] is an optional leading path (default "."). "check" reads arc.conf and
arc.sum from it directly; the other commands search upward from it for arc.conf.

global flags:
  --dry-run    show what would happen without writing arc.sum / running restic
`

// isCommand reports whether token names a subcommand rather than a directory
func isCommand(token string) bool {
	name, _, _ := strings.Cut(token, ":")
	switch name {
	case "check", "restic", "init":
		return true
	}
	return false
}

func main() {
	args := os.Args[1:]

	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Print(usage)
		return
	}

	dir := "."
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") && !isCommand(args[0]) {
		dir = args[0]
		args = args[1:]
	}

	dir, err := filepath.Abs(config.ExpandTilde(dir))
	if err != nil {
		log.Fatal(err)
	}

	var dryRun bool
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--dry-run":
			dryRun = true
			i++
			continue
		case "-h", "--help", "help":
			fmt.Print(usage)
			return
		}
		break
	}
	rest := args[i:]

	if len(rest) == 0 {
		fmt.Print(usage)
		os.Exit(2)
	}

	name, profile, hasProfile := strings.Cut(rest[0], ":")
	cmdArgs := rest[1:]

	if hasProfile && name != "restic" {
		log.Fatalf("%q does not take a :profile suffix", name)
	}

	switch name {
	case "check":
		check.Run(dir, cmdArgs, dryRun)
	case "restic":
		restic.Run(dir, profile, cmdArgs, dryRun)
	case "init":
		initcmd.Run(dir)
	default:
		log.Fatalf("unknown command %q (try: arc help)", name)
	}
}
