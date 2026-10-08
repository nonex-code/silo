// Package shell implements the interactive REPL and the virtual filesystem
// exposed to the user.
package shell

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ergochat/readline"
	"github.com/mattn/go-isatty"
	"github.com/nonex-code/silo/internal/config"
	"github.com/nonex-code/silo/internal/s3fs"
	"github.com/nonex-code/silo/internal/ui"
)

// Version is the silo version string. It is overridden at build time via
// -ldflags "-X github.com/nonex-code/silo/internal/shell.Version=<tag>".
var Version = "0.1.0"

// Options configures a Shell.
type Options struct {
	ConfigPath  string
	StartAlias  string
	PartSize    uint64
	Concurrency int
	HistoryFile string
	// JSON makes Eval emit one machine-readable JSON object per command
	// instead of human text. It changes presentation only; dispatch and
	// command behaviour are shared with the interactive path.
	JSON bool
}

// outputFormat selects how Eval presents command output.
type outputFormat int

const (
	formatText outputFormat = iota
	formatJSON
)

// Shell is the interactive session state.
type Shell struct {
	cfg         *config.Config
	clients     map[string]*s3fs.Client
	cwd         Path
	prevDir     Path
	rl          *readline.Instance
	history     []string
	historyFile string
	stdout      io.Writer
	stderr      io.Writer
	partSize    uint64
	concurrency int
	interactive bool
	format      outputFormat
	exitCode    int
}

// New creates a shell, loading the mc configuration.
func New(opts Options) (*Shell, error) {
	cfgPath := opts.ConfigPath
	if cfgPath == "" {
		p, err := config.DefaultPath()
		if err != nil {
			return nil, err
		}
		cfgPath = p
	}
	cfg, err := config.LoadFrom(cfgPath)
	if err != nil {
		return nil, err
	}

	partSize := opts.PartSize
	if partSize == 0 {
		partSize = s3fs.DefaultPartSize
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = s3fs.DefaultConcurrency
	}

	s := &Shell{
		cfg:         cfg,
		clients:     map[string]*s3fs.Client{},
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		partSize:    partSize,
		concurrency: concurrency,
		historyFile: opts.HistoryFile,
	}
	if opts.JSON {
		s.format = formatJSON
	}

	if opts.StartAlias != "" {
		if _, ok := cfg.Get(opts.StartAlias); !ok {
			return nil, fmt.Errorf("alias %q not found in %s", opts.StartAlias, cfg.Path())
		}
		s.cwd = Path{Alias: opts.StartAlias}
	}
	return s, nil
}

// Config returns the loaded mc configuration.
func (s *Shell) Config() *config.Config { return s.cfg }

// Cwd returns the current virtual directory.
func (s *Shell) Cwd() Path { return s.cwd }

// client returns (creating if needed) a client for the alias.
func (s *Shell) client(alias string) (*s3fs.Client, error) {
	if c, ok := s.clients[alias]; ok {
		return c, nil
	}
	a, ok := s.cfg.Get(alias)
	if !ok {
		return nil, fmt.Errorf("alias %q not found", alias)
	}
	c, err := s3fs.New(alias, a)
	if err != nil {
		return nil, err
	}
	s.clients[alias] = c
	return c, nil
}

// clientFor returns the client for a path, guaranteeing an alias is set.
func (s *Shell) clientFor(p Path) (*s3fs.Client, error) {
	if p.Alias == "" {
		return nil, errors.New("no alias selected")
	}
	return s.client(p.Alias)
}

// updatePrompt refreshes the readline prompt from the current directory.
func (s *Shell) updatePrompt() {
	if s.rl == nil {
		return
	}
	s.rl.SetPrompt(s.prompt())
}

func (s *Shell) prompt() string {
	return ui.Prompt.Sprint(s.cwd.String() + " > ")
}

// Run starts the read-eval-print loop. When stdin is not a terminal it falls
// back to a plain line reader so that scripts can pipe commands in.
func (s *Shell) Run(ctx context.Context) error {
	if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		return s.runScript(ctx)
	}
	s.interactive = true

	histFile := s.historyFile
	if histFile == "" {
		histFile = os.Getenv("SILO_HISTORY")
	}
	if histFile == "" {
		// Prefer ~/.silo_history next to the user.
		if home, err := os.UserHomeDir(); err == nil {
			histFile = filepath.Join(home, ".silo_history")
		} else {
			histFile = s.cfg.Path() + ".silo_history"
		}
	}
	s.historyFile = histFile

	rl, err := readline.NewFromConfig(&readline.Config{
		Prompt:          s.prompt(),
		HistoryFile:     histFile,
		HistoryLimit:    2000,
		AutoComplete:    &completer{sh: s},
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		return err
	}
	s.rl = rl
	defer rl.Close()

	s.printBanner()

	for {
		line, err := rl.Readline()
		if err != nil {
			if errors.Is(err, readline.ErrInterrupt) {
				continue
			}
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(s.stdout)
				return nil
			}
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		s.history = append(s.history, line)
		if err := s.Eval(ctx, line); err != nil {
			if errors.Is(err, ErrExit) {
				return nil
			}
			if !s.JSONMode() {
				fmt.Fprintln(s.stderr, ui.Error.Sprint("error: ")+err.Error())
			}
		}
		s.updatePrompt()
	}
}

// runScript reads commands from stdin without readline. It returns an
// ExitError when any line failed so a batch run reports a non-zero status.
func (s *Shell) runScript(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	code := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		s.history = append(s.history, line)
		if err := s.Eval(ctx, line); err != nil {
			if errors.Is(err, ErrExit) {
				return exitCodeError(code)
			}
			if !s.JSONMode() {
				fmt.Fprintln(s.stderr, ui.Error.Sprint("error: ")+err.Error())
			}
			code = 1
		}
		if c := s.ExitCode(); c != 0 {
			code = c
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return exitCodeError(code)
}

// exitCodeError converts a non-zero status into an ExitError.
func exitCodeError(code int) error {
	if code == 0 {
		return nil
	}
	return &ExitError{Code: code}
}

func (s *Shell) printBanner() {
	fmt.Fprintf(s.stdout, "%s %s  %s\n",
		ui.Bold.Sprint("silo"), ui.Info.Sprint(Version),
		ui.Info.Sprint("(type 'help' for commands, 'exit' to quit)"))
	if n := len(s.cfg.Aliases); n == 0 {
		fmt.Fprintln(s.stdout, ui.Warn.Sprint("no aliases configured; use 'alias set <name> <url> <access> <secret>'"))
	} else {
		fmt.Fprintf(s.stdout, "%s %s\n", ui.Info.Sprint("config:"), s.cfg.Path())
	}
}

// Eval parses and runs a single command line. It is the single execution path
// shared by the interactive REPL, the -c flag and piped scripts. In JSON mode
// the command runs against captured streams and the result is printed as one
// JSON object; command dispatch and behaviour are otherwise identical.
func (s *Shell) Eval(ctx context.Context, line string) error {
	if s.format == formatJSON {
		return s.evalJSON(ctx, line)
	}
	return s.eval(ctx, line)
}

// JSONMode reports whether Eval emits machine-readable JSON.
func (s *Shell) JSONMode() bool { return s.format == formatJSON }

// ExitError carries a process exit status without an extra message.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// ExitCode returns the status accumulated by the last evaluated command.
func (s *Shell) ExitCode() int { return s.exitCode }

// eval is the shared command dispatcher: tokenize, look up, run.
func (s *Shell) eval(ctx context.Context, line string) error {
	s.exitCode = 0
	toks, err := tokenize(line)
	if err != nil {
		return err
	}
	if len(toks) == 0 {
		return nil
	}
	cmd, ok := lookupCommand(toks[0])
	if !ok {
		return fmt.Errorf("unknown command %q (try 'help')", toks[0])
	}
	return cmd.run(ctx, s, toks[1:])
}

// evalJSON runs one command with its stdout/stderr captured and prints a single
// JSON object describing the result. This keeps JSON as a presentation layer
// instead of a second set of command implementations.
func (s *Shell) evalJSON(ctx context.Context, line string) error {
	var out, errOut bytes.Buffer
	origOut, origErr := s.stdout, s.stderr
	s.stdout, s.stderr = &out, &errOut
	err := s.eval(ctx, line)
	s.stdout, s.stderr = origOut, origErr

	exited := errors.Is(err, ErrExit)
	code := s.exitCode
	if err != nil && !exited && code == 0 {
		code = 1
	}
	s.exitCode = code
	env := struct {
		Command  string `json:"command"`
		OK       bool   `json:"ok"`
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout,omitempty"`
		Stderr   string `json:"stderr,omitempty"`
		Error    string `json:"error,omitempty"`
	}{
		Command:  line,
		OK:       (err == nil || exited) && code == 0,
		ExitCode: code,
		Stdout:   out.String(),
		Stderr:   errOut.String(),
	}
	if err != nil && !exited {
		env.Error = err.Error()
	}
	enc, merr := json.Marshal(env)
	if merr != nil {
		return err
	}
	fmt.Fprintln(origOut, string(enc))
	return err
}

// ErrExit is a sentinel used to leave the REPL.
var ErrExit = errors.New("exit")

// fsEntry is a filesystem entry plus its virtual path.
type fsEntry struct {
	s3fs.Entry
	Path    Path
	IsAlias bool
}

// listDir returns the children of a virtual directory.
func (s *Shell) listDir(ctx context.Context, p Path, recursive bool) ([]fsEntry, error) {
	switch {
	case p.IsRoot():
		names := s.cfg.Names()
		out := make([]fsEntry, 0, len(names))
		for _, n := range names {
			out = append(out, fsEntry{
				Entry:   s3fs.Entry{Name: n, IsDir: true},
				Path:    Path{Alias: n},
				IsAlias: true,
			})
		}
		return out, nil
	case p.IsAlias():
		c, err := s.client(p.Alias)
		if err != nil {
			return nil, err
		}
		buckets, err := c.ListBuckets(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]fsEntry, 0, len(buckets))
		for _, b := range buckets {
			out = append(out, fsEntry{
				Entry: b,
				Path:  Path{Alias: p.Alias, Bucket: b.Name},
			})
		}
		return out, nil
	default:
		c, err := s.client(p.Alias)
		if err != nil {
			return nil, err
		}
		objs, err := c.List(ctx, p.Bucket, p.Key, recursive)
		if err != nil {
			return nil, err
		}
		out := make([]fsEntry, 0, len(objs))
		for _, o := range objs {
			ep := p
			if o.IsDir {
				// strip trailing slash for the virtual path
				ep.Key = strings.TrimSuffix(o.Key, "/")
			} else {
				ep.Key = o.Key
			}
			if recursive {
				ep.Key = joinKey(p.Key, o.Name)
			}
			out = append(out, fsEntry{Entry: o, Path: ep})
		}
		return out, nil
	}
}

// stat resolves a virtual path that may be an object or a directory.
func (s *Shell) statPath(ctx context.Context, p Path) (fsEntry, error) {
	switch {
	case p.IsRoot():
		return fsEntry{Entry: s3fs.Entry{Name: "/", IsDir: true}, Path: p}, nil
	case p.IsAlias():
		return fsEntry{Entry: s3fs.Entry{Name: p.Alias, IsDir: true}, Path: p, IsAlias: true}, nil
	case p.IsBucket():
		return fsEntry{Entry: s3fs.Entry{Name: p.Bucket, IsBucket: true, IsDir: true}, Path: p}, nil
	default:
		c, err := s.client(p.Alias)
		if err != nil {
			return fsEntry{}, err
		}
		e, err := c.Stat(ctx, p.Bucket, p.Key)
		if err == nil {
			return fsEntry{Entry: e, Path: p}, nil
		}
		if !s3fs.IsNotFound(err) {
			return fsEntry{}, err
		}
		// Maybe it's a prefix/directory.
		children, lerr := c.List(ctx, p.Bucket, p.Key, false)
		if lerr != nil {
			return fsEntry{}, lerr
		}
		if len(children) == 0 {
			return fsEntry{}, fmt.Errorf("%s: no such object", p)
		}
		return fsEntry{Entry: s3fs.Entry{Name: p.Base(), IsDir: true}, Path: p}, nil
	}
}

// expandS3 turns arguments (possibly containing globs) into concrete paths.
// When recursive is true, directories expand to all objects beneath them.
func (s *Shell) expandS3(ctx context.Context, args []string, recursive bool) ([]Path, error) {
	var out []Path
	for _, arg := range args {
		if hasGlob(arg) {
			matches, err := s.globS3(ctx, arg)
			if err != nil {
				return nil, err
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("%s: no matches", arg)
			}
			out = append(out, matches...)
			continue
		}
		out = append(out, resolvePath(s.cwd, arg))
	}
	if !recursive {
		return out, nil
	}
	var expanded []Path
	for _, p := range out {
		entry, err := s.statPath(ctx, p)
		if err != nil {
			// Keep the raw path; the caller will report the error.
			expanded = append(expanded, p)
			continue
		}
		if !entry.IsDir || p.IsBucket() || p.IsAlias() || p.IsRoot() {
			expanded = append(expanded, p)
			continue
		}
		c, err := s.clientFor(p)
		if err != nil {
			return nil, err
		}
		err = c.ListRecursive(ctx, p.Bucket, p.Key, func(e s3fs.Entry) error {
			expanded = append(expanded, Path{Alias: p.Alias, Bucket: p.Bucket, Key: e.Key})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return expanded, nil
}

// globS3 expands a single glob pattern against the virtual filesystem.
func (s *Shell) globS3(ctx context.Context, pattern string) ([]Path, error) {
	dir, base := splitGlob(pattern)
	var dirPath Path
	if strings.HasPrefix(pattern, "/") {
		dirPath = resolvePath(s.cwd, dir)
	} else {
		dirPath = resolvePath(s.cwd, dir)
	}
	entries, err := s.listDir(ctx, dirPath, false)
	if err != nil {
		return nil, err
	}
	var out []Path
	for _, e := range entries {
		if matchGlob(base, e.Entry.Name) {
			out = append(out, e.Path)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

func joinKey(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return strings.TrimSuffix(prefix, "/") + "/" + name
}

func hasGlob(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func splitGlob(pattern string) (dir, base string) {
	i := strings.LastIndex(pattern, "/")
	if i < 0 {
		return "", pattern
	}
	return pattern[:i], pattern[i+1:]
}

// matchGlob matches a shell-style pattern against a name (no path separators).
func matchGlob(pattern, name string) bool {
	ok, err := filepath.Match(pattern, name)
	return err == nil && ok
}

// printError reports a non-fatal error and marks the command as failed.
func (s *Shell) printError(err error) {
	if err == nil {
		return
	}
	s.exitCode = 1
	fmt.Fprintln(s.stderr, ui.Error.Sprint("error: ")+err.Error())
}

// warn prints a warning message.
func (s *Shell) warn(format string, args ...any) {
	fmt.Fprintln(s.stderr, ui.Warn.Sprint("warning: ")+fmt.Sprintf(format, args...))
}
