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

// Run executes "arc restic[:profile]"
func Run(dir, profile string, args []string, dryRun bool) {
	subcmd := ""
	cliArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		subcmd = args[0]
		cliArgs = args[1:]
	}

	conf, err := config.Load(dir)
	if err != nil {
		log.Fatal(err)
	}

	invocation, err := conf.ResolveRestic(profile, subcmd, cliArgs)
	if err != nil {
		log.Fatal(err)
	}

	if dryRun {
		fmt.Printf("cwd:  %s\n", conf.Dir)
		for _, envVar := range invocation.Env {
			fmt.Printf("env:  %s\n", envVar)
		}
		fmt.Printf("exec: restic %s\n", strings.Join(invocation.Argv, " "))
		return
	}

	cmd := exec.Command("restic", invocation.Argv...)
	cmd.Dir = conf.Dir
	cmd.Env = append(os.Environ(), invocation.Env...)
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
