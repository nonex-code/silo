package shell

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/nonex-code/silo/internal/ui"
)

func init() {
	register(&command{
		name:    "diff",
		usage:   "diff [--include G] [--exclude G] <source> <target>",
		summary: "compare two trees (local or remote) and list the differences",
		group:   "transfer",
		run:     cmdDiff,
	})
}

func cmdDiff(ctx context.Context, sh *Shell, args []string) error {
	pa, err := parseArgs(args, []flagDef{
		{Names: []string{"include"}, Value: true},
		{Names: []string{"exclude"}, Value: true},
	})
	if err != nil {
		return err
	}
	pos := pa.Positional()
	if len(pos) != 2 {
		return fmt.Errorf("usage: diff [--include G] [--exclude G] <source> <target>")
	}
	includes := pa.GetAll("include")
	excludes := pa.GetAll("exclude")

	src, err := sh.diffSide(ctx, pos[0], includes, excludes)
	if err != nil {
		sh.printError(fmt.Errorf("%s: %w", pos[0], err))
		return nil
	}
	dst, err := sh.diffSide(ctx, pos[1], includes, excludes)
	if err != nil {
		sh.printError(fmt.Errorf("%s: %w", pos[1], err))
		return nil
	}

	seen := map[string]bool{}
	var rels []string
	for rel := range src {
		if !seen[rel] {
			seen[rel] = true
			rels = append(rels, rel)
		}
	}
	for rel := range dst {
		if !seen[rel] {
			seen[rel] = true
			rels = append(rels, rel)
		}
	}
	sort.Strings(rels)

	var diffs int
	for _, rel := range rels {
		ss, sok := src[rel]
		ds, dok := dst[rel]
		switch {
		case sok && !dok:
			fmt.Fprintf(sh.stdout, "%s %s\n", ui.Warn.Sprint("< only in source:"), rel)
			diffs++
		case !sok && dok:
			fmt.Fprintf(sh.stdout, "%s %s\n", ui.Warn.Sprint("> only in target:"), rel)
			diffs++
		case ss != ds:
			fmt.Fprintf(sh.stdout, "%s %s (%s vs %s)\n", ui.Warn.Sprint("! size differs:"),
				rel, ui.HumanSize(ss), ui.HumanSize(ds))
			diffs++
		}
	}
	if diffs == 0 {
		fmt.Fprintln(sh.stdout, ui.OK.Sprint("no differences"))
	} else {
		fmt.Fprintf(sh.stdout, "%s %d difference(s)\n", ui.Bold.Sprint("diff:"), diffs)
	}
	return nil
}

// diffSide enumerates a local directory or a remote prefix into a map of
// relative path -> size, applying the include/exclude filters.
func (sh *Shell) diffSide(ctx context.Context, arg string, includes, excludes []string) (map[string]int64, error) {
	remote, p, local := sh.mirrorSide(arg)
	out := map[string]int64{}
	if remote {
		if p.IsRoot() || p.IsAlias() || p.Bucket == "" {
			return nil, fmt.Errorf("need a bucket path")
		}
		items, err := enumerateRemote(ctx, sh, p)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if !matchFilters(filepath.Base(it.rel), it.rel, includes, excludes) {
				continue
			}
			out[it.rel] = it.size
		}
		return out, nil
	}
	items, err := enumerateLocal(local)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if !matchFilters(filepath.Base(it.rel), it.rel, includes, excludes) {
			continue
		}
		out[it.rel] = it.size
	}
	return out, nil
}
