package shell

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/nonex-code/silo/internal/ui"
)

func init() {
	register(&command{
		name:    "help",
		usage:   "help [command]",
		summary: "show available commands or help for one command",
		group:   "misc",
		run:     helpCommand,
	})

	register(&command{
		name:    "history",
		usage:   "history [-c]",
		summary: "show (or clear with -c) the command history of this session",
		group:   "misc",
		run: func(ctx context.Context, sh *Shell, args []string) error {
			pa, err := parseArgs(args, []flagDef{{Names: []string{"c", "clear"}}})
			if err != nil {
				return err
			}
			if pa.Has("clear") {
				sh.history = nil
				if sh.rl != nil {
					sh.rl.ResetHistory()
				}
				if sh.historyFile != "" {
					if err := os.Truncate(sh.historyFile, 0); err != nil && !os.IsNotExist(err) {
						sh.printError(fmt.Errorf("clear history: %w", err))
					}
				}
				fmt.Fprintln(sh.stdout, ui.OK.Sprint("history cleared"))
				return nil
			}
			for i, h := range sh.history {
				fmt.Fprintf(sh.stdout, "%5d  %s\n", i+1, h)
			}
			return nil
		},
	})

	register(&command{
		name:    "clear",
		usage:   "clear",
		summary: "clear the screen",
		group:   "misc",
		run: func(ctx context.Context, sh *Shell, args []string) error {
			if sh.rl != nil {
				sh.rl.ClearScreen()
			}
			// Never leak ANSI escapes into piped or captured output.
			return nil
		},
	})

	register(&command{
		name:    "exit",
		usage:   "exit",
		summary: "leave silo",
		group:   "misc",
		run: func(ctx context.Context, sh *Shell, args []string) error {
			return ErrExit
		},
	})
	registerAlias("quit", "exit")
	registerAlias("q", "exit")

	register(&command{
		name:    "version",
		usage:   "version",
		summary: "print the silo version",
		group:   "misc",
		run: func(ctx context.Context, sh *Shell, args []string) error {
			fmt.Fprintf(sh.stdout, "silo %s\n", Version)
			return nil
		},
	})

	register(&command{
		name:    "info",
		usage:   "info",
		summary: "show the session configuration (config path, cwd, transfer settings)",
		group:   "misc",
		run: func(ctx context.Context, sh *Shell, args []string) error {
			fmt.Fprintf(sh.stdout, "%-14s %s\n", ui.Bold.Sprint("version:"), Version)
			fmt.Fprintf(sh.stdout, "%-14s %s\n", ui.Bold.Sprint("config file:"), sh.cfg.Path())
			fmt.Fprintf(sh.stdout, "%-14s %d\n", ui.Bold.Sprint("aliases:"), len(sh.cfg.Names()))
			fmt.Fprintf(sh.stdout, "%-14s %s\n", ui.Bold.Sprint("cwd:"), sh.cwd.String())
			fmt.Fprintf(sh.stdout, "%-14s %s\n", ui.Bold.Sprint("part size:"), ui.HumanSize(int64(sh.partSize)))
			fmt.Fprintf(sh.stdout, "%-14s %d\n", ui.Bold.Sprint("concurrency:"), sh.concurrency)
			return nil
		},
	})

	register(&command{
		name:    "echo",
		usage:   "echo <text...>",
		summary: "print the arguments (useful in scripts)",
		group:   "misc",
		run: func(ctx context.Context, sh *Shell, args []string) error {
			fmt.Fprintln(sh.stdout, strings.Join(args, " "))
			return nil
		},
	})
}
