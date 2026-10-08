package shell

import (
	"bufio"
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/nonex-code/silo/internal/s3fs"
)

func init() {
	register(&command{
		name:    "grep",
		usage:   "grep [-i] [-v] [-n] [-l] [-c] [-h] [-E] [--include G] [--exclude G] <pattern> <path...>",
		summary: "search the contents of objects (streamed from the server)",
		group:   "objects",
		run:     cmdGrep,
	})
}

type lineMatcher func(string) bool

func buildMatcher(pattern string, ignoreCase, useRegex bool) (lineMatcher, error) {
	if useRegex {
		p := pattern
		if ignoreCase {
			p = "(?i)" + p
		}
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern: %w", err)
		}
		return re.MatchString, nil
	}
	if ignoreCase {
		lower := strings.ToLower(pattern)
		return func(s string) bool { return strings.Contains(strings.ToLower(s), lower) }, nil
	}
	return func(s string) bool { return strings.Contains(s, pattern) }, nil
}

func cmdGrep(ctx context.Context, sh *Shell, args []string) error {
	pa, err := parseArgs(args, []flagDef{
		{Names: []string{"i", "ignore-case"}},
		{Names: []string{"v", "invert-match"}},
		{Names: []string{"n", "line-number"}},
		{Names: []string{"l", "files-with-matches"}},
		{Names: []string{"c", "count"}},
		{Names: []string{"h", "no-filename"}},
		{Names: []string{"E", "extended-regexp"}},
		{Names: []string{"include"}, Value: true},
		{Names: []string{"exclude"}, Value: true},
	})
	if err != nil {
		return err
	}
	pos := pa.Positional()
	if len(pos) < 2 {
		return fmt.Errorf("usage: grep [-i] [-v] [-n] [-l] [-c] [-h] [-E] <pattern> <path...>")
	}
	pattern := pos[0]
	paths := pos[1:]

	match, err := buildMatcher(pattern, pa.Has("ignore-case"), pa.Has("extended-regexp"))
	if err != nil {
		return err
	}
	invert := pa.Has("invert-match")
	showNum := pa.Has("line-number")
	filesWith := pa.Has("files-with-matches")
	countOnly := pa.Has("count")
	noName := pa.Has("no-filename")
	includes := pa.GetAll("include")
	excludes := pa.GetAll("exclude")

	var matched int
	for _, arg := range paths {
		p := resolvePath(sh.cwd, arg)
		objects, err := sh.grepObjects(ctx, p, includes, excludes)
		if err != nil {
			sh.printError(fmt.Errorf("%s: %w", p, err))
			continue
		}
		for _, obj := range objects {
			n, err := sh.grepObject(ctx, obj, match, invert, showNum, filesWith, countOnly, noName)
			if err != nil {
				sh.printError(err)
				continue
			}
			matched += n
		}
	}
	if matched == 0 {
		// Like grep: no matches is a non-zero status, not an error message.
		sh.exitCode = 1
	}
	return nil
}

// grepObjects expands a path into the concrete objects to scan.
func (s *Shell) grepObjects(ctx context.Context, p Path, includes, excludes []string) ([]Path, error) {
	if p.IsRoot() || p.IsAlias() {
		return nil, fmt.Errorf("grep: need a bucket path")
	}
	var out []Path
	add := func(pp Path) {
		if !matchFilters(pp.Base(), pp.String(), includes, excludes) {
			return
		}
		out = append(out, pp)
	}
	entry, err := s.statPath(ctx, p)
	if err == nil {
		if !entry.IsDir {
			add(p)
			return out, nil
		}
		c, cerr := s.clientFor(p)
		if cerr != nil {
			return nil, cerr
		}
		err = c.ListRecursive(ctx, p.Bucket, p.Key, func(e s3fs.Entry) error {
			add(Path{Alias: p.Alias, Bucket: p.Bucket, Key: e.Key})
			return nil
		})
		return out, err
	}
	return out, nil
}

func (s *Shell) grepObject(ctx context.Context, p Path, match lineMatcher, invert, showNum, filesWith, countOnly, noName bool) (int, error) {
	c, err := s.clientFor(p)
	if err != nil {
		return 0, err
	}
	rc, err := c.Get(ctx, p.Bucket, p.Key, 0, 0)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", p, err)
	}
	defer rc.Close()

	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	matches := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		ok := match(line)
		if invert {
			ok = !ok
		}
		if !ok {
			continue
		}
		matches++
		if filesWith {
			fmt.Fprintln(s.stdout, p.String())
			return matches, nil
		}
		if countOnly {
			continue
		}
		prefix := ""
		if !noName {
			prefix = p.String() + ":"
		}
		if showNum {
			fmt.Fprintf(s.stdout, "%s%d:%s\n", prefix, lineNo, line)
		} else {
			fmt.Fprintf(s.stdout, "%s%s\n", prefix, line)
		}
	}
	if err := sc.Err(); err != nil {
		return matches, fmt.Errorf("%s: %w", p, err)
	}
	if countOnly {
		fmt.Fprintf(s.stdout, "%s:%d\n", p.String(), matches)
	}
	return matches, nil
}

// matchFilters reports whether name/path passes the include/exclude globs.
func matchFilters(name, full string, includes, excludes []string) bool {
	if len(includes) > 0 {
		ok := false
		for _, pat := range includes {
			if matchGlob(pat, name) || matchGlob(pat, full) || matchGlob(pat, strings.TrimPrefix(full, "/")) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, pat := range excludes {
		if matchGlob(pat, name) || matchGlob(pat, full) || matchGlob(pat, strings.TrimPrefix(full, "/")) {
			return false
		}
	}
	return true
}
