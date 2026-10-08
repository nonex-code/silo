package shell

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nonex-code/silo/internal/ui"
)

func init() {
	register(&command{
		name:    "pwd",
		usage:   "pwd",
		summary: "print the current virtual directory",
		group:   "navigation",
		run: func(ctx context.Context, sh *Shell, args []string) error {
			fmt.Fprintln(sh.stdout, sh.cwd.String())
			return nil
		},
	})

	register(&command{
		name:    "cd",
		usage:   "cd [path]",
		summary: "change the current virtual directory (no argument goes to /)",
		group:   "navigation",
		run:     cmdCd,
	})

	register(&command{
		name:    "ls",
		usage:   "ls [-l] [-1] [-R] [-a] [path...]",
		summary: "list aliases, buckets, prefixes or objects",
		group:   "navigation",
		run:     cmdLs,
	})
	register(&command{
		name:    "ll",
		usage:   "ll [-R] [-a] [path...]",
		summary: "long listing (one entry per line with details)",
		group:   "navigation",
		run:     cmdLl,
	})
	registerAlias("dir", "ls")

	register(&command{
		name:    "tree",
		usage:   "tree [-L depth] [path]",
		summary: "recursively print the directory tree",
		group:   "navigation",
		run:     cmdTree,
	})

	register(&command{
		name:    "use",
		usage:   "use <alias>",
		summary: "switch to an alias namespace (shortcut for 'cd /<alias>')",
		group:   "navigation",
		run: func(ctx context.Context, sh *Shell, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: use <alias>")
			}
			name := args[0]
			if _, ok := sh.cfg.Get(name); !ok {
				return fmt.Errorf("alias %q not found", name)
			}
			sh.cwd = Path{Alias: name}
			sh.updatePrompt()
			return nil
		},
	})
}

func cmdCd(ctx context.Context, sh *Shell, args []string) error {
	if len(args) == 0 {
		sh.cwd = Path{}
		sh.updatePrompt()
		return nil
	}
	if len(args) > 1 {
		return fmt.Errorf("usage: cd [path]")
	}
	if args[0] == "-" {
		if sh.prevDir.Alias == "" && sh.prevDir.Bucket == "" && sh.prevDir.Key == "" && sh.cwd.IsRoot() {
			return fmt.Errorf("no previous directory")
		}
		sh.cwd, sh.prevDir = sh.prevDir, sh.cwd
		sh.updatePrompt()
		fmt.Fprintln(sh.stdout, sh.cwd.String())
		return nil
	}

	target := resolvePath(sh.cwd, args[0])
	if target.IsRoot() {
		sh.prevDir, sh.cwd = sh.cwd, target
		sh.updatePrompt()
		return nil
	}
	if target.IsAlias() {
		if _, ok := sh.cfg.Get(target.Alias); !ok {
			return fmt.Errorf("alias %q not found", target.Alias)
		}
		sh.prevDir, sh.cwd = sh.cwd, target
		sh.updatePrompt()
		return nil
	}

	// Validate the bucket exists.
	c, err := sh.client(target.Alias)
	if err != nil {
		return err
	}
	exists, err := c.BucketExists(ctx, target.Bucket)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("bucket %q does not exist", target.Bucket)
	}
	sh.prevDir, sh.cwd = sh.cwd, target
	sh.updatePrompt()
	return nil
}

func cmdLs(ctx context.Context, sh *Shell, args []string) error {
	return listCommand(ctx, sh, args, false)
}

func cmdLl(ctx context.Context, sh *Shell, args []string) error {
	return listCommand(ctx, sh, args, true)
}

// listEntries resolves a listing target. Per the object-storage model, a
// concrete prefix that does not exist yet simply lists as empty rather than
// being an error (the user can cd into it and create objects).
func (sh *Shell) listEntries(ctx context.Context, p Path, recursive, all bool) ([]fsEntry, error) {
	entry, err := sh.statPath(ctx, p)
	if err == nil {
		if entry.Path.IsObject() && !entry.IsDir {
			return []fsEntry{entry}, nil
		}
		entries, lerr := sh.listDir(ctx, p, recursive)
		if lerr != nil {
			return nil, lerr
		}
		return filterHidden(entries, all), nil
	}
	if p.IsObject() {
		entries, lerr := sh.listDir(ctx, p, recursive)
		if lerr != nil {
			return nil, lerr
		}
		return filterHidden(entries, all), nil
	}
	return nil, err
}

func filterHidden(entries []fsEntry, all bool) []fsEntry {
	if all {
		return entries
	}
	out := entries[:0]
	for _, e := range entries {
		if !e.IsAlias && strings.HasPrefix(e.Entry.Name, ".") {
			continue
		}
		out = append(out, e)
	}
	return out
}

func listCommand(ctx context.Context, sh *Shell, args []string, alwaysLong bool) error {
	pa, err := parseArgs(args, []flagDef{
		{Names: []string{"l", "long"}},
		{Names: []string{"R", "recursive"}},
		{Names: []string{"a", "all"}},
		{Names: []string{"1", "one"}},
	})
	if err != nil {
		return err
	}
	paths := pa.Positional()
	if len(paths) == 0 {
		paths = []string{"."}
	}
	long := alwaysLong || pa.Has("long")
	recursive := pa.Has("recursive")
	one := long || pa.Has("one")
	all := pa.Has("all")

	for i, arg := range paths {
		p := resolvePath(sh.cwd, arg)
		if multiple := len(paths) > 1; multiple {
			if i > 0 {
				fmt.Fprintln(sh.stdout)
			}
			fmt.Fprintf(sh.stdout, "%s:\n", p.String())
		}

		entries, err := sh.listEntries(ctx, p, recursive, all)
		if err != nil {
			sh.printError(err)
			continue
		}
		switch {
		case alwaysLong:
			printLong(sh, entries, true)
		case long:
			printLong(sh, entries, false)
		case one:
			for _, e := range entries {
				fmt.Fprintln(sh.stdout, colorName(e))
			}
		default:
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, colorName(e))
			}
			printColumns(sh, names)
		}
	}
	return nil
}

func printLong(sh *Shell, entries []fsEntry, header bool) {
	if header {
		var total int64
		var files int
		for _, e := range entries {
			if !e.IsDir && !e.IsBucket && !e.IsAlias {
				total += e.Size
				files++
			}
		}
		fmt.Fprintf(sh.stdout, "%s\n", ui.Info.Sprintf("total %d object(s), %s", files, ui.HumanSize(total)))
		fmt.Fprintf(sh.stdout, "%s\n", ui.Info.Sprint(longHeader()))
	}
	for _, e := range entries {
		fmt.Fprintln(sh.stdout, longLine(e))
	}
}

func longHeader() string {
	return fmt.Sprintf("%-10s %10s  %-19s  %-8s  %-9s  %s",
		"MODE", "SIZE", "MODIFIED", "ETAG", "CLASS", "NAME")
}

func longLine(e fsEntry) string {
	mode := "-rw-r--r--"
	size := ui.HumanSize(e.Size)
	etag := "-"
	class := "-"
	if e.IsAlias || e.IsBucket || e.IsDir {
		mode = "drwxr-xr-x"
	}
	if e.IsAlias || e.IsBucket || e.IsDir {
		size = "-"
	}
	if !e.IsDir && !e.IsBucket && !e.IsAlias {
		if e.ETag != "" {
			etag = strings.Trim(e.ETag, `"`)
			if len(etag) > 8 {
				etag = etag[:8]
			}
		}
		if e.StorageClass != "" {
			class = e.StorageClass
		}
	}
	return fmt.Sprintf("%s %10s  %-19s  %-8s  %-9s  %s",
		ui.Info.Sprint(mode),
		size,
		ui.Info.Sprint(ui.HumanTime(e.LastModified)),
		ui.Info.Sprint(etag),
		ui.Info.Sprint(class),
		colorName(e),
	)
}

func colorName(e fsEntry) string {
	name := e.Name
	switch {
	case e.IsAlias:
		return ui.Alias.Sprint(name)
	case e.IsBucket:
		return ui.Bucket.Sprint(name + "/")
	case e.IsDir:
		return ui.Dir.Sprint(name + "/")
	default:
		return ui.Object.Sprint(name)
	}
}

// printColumns lays names out in columns using a fixed nominal width.
func printColumns(sh *Shell, names []string) {
	if len(names) == 0 {
		return
	}
	max := 0
	for _, n := range names {
		if l := len(stripANSI(n)); l > max {
			max = l
		}
	}
	const totalWidth = 100
	colWidth := max + 2
	cols := totalWidth / colWidth
	if cols < 1 {
		cols = 1
	}
	for i, n := range names {
		pad := colWidth - len(stripANSI(n))
		if pad < 0 {
			pad = 0
		}
		fmt.Fprint(sh.stdout, n, strings.Repeat(" ", pad))
		if (i+1)%cols == 0 {
			fmt.Fprintln(sh.stdout)
		}
	}
	if len(names)%cols != 0 {
		fmt.Fprintln(sh.stdout)
	}
}

// stripANSI removes ANSI escape sequences so widths can be measured.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		case r == 0x1b:
			inEsc = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func cmdTree(ctx context.Context, sh *Shell, args []string) error {
	pa, err := parseArgs(args, []flagDef{
		{Names: []string{"L", "level"}, Value: true},
	})
	if err != nil {
		return err
	}
	depth := 3
	if v := pa.Get("level"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &depth); err != nil {
			return fmt.Errorf("invalid depth %q", v)
		}
	}
	pos := pa.Positional()
	root := sh.cwd
	if len(pos) > 0 {
		root = resolvePath(sh.cwd, pos[0])
	}
	fmt.Fprintln(sh.stdout, ui.Dir.Sprint(root.String()))
	return treeWalk(ctx, sh, root, "", depth)
}

func treeWalk(ctx context.Context, sh *Shell, p Path, prefix string, depth int) error {
	if depth <= 0 {
		return nil
	}
	entries, err := sh.listDir(ctx, p, false)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	for i, e := range entries {
		last := i == len(entries)-1
		branch := "├── "
		nextPrefix := prefix + "│   "
		if last {
			branch = "└── "
			nextPrefix = prefix + "    "
		}
		fmt.Fprintf(sh.stdout, "%s%s%s\n", prefix, branch, colorName(e))
		if e.IsDir && !e.Entry.IsBucket && !e.Path.IsRoot() {
			if err := treeWalk(ctx, sh, e.Path, nextPrefix, depth-1); err != nil {
				sh.printError(err)
			}
		}
	}
	return nil
}
