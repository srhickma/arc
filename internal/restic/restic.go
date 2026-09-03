package restic

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/srhickma/arc/internal/config"
)

// Run executes the "arc restic[:profile]" subcommand.
//
// dir is where the search for arc.conf begins (walking upward). profile is the
// optional ":name" suffix from the command token. args is everything after the
// token: the restic subcommand followed by any arguments to pass straight
// through to restic.
func Run(dir, profile string, args []string, dryRun bool) {
	sub := ""
	var cliArgs []string
	if len(args) > 0 {
		sub = args[0]
		cliArgs = args[1:]
	}

	if strings.HasPrefix(sub, "-") {
		log.Fatalf("expected a restic subcommand, got %q; arc's global flags go before the command (e.g. arc --dry-run restic backup)", sub)
	}

	start, err := config.ResolveDir(dir)
	if err != nil {
		log.Fatal(err)
	}
	confPath, err := config.Discover(start)
	if err != nil {
		log.Fatal(err)
	}

	cf, err := config.Load(confPath)
	if err != nil {
		log.Fatal(err)
	}

	inv, err := cf.ResolveRestic(profile, sub, cliArgs)
	if err != nil {
		log.Fatal(err)
	}

	if dryRun {
		fmt.Printf("cwd:  %s\n", cf.Root)
		for _, e := range inv.Env {
			fmt.Printf("env:  %s\n", e)
		}
		fmt.Printf("exec: restic %s\n", strings.Join(inv.Argv, " "))
		return
	}

	cmd := exec.Command("restic", inv.Argv...)
	cmd.Dir = cf.Root
	cmd.Env = append(os.Environ(), inv.Env...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		log.Fatalf("running restic: %v", err)
	}
}
