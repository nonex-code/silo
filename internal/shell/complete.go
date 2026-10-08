package shell

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ergochat/readline"
)

// completer implements readline.AutoCompleter for the shell.
type completer struct {
	sh *Shell
}

var _ readline.AutoCompleter = (*completer)(nil)

// Do returns completion suffixes for the word at the cursor.
func (c *completer) Do(line []rune, pos int) ([][]rune, int) {
	if pos > len(line) {
		pos = len(line)
	}
	text := string(line[:pos])
	start := currentWordStart(text)
	word := text[start:]
	before := text[:start]

	tokens, err := tokenize(before)
	if err != nil {
		tokens = strings.Fields(before)
	}

	var cands []string
	switch {
	case len(tokens) == 0:
		cands = commandCandidates(word)
	default:
		cands = c.argCandidates(tokens[0], tokens[1:], word)
	}
	return toSuffixes(cands, word)
}

// currentWordStart returns the byte index where the word under the cursor
// begins.
func currentWordStart(text string) int {
	for i := len(text) - 1; i >= 0; i-- {
		switch text[i] {
		case ' ', '\t':
			return i + 1
		}
	}
	return 0
}

// commandFlagCandidates lists the flags each command accepts, used for
// tab completion of words that start with '-'.
var commandFlagCandidates = map[string][]string{
	"ls":      {"-l", "-R", "-a", "-1"},
	"ll":      {"-R", "-a", "-1"},
	"tree":    {"-L"},
	"head":    {"-n"},
	"tail":    {"-n"},
	"wc":      {"-l", "-w", "-c"},
	"du":      {"-a"},
	"find":    {"-name", "-type", "-size", "-maxdepth"},
	"mkdir":   {"-p"},
	"rmdir":   {"-p"},
	"rm":      {"-r", "-f", "-n"},
	"presign": {"-e"},
	"hash":    {"-t", "--type"},
	"grep":    {"-i", "-v", "-n", "-l", "-c", "-h", "-E", "--include", "--exclude"},
	"put":     {"-r", "-c", "-n", "--parallel"},
	"get":     {"-r", "-c", "-n", "--parallel"},
	"cp":      {"-r", "--parallel"},
	"mv":      {"-r", "--parallel"},
	"mirror":  {"--overwrite", "--remove", "-n", "--include", "--exclude", "--parallel"},
	"diff":    {"--include", "--exclude"},
	"mb":      {},
	"rb":      {"-f"},
	"buckets": {"-a"},
	"history": {"-c"},
	"alias":   {"--path", "--api", "--region"},
}

// flagCandidates returns the flags of cmd that start with word.
func flagCandidates(cmd, word string) []string {
	var out []string
	for _, f := range commandFlagCandidates[cmd] {
		if strings.HasPrefix(f, word) {
			out = append(out, f)
		}
	}
	return out
}

func (c *completer) argCandidates(cmd string, subArgs []string, word string) []string {
	if canon, ok := commandAliases[cmd]; ok {
		cmd = canon
	}
	if strings.HasPrefix(word, "-") {
		if fs := flagCandidates(cmd, word); len(fs) > 0 {
			return fs
		}
	}
	argN := len(subArgs)
	switch cmd {
	case "help":
		return commandCandidates(word)
	case "use", "buckets":
		return c.aliasCandidates(word)
	case "alias":
		if argN == 0 {
			return prefixFilter([]string{"list", "set", "remove", "use"}, word)
		}
		switch subArgs[0] {
		case "remove", "rm", "use":
			return c.aliasCandidates(word)
		}
		return nil
	case "put":
		if argN == 0 {
			return localCandidates(word)
		}
		return c.pathCandidates(word)
	case "get":
		if argN == 0 {
			return c.pathCandidates(word)
		}
		return localCandidates(word)
	case "mirror", "diff":
		return append(localCandidates(word), c.pathCandidates(word)...)
	default:
		return c.pathCandidates(word)
	}
}

func commandCandidates(word string) []string {
	seen := map[string]bool{}
	var names []string
	for name := range commands {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	for alias := range commandAliases {
		if !seen[alias] {
			seen[alias] = true
			names = append(names, alias)
		}
	}
	return prefixFilter(names, word)
}

func (c *completer) aliasCandidates(word string) []string {
	return prefixFilter(c.sh.cfg.Names(), word)
}

func (c *completer) pathCandidates(word string) []string {
	dirPart, partial := splitWord(word)
	var dirPath Path
	if dirPart == "" {
		dirPath = c.sh.cwd
	} else {
		dirPath = resolvePath(c.sh.cwd, dirPart)
	}
	entries, err := c.sh.listDir(context.Background(), dirPath, false)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Entry.Name
		if e.Entry.IsDir || e.IsAlias {
			name += "/"
		}
		if strings.HasPrefix(name, partial) {
			out = append(out, dirPart+name)
		}
	}
	sort.Strings(out)
	return out
}

// localCandidates completes local filesystem paths for the put command.
func localCandidates(word string) []string {
	dir := filepath.Dir(word)
	base := filepath.Base(word)
	if word == "" {
		dir = "."
		base = ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, base) {
			continue
		}
		candidate := name
		if dir != "." {
			candidate = filepath.ToSlash(filepath.Join(dir, name))
		}
		if e.IsDir() {
			candidate += "/"
		}
		out = append(out, candidate)
	}
	sort.Strings(out)
	return out
}

func prefixFilter(names []string, word string) []string {
	var out []string
	for _, n := range names {
		if strings.HasPrefix(n, word) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// splitWord splits a partially typed path into the directory part (including
// the trailing slash) and the partial name.
func splitWord(word string) (dir, partial string) {
	if i := strings.LastIndex(word, "/"); i >= 0 {
		return word[:i+1], word[i+1:]
	}
	return "", word
}

func toSuffixes(cands []string, word string) ([][]rune, int) {
	offset := utf8.RuneCountInString(word)
	out := make([][]rune, 0, len(cands))
	seen := map[string]bool{}
	for _, c := range cands {
		if !strings.HasPrefix(c, word) {
			continue
		}
		suffix := c[len(word):]
		if suffix == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, []rune(suffix))
	}
	return out, offset
}
