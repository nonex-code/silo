// Command silo is an interactive, shell-like client for S3 compatible object
// storage. It reads the same configuration file as MinIO's mc client.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/mattn/go-isatty"
	"github.com/nonex-code/silo/internal/shell"
	"github.com/nonex-code/silo/internal/ui"
)

func main() {
	if err := run(); err != nil {
		var ee *shell.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.Code)
		}
		fmt.Fprintln(os.Stderr, ui.Error.Sprint("error: ")+err.Error())
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("silo", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		configPath = fs.String("config", "", "path to the mc config.json (default: ~/.mc/config.json)")
		alias      = fs.String("alias", "", "start inside this alias")
		command    = fs.String("c", "", "run a single command and exit")
		noColor    = fs.Bool("no-color", false, "disable coloured output")
		jsonOut    = fs.Bool("json", false, "emit one JSON object per command (machine readable)")
		parallel   = fs.Int("parallel", 0, "default transfer concurrency")
		partSize   = fs.String("part-size", "", "multipart part size (e.g. 16M, 64M)")
		history    = fs.String("history", "", "path to the history file (default: ~/.silo_history)")
		showVer    = fs.Bool("version", false, "print version and exit")
	)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "silo %s - interactive S3 client\n\n", shell.Version)
		fmt.Fprintf(os.Stderr, "usage: silo [flags] [command...]\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVer {
		fmt.Printf("silo %s\n", shell.Version)
		return nil
	}
	if *noColor {
		ui.Disable()
	}
	if *jsonOut {
		// JSON is consumed by machines: no colour codes, no progress bars.
		ui.Disable()
		ui.DisableProgress()
	}

	// --json only makes sense for one-shot or piped execution.
	interactiveInput := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	if *jsonOut && *command == "" && len(fs.Args()) == 0 && interactiveInput {
		return fmt.Errorf("--json requires -c or piped input; it cannot drive the interactive REPL")
	}

	ps, err := parseSize(*partSize)
	if err != nil {
		return err
	}

	sh, err := shell.New(shell.Options{
		ConfigPath:  *configPath,
		StartAlias:  *alias,
		PartSize:    ps,
		Concurrency: *parallel,
		HistoryFile: *history,
		JSON:        *jsonOut,
	})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A command may be supplied with -c or as trailing arguments.
	line := *command
	if line == "" {
		if rest := fs.Args(); len(rest) > 0 {
			line = strings.Join(rest, " ")
		}
	}
	if line != "" {
		err := sh.Eval(ctx, line)
		if err != nil && !errors.Is(err, shell.ErrExit) {
			if sh.JSONMode() {
				// The error is already inside the emitted JSON object.
				return exitFromShell(sh)
			}
			return err
		}
		return exitFromShell(sh)
	}

	return sh.Run(ctx)
}

// exitFromShell converts any accumulated non-zero command status into an
// ExitError so the process exits with the same code.
func exitFromShell(sh *shell.Shell) error {
	if code := sh.ExitCode(); code != 0 {
		return &shell.ExitError{Code: code}
	}
	return nil
}

// parseSize parses sizes such as "16M", "512K" or a plain byte count.
func parseSize(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	mult := uint64(1)
	switch {
	case strings.HasSuffix(s, "K"), strings.HasSuffix(s, "k"):
		mult, s = 1024, s[:len(s)-1]
	case strings.HasSuffix(s, "M"), strings.HasSuffix(s, "m"):
		mult, s = 1024*1024, s[:len(s)-1]
	case strings.HasSuffix(s, "G"), strings.HasSuffix(s, "g"):
		mult, s = 1024*1024*1024, s[:len(s)-1]
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid part size %q", s)
	}
	return n * mult, nil
}
