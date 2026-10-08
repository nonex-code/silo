package shell

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nonex-code/silo/internal/s3fs"
	"github.com/nonex-code/silo/internal/ui"
)

func init() {
	register(&command{
		name:    "stat",
		usage:   "stat <path>",
		summary: "show detailed information about an object",
		group:   "objects",
		run:     cmdStat,
	})
	register(&command{
		name:    "cat",
		usage:   "cat <file...>",
		summary: "print object contents to stdout",
		group:   "objects",
		run:     cmdCat,
	})
	register(&command{
		name:    "head",
		usage:   "head [-n N] <file...>",
		summary: "print the first N lines of an object (default 10)",
		group:   "objects",
		run:     cmdHead,
	})
	register(&command{
		name:    "tail",
		usage:   "tail [-n N] <file...>",
		summary: "print the last N lines of an object (default 10)",
		group:   "objects",
		run:     cmdTail,
	})
	register(&command{
		name:    "wc",
		usage:   "wc [-l] [-w] [-c] <file...>",
		summary: "count lines, words and bytes",
		group:   "objects",
		run:     cmdWc,
	})
	register(&command{
		name:    "touch",
		usage:   "touch <file...>",
		summary: "create empty objects (or refresh their timestamp)",
		group:   "objects",
		run:     cmdTouch,
	})
	register(&command{
		name:    "mkdir",
		usage:   "mkdir [-p] <prefix...>",
		summary: "create a folder marker or a bucket",
		group:   "objects",
		run:     cmdMkdir,
	})
	register(&command{
		name:    "rm",
		usage:   "rm [-r] [-f] [-n] <path...>",
		summary: "remove objects, prefixes (-r) or buckets (-r)",
		group:   "objects",
		run:     cmdRm,
	})
	register(&command{
		name:    "rmdir",
		usage:   "rmdir [-p] <prefix...>",
		summary: "remove an empty prefix (folder marker)",
		group:   "objects",
		run:     cmdRmdir,
	})
	registerAlias("delete", "rm")
	registerAlias("del", "rm")
	register(&command{
		name:    "presign",
		usage:   "presign [-e 7d] <file>",
		summary: "generate a presigned download URL",
		group:   "objects",
		run:     cmdPresign,
	})
	register(&command{
		name:    "du",
		usage:   "du [-a] [path...]",
		summary: "summarise object sizes recursively",
		group:   "objects",
		run:     cmdDu,
	})
	register(&command{
		name:    "find",
		usage:   "find [path] [-name PATTERN] [-type f|d] [-size +N|-N] [-maxdepth N]",
		summary: "recursively search for objects and prefixes",
		group:   "objects",
		run:     cmdFind,
	})
}

func requireObject(ctx context.Context, sh *Shell, p Path) (*s3fs.Client, s3fs.Entry, error) {
	if p.Alias == "" || p.Bucket == "" || p.Key == "" {
		return nil, s3fs.Entry{}, fmt.Errorf("%s is not an object", p)
	}
	c, err := sh.client(p.Alias)
	if err != nil {
		return nil, s3fs.Entry{}, err
	}
	e, err := c.Stat(ctx, p.Bucket, p.Key)
	if err != nil {
		return nil, s3fs.Entry{}, err
	}
	return c, e, nil
}

func cmdStat(ctx context.Context, sh *Shell, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: stat <path>")
	}
	p := resolvePath(sh.cwd, args[0])
	if p.IsRoot() || p.IsAlias() {
		return fmt.Errorf("stat: %s is not an object", p)
	}
	entry, err := sh.statPath(ctx, p)
	if err != nil {
		return err
	}
	if entry.IsDir || p.IsBucket() {
		return statDir(ctx, sh, p)
	}
	c, e, err := requireObject(ctx, sh, p)
	if err != nil {
		return err
	}
	fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Name:"), p.String())
	fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Type:"), "object")
	fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Alias:"), c.Alias)
	fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Bucket:"), p.Bucket)
	fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Key:"), p.Key)
	fmt.Fprintf(sh.stdout, "%s %d (%s)\n", ui.Bold.Sprint("Size:"), e.Size, ui.HumanSize(e.Size))
	fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Last-Modified:"), ui.HumanTime(e.LastModified))
	fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("ETag:"), strings.Trim(e.ETag, `"`))
	if e.StorageClass != "" {
		fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Storage-Class:"), e.StorageClass)
	}
	if e.VersionID != "" {
		fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Version-ID:"), e.VersionID)
	}
	if e.Owner != "" {
		fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Owner:"), e.Owner)
	}
	if e.ContentType != "" {
		fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Content-Type:"), e.ContentType)
	}
	if e.ContentEncoding != "" {
		fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Content-Encoding:"), e.ContentEncoding)
	}
	return nil
}

// statDir describes a bucket or prefix: object count and total size.
func statDir(ctx context.Context, sh *Shell, p Path) error {
	c, err := sh.clientFor(p)
	if err != nil {
		return err
	}
	var count, total int64
	var newest time.Time
	if p.Key == "" {
		if _, err := c.BucketExists(ctx, p.Bucket); err != nil {
			return err
		}
		err = c.ListRecursive(ctx, p.Bucket, "", func(e s3fs.Entry) error {
			count++
			total += e.Size
			if e.LastModified.After(newest) {
				newest = e.LastModified
			}
			return nil
		})
	} else {
		err = c.ListRecursive(ctx, p.Bucket, p.Key, func(e s3fs.Entry) error {
			count++
			total += e.Size
			if e.LastModified.After(newest) {
				newest = e.LastModified
			}
			return nil
		})
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Name:"), p.String())
	fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Type:"), "directory")
	fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Alias:"), p.Alias)
	fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Bucket:"), p.Bucket)
	if p.Key != "" {
		fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Prefix:"), strings.TrimSuffix(p.Key, "/")+"/")
	}
	fmt.Fprintf(sh.stdout, "%s %d\n", ui.Bold.Sprint("Objects:"), count)
	fmt.Fprintf(sh.stdout, "%s %d (%s)\n", ui.Bold.Sprint("Total-Size:"), total, ui.HumanSize(total))
	if !newest.IsZero() {
		fmt.Fprintf(sh.stdout, "%s %s\n", ui.Bold.Sprint("Last-Modified:"), ui.HumanTime(newest))
	}
	return nil
}

func cmdCat(ctx context.Context, sh *Shell, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cat <file...>")
	}
	for _, arg := range args {
		p := resolvePath(sh.cwd, arg)
		c, _, err := requireObject(ctx, sh, p)
		if err != nil {
			sh.printError(fmt.Errorf("%s: %w", p, err))
			continue
		}
		rc, err := c.Get(ctx, p.Bucket, p.Key, 0, 0)
		if err != nil {
			sh.printError(fmt.Errorf("%s: %w", p, err))
			continue
		}
		if _, err := io.Copy(sh.stdout, rc); err != nil {
			sh.printError(fmt.Errorf("%s: %w", p, err))
		}
		rc.Close()
	}
	return nil
}

func parseNFlag(sh *Shell, args []string, def int) (flags *parsedArgs, n int, rest []string, err error) {
	pa, err := parseArgs(args, []flagDef{{Names: []string{"n", "lines"}, Value: true}})
	if err != nil {
		return nil, 0, nil, err
	}
	n = def
	if v := pa.Get("lines"); v != "" {
		n, err = strconv.Atoi(v)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("invalid -n value %q", v)
		}
	}
	return pa, n, pa.Positional(), nil
}

func cmdHead(ctx context.Context, sh *Shell, args []string) error {
	_, n, files, err := parseNFlag(sh, args, 10)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("usage: head [-n N] <file...>")
	}
	for _, arg := range files {
		p := resolvePath(sh.cwd, arg)
		c, _, err := requireObject(ctx, sh, p)
		if err != nil {
			sh.printError(fmt.Errorf("%s: %w", p, err))
			continue
		}
		rc, err := c.Get(ctx, p.Bucket, p.Key, 0, 0)
		if err != nil {
			sh.printError(fmt.Errorf("%s: %w", p, err))
			continue
		}
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for i := 0; i < n && sc.Scan(); i++ {
			fmt.Fprintln(sh.stdout, sc.Text())
		}
		rc.Close()
	}
	return nil
}

func cmdTail(ctx context.Context, sh *Shell, args []string) error {
	_, n, files, err := parseNFlag(sh, args, 10)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("usage: tail [-n N] <file...>")
	}
	const maxTail = 1 << 20 // read at most 1 MiB from the end
	for _, arg := range files {
		p := resolvePath(sh.cwd, arg)
		c, _, err := requireObject(ctx, sh, p)
		if err != nil {
			sh.printError(fmt.Errorf("%s: %w", p, err))
			continue
		}
		data, err := c.GetTail(ctx, p.Bucket, p.Key, maxTail)
		if err != nil {
			sh.printError(fmt.Errorf("%s: %w", p, err))
			continue
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) > n {
			lines = lines[len(lines)-n:]
		}
		for _, l := range lines {
			fmt.Fprintln(sh.stdout, l)
		}
	}
	return nil
}

func cmdWc(ctx context.Context, sh *Shell, args []string) error {
	pa, err := parseArgs(args, []flagDef{
		{Names: []string{"l", "lines"}},
		{Names: []string{"w", "words"}},
		{Names: []string{"c", "bytes"}},
	})
	if err != nil {
		return err
	}
	files := pa.Positional()
	if len(files) == 0 {
		return fmt.Errorf("usage: wc [-l] [-w] [-c] <file...>")
	}
	linesOnly, wordsOnly, bytesOnly := pa.Has("lines"), pa.Has("words"), pa.Has("bytes")
	if !linesOnly && !wordsOnly && !bytesOnly {
		linesOnly, wordsOnly, bytesOnly = true, true, true
	}
	for _, arg := range files {
		p := resolvePath(sh.cwd, arg)
		c, info, err := requireObject(ctx, sh, p)
		if err != nil {
			sh.printError(fmt.Errorf("%s: %w", p, err))
			continue
		}
		var lines, words int64
		if linesOnly || wordsOnly {
			rc, err := c.Get(ctx, p.Bucket, p.Key, 0, 0)
			if err != nil {
				sh.printError(fmt.Errorf("%s: %w", p, err))
				continue
			}
			sc := bufio.NewScanner(rc)
			sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
			for sc.Scan() {
				lines++
				words += int64(len(strings.Fields(sc.Text())))
			}
			rc.Close()
		}
		var parts []string
		if linesOnly {
			parts = append(parts, strconv.FormatInt(lines, 10))
		}
		if wordsOnly {
			parts = append(parts, strconv.FormatInt(words, 10))
		}
		if bytesOnly {
			parts = append(parts, strconv.FormatInt(info.Size, 10))
		}
		fmt.Fprintf(sh.stdout, "%s %s\n", strings.Join(parts, "\t"), p.String())
	}
	return nil
}

func cmdTouch(ctx context.Context, sh *Shell, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: touch <file...>")
	}
	for _, arg := range args {
		p := resolvePath(sh.cwd, arg)
		if p.Alias == "" || p.Bucket == "" || p.Key == "" {
			sh.printError(fmt.Errorf("touch: %s is not an object path", p))
			continue
		}
		c, err := sh.client(p.Alias)
		if err != nil {
			sh.printError(err)
			continue
		}
		if err := c.Touch(ctx, p.Bucket, p.Key); err != nil {
			sh.printError(fmt.Errorf("%s: %w", p, err))
		}
	}
	return nil
}

func cmdMkdir(ctx context.Context, sh *Shell, args []string) error {
	pa, err := parseArgs(args, []flagDef{{Names: []string{"p", "parents"}}})
	if err != nil {
		return err
	}
	parents := pa.Has("parents")
	pos := pa.Positional()
	if len(pos) == 0 {
		return fmt.Errorf("usage: mkdir [-p] <prefix...>")
	}
	for _, arg := range pos {
		p := resolvePath(sh.cwd, arg)
		if p.IsRoot() || p.IsAlias() {
			sh.printError(fmt.Errorf("mkdir: cannot create %s", p))
			continue
		}
		c, err := sh.client(p.Alias)
		if err != nil {
			sh.printError(err)
			continue
		}
		if p.Key == "" {
			if err := c.MakeBucket(ctx, p.Bucket); err != nil && !strings.Contains(strings.ToLower(err.Error()), "exist") {
				sh.printError(err)
				continue
			}
			fmt.Fprintf(sh.stdout, "%s %s\n", ui.OK.Sprint("created bucket"), p.String())
			continue
		}
		if parents {
			if err := makeParentMarkers(ctx, c, p.Bucket, p.Key); err != nil {
				sh.printError(err)
				continue
			}
		}
		key := strings.TrimSuffix(p.Key, "/") + "/"
		if err := c.PutBytes(ctx, p.Bucket, key, nil, "application/octet-stream"); err != nil {
			sh.printError(err)
			continue
		}
		fmt.Fprintf(sh.stdout, "%s %s\n", ui.OK.Sprint("created"), p.String())
	}
	return nil
}

// makeParentMarkers creates zero-byte marker objects for every ancestor prefix
// of key (mkdir -p semantics).
func makeParentMarkers(ctx context.Context, c *s3fs.Client, bucket, key string) error {
	key = strings.TrimSuffix(key, "/")
	parts := strings.Split(key, "/")
	for i := 1; i < len(parts); i++ {
		prefix := strings.Join(parts[:i], "/") + "/"
		if err := c.PutBytes(ctx, bucket, prefix, nil, "application/octet-stream"); err != nil {
			return err
		}
	}
	return nil
}

func cmdRm(ctx context.Context, sh *Shell, args []string) error {
	pa, err := parseArgs(args, []flagDef{
		{Names: []string{"r", "recursive"}},
		{Names: []string{"f", "force"}},
		{Names: []string{"n", "dry-run"}},
	})
	if err != nil {
		return err
	}
	recursive := pa.Has("recursive")
	force := pa.Has("force")
	dryRun := pa.Has("dry-run")
	paths := pa.Positional()
	if len(paths) == 0 {
		return fmt.Errorf("usage: rm [-r] [-f] [-n] <path...>")
	}
	targets, err := sh.expandS3(ctx, paths, false)
	if err != nil {
		if force {
			return nil
		}
		return err
	}
	for _, p := range targets {
		if p.IsRoot() || p.IsAlias() {
			sh.printError(fmt.Errorf("refusing to remove %s", p))
			continue
		}
		c, err := sh.clientFor(p)
		if err != nil {
			sh.printError(err)
			continue
		}
		switch {
		case p.IsBucket():
			if !recursive {
				sh.printError(fmt.Errorf("%s is a bucket; use -r or 'rb'", p))
				continue
			}
			if dryRun {
				fmt.Fprintf(sh.stdout, "%s %s\n", ui.Warn.Sprint("would remove bucket"), p.String())
				continue
			}
			if err := removePrefix(ctx, sh, c, p.Bucket, ""); err != nil {
				sh.printError(err)
				continue
			}
			if err := c.RemoveBucket(ctx, p.Bucket); err != nil {
				sh.printError(err)
				continue
			}
			fmt.Fprintf(sh.stdout, "%s %s\n", ui.OK.Sprint("removed bucket"), p.String())
		case p.IsObject():
			// Could be an object or a prefix; stat first.
			entry, serr := c.Stat(ctx, p.Bucket, p.Key)
			if serr == nil && !entry.IsDir {
				if dryRun {
					fmt.Fprintf(sh.stdout, "%s %s\n", ui.Warn.Sprint("would remove"), p.String())
					continue
				}
				if err := c.RemoveObject(ctx, p.Bucket, p.Key); err != nil {
					if !force {
						sh.printError(fmt.Errorf("%s: %w", p, err))
					}
					continue
				}
				fmt.Fprintf(sh.stdout, "%s %s\n", ui.OK.Sprint("removed"), p.String())
				continue
			}
			if !recursive {
				if !force {
					sh.printError(fmt.Errorf("%s is not an object (use -r for prefixes)", p))
				}
				continue
			}
			if dryRun {
				fmt.Fprintf(sh.stdout, "%s %s\n", ui.Warn.Sprint("would remove"), p.String())
				continue
			}
			if err := removePrefix(ctx, sh, c, p.Bucket, p.Key); err != nil {
				sh.printError(err)
				continue
			}
			fmt.Fprintf(sh.stdout, "%s %s\n", ui.OK.Sprint("removed"), p.String())
		default:
			if !force {
				sh.printError(fmt.Errorf("cannot remove %s", p))
			}
		}
	}
	return nil
}

func cmdRmdir(ctx context.Context, sh *Shell, args []string) error {
	pa, err := parseArgs(args, []flagDef{{Names: []string{"p", "parents"}}})
	if err != nil {
		return err
	}
	parents := pa.Has("parents")
	pos := pa.Positional()
	if len(pos) == 0 {
		return fmt.Errorf("usage: rmdir [-p] <prefix...>")
	}
	for _, arg := range pos {
		p := resolvePath(sh.cwd, arg)
		if p.IsRoot() || p.IsAlias() || p.Key == "" {
			sh.printError(fmt.Errorf("rmdir: %s is not a prefix", p))
			continue
		}
		c, err := sh.clientFor(p)
		if err != nil {
			sh.printError(err)
			continue
		}
		// Refuse unless the prefix has no children (folder markers aside).
		children, err := c.List(ctx, p.Bucket, p.Key, false)
		if err != nil {
			sh.printError(err)
			continue
		}
		if len(children) > 0 {
			sh.printError(fmt.Errorf("rmdir: %s: directory not empty (%d entries)", p, len(children)))
			continue
		}
		if err := c.RemoveObject(ctx, p.Bucket, strings.TrimSuffix(p.Key, "/")+"/"); err != nil && !s3fs.IsNotFound(err) {
			sh.printError(err)
			continue
		}
		fmt.Fprintf(sh.stdout, "%s %s\n", ui.OK.Sprint("removed"), p.String())
		if parents {
			cur := p
			for {
				cur = cur.Dir()
				if cur.Key == "" {
					break
				}
				kids, err := c.List(ctx, cur.Bucket, cur.Key, false)
				if err != nil || len(kids) > 0 {
					break
				}
				_ = c.RemoveObject(ctx, cur.Bucket, strings.TrimSuffix(cur.Key, "/")+"/")
				fmt.Fprintf(sh.stdout, "%s %s\n", ui.OK.Sprint("removed"), cur.String())
			}
		}
	}
	return nil
}

func removePrefix(ctx context.Context, sh *Shell, c *s3fs.Client, bucket, prefix string) error {
	keys, err := c.ListKeys(ctx, bucket, prefix)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	return c.RemoveObjects(ctx, bucket, keys, func(key string, err error) {
		if err != nil {
			sh.printError(fmt.Errorf("%s/%s: %w", bucket, key, err))
		}
	})
}

func cmdPresign(ctx context.Context, sh *Shell, args []string) error {
	pa, err := parseArgs(args, []flagDef{{Names: []string{"e", "expire"}, Value: true}})
	if err != nil {
		return err
	}
	pos := pa.Positional()
	if len(pos) != 1 {
		return fmt.Errorf("usage: presign [-e 7d] <file>")
	}
	expires := 7 * 24 * time.Hour
	if v := pa.Get("expire"); v != "" {
		expires, err = parseDuration(v)
		if err != nil {
			return err
		}
	}
	p := resolvePath(sh.cwd, pos[0])
	c, _, err := requireObject(ctx, sh, p)
	if err != nil {
		return err
	}
	url, err := c.PresignedGet(ctx, p.Bucket, p.Key, expires)
	if err != nil {
		return err
	}
	fmt.Fprintln(sh.stdout, url)
	return nil
}

// parseDuration understands Go durations plus a trailing "d" for days.
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return d, nil
}

func cmdDu(ctx context.Context, sh *Shell, args []string) error {
	pa, err := parseArgs(args, []flagDef{
		{Names: []string{"a", "all"}},
	})
	if err != nil {
		return err
	}
	all := pa.Has("all")
	paths := pa.Positional()
	if len(paths) == 0 {
		paths = []string{"."}
	}
	var grand int64
	for _, arg := range paths {
		p := resolvePath(sh.cwd, arg)
		entry, err := sh.statPath(ctx, p)
		if err != nil {
			sh.printError(fmt.Errorf("%s: %w", p, err))
			continue
		}
		if !entry.IsDir {
			fmt.Fprintf(sh.stdout, "%s\t%s\n", ui.HumanSize(entry.Size), p.String())
			grand += entry.Size
			continue
		}
		if p.IsRoot() || p.IsAlias() {
			sh.printError(fmt.Errorf("%s: cannot summarise aliases", p))
			continue
		}
		c, err := sh.clientFor(p)
		if err != nil {
			sh.printError(err)
			continue
		}
		var total int64
		var count int64
		err = c.ListRecursive(ctx, p.Bucket, p.Key, func(e s3fs.Entry) error {
			total += e.Size
			count++
			if all {
				fmt.Fprintf(sh.stdout, "%s\t%s\n", ui.HumanSize(e.Size), joinRemote(p.Alias, p.Bucket, e.Key))
			}
			return nil
		})
		if err != nil {
			sh.printError(err)
			continue
		}
		fmt.Fprintf(sh.stdout, "%s\t%s\t(%d objects)\n", ui.HumanSize(total), p.String(), count)
		grand += total
	}
	if len(paths) > 1 {
		fmt.Fprintf(sh.stdout, "%s\t%s\n", ui.HumanSize(grand), ui.Bold.Sprint("total"))
	}
	return nil
}

func cmdFind(ctx context.Context, sh *Shell, args []string) error {
	pa, err := parseArgs(args, []flagDef{
		{Names: []string{"name"}, Value: true},
		{Names: []string{"type"}, Value: true},
		{Names: []string{"size"}, Value: true},
		{Names: []string{"maxdepth"}, Value: true},
	})
	if err != nil {
		return err
	}
	root := sh.cwd
	pos := pa.Positional()
	if len(pos) > 0 {
		root = resolvePath(sh.cwd, pos[0])
	}
	if root.IsRoot() || root.IsAlias() {
		return fmt.Errorf("find: need a bucket path")
	}
	namePat := pa.Get("name")
	typeFilter := pa.Get("type")
	maxDepth := -1
	if v := pa.Get("maxdepth"); v != "" {
		maxDepth, err = strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid -maxdepth %q", v)
		}
	}
	sizeFilter, err := parseSizeFilter(pa.Get("size"))
	if err != nil {
		return err
	}

	c, err := sh.clientFor(root)
	if err != nil {
		return err
	}
	type match struct {
		path  string
		isDir bool
		size  int64
	}
	var results []match
	seenDirs := map[string]bool{}

	err = c.ListRecursive(ctx, root.Bucket, root.Key, func(e s3fs.Entry) error {
		rel := strings.TrimPrefix(e.Key, strings.TrimSuffix(root.Key, "/"))
		rel = strings.TrimPrefix(rel, "/")
		depth := strings.Count(rel, "/") + 1
		if maxDepth >= 0 && depth > maxDepth {
			return nil
		}
		base := baseNameOf(rel)
		if typeFilter == "" || typeFilter == "f" {
			if namePat == "" || matchGlob(namePat, base) || strings.Contains(rel, namePat) {
				if sizeFilter == nil || sizeFilter(e.Size) {
					results = append(results, match{path: joinPath(root, e.Key), size: e.Size})
				}
			}
		}
		if typeFilter == "" || typeFilter == "d" {
			dir := rel
			for {
				i := strings.LastIndex(dir, "/")
				if i < 0 {
					break
				}
				dir = dir[:i]
				if dir == "" || seenDirs[dir] {
					break
				}
				seenDirs[dir] = true
				dDepth := strings.Count(dir, "/") + 1
				if maxDepth >= 0 && dDepth > maxDepth {
					break
				}
				dBase := baseNameOf(dir)
				if namePat == "" || matchGlob(namePat, dBase) || strings.Contains(dir, namePat) {
					results = append(results, match{path: joinPath(root, dir+"/"), isDir: true})
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(results, func(i, j int) bool { return results[i].path < results[j].path })
	for _, r := range results {
		if r.isDir {
			fmt.Fprintln(sh.stdout, ui.Dir.Sprint(r.path))
		} else {
			fmt.Fprintln(sh.stdout, r.path)
		}
	}
	return nil
}

func baseNameOf(p string) string {
	p = strings.TrimSuffix(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func joinPath(root Path, key string) string {
	p := root
	p.Key = key
	return p.String()
}

// parseSizeFilter returns a predicate for find's -size flag.
func parseSizeFilter(s string) (func(int64) bool, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	op := byte('=')
	body := s
	if s[0] == '+' || s[0] == '-' {
		op = s[0]
		body = s[1:]
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(body, "k"):
		mult, body = 1024, strings.TrimSuffix(body, "k")
	case strings.HasSuffix(body, "M"):
		mult, body = 1024*1024, strings.TrimSuffix(body, "M")
	case strings.HasSuffix(body, "G"):
		mult, body = 1024*1024*1024, strings.TrimSuffix(body, "G")
	}
	n, err := strconv.ParseInt(body, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid -size %q", s)
	}
	target := n * mult
	return func(size int64) bool {
		switch op {
		case '+':
			return size > target
		case '-':
			return size < target
		default:
			return size == target
		}
	}, nil
}
