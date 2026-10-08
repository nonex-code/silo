package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nonex-code/silo/internal/s3fs"
	"github.com/nonex-code/silo/internal/ui"
)

func init() {
	register(&command{
		name:    "mirror",
		usage:   "mirror [flags] <source> <target>",
		summary: "incrementally sync a tree between local disk and an alias",
		group:   "transfer",
		run:     cmdMirror,
	})
}

// mirrorOpts holds the parsed flags for a mirror run.
type mirrorOpts struct {
	overwrite bool
	remove    bool
	dryRun    bool
	includes  []string
	excludes  []string
	parallel  int
}

// mirrorItem is one source entry: a relative path plus its local or remote
// locator.
type mirrorItem struct {
	rel    string // slash-separated path relative to the source root
	key    string // source locator: full object key or absolute local path
	dstKey string // computed target object key
	size   int64
}

// mirrorStats accumulates counters for the final summary.
type mirrorStats struct {
	mu      sync.Mutex
	copied  int
	skipped int
	removed int
	failed  int
}

func (s *mirrorStats) inc(field *int) {
	s.mu.Lock()
	*field++
	s.mu.Unlock()
}

// mirrorSide classifies an argument as a remote path (whose first component is
// a configured alias) or a local path.
func (s *Shell) mirrorSide(arg string) (remote bool, p Path, local string) {
	if strings.HasPrefix(arg, "~") {
		return false, Path{}, expandHome(arg)
	}
	trimmed := strings.TrimPrefix(arg, "/")
	seg := trimmed
	if i := strings.IndexByte(trimmed, '/'); i >= 0 {
		seg = trimmed[:i]
	}
	if seg == "" {
		return true, Path{}, ""
	}
	if _, ok := s.cfg.Get(seg); ok {
		return true, resolvePath(s.cwd, arg), ""
	}
	return false, Path{}, arg
}

func expandHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, filepath.FromSlash(p[2:]))
	}
	return p
}

func cmdMirror(ctx context.Context, sh *Shell, args []string) error {
	pa, err := parseArgs(args, []flagDef{
		{Names: []string{"overwrite", "force"}},
		{Names: []string{"remove", "delete"}},
		{Names: []string{"n", "dry-run"}},
		{Names: []string{"include"}, Value: true},
		{Names: []string{"exclude"}, Value: true},
		{Names: []string{"parallel", "p"}, Value: true},
	})
	if err != nil {
		return err
	}
	pos := pa.Positional()
	if len(pos) != 2 {
		return fmt.Errorf("usage: mirror [flags] <source> <target>")
	}
	opts := mirrorOpts{
		overwrite: pa.Has("overwrite"),
		remove:    pa.Has("remove"),
		dryRun:    pa.Has("dry-run"),
		includes:  pa.GetAll("include"),
		excludes:  pa.GetAll("exclude"),
		parallel:  parallelFlag(pa, sh.concurrency),
	}

	srcRemote, srcPath, srcLocal := sh.mirrorSide(pos[0])
	dstRemote, dstPath, dstLocal := sh.mirrorSide(pos[1])

	switch {
	case srcRemote && dstRemote:
		return mirrorRemoteRemote(ctx, sh, srcPath, dstPath, opts)
	case srcRemote && !dstRemote:
		return mirrorRemoteLocal(ctx, sh, srcPath, dstLocal, opts)
	case !srcRemote && dstRemote:
		return mirrorLocalRemote(ctx, sh, srcLocal, dstPath, opts)
	default:
		return fmt.Errorf("mirror: at least one of source/target must be a remote alias path")
	}
}

func enumerateLocal(root string) ([]mirrorItem, error) {
	fi, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return []mirrorItem{{rel: filepath.Base(root), key: root, size: fi.Size()}}, nil
	}
	var out []mirrorItem
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, mirrorItem{rel: filepath.ToSlash(rel), key: path, size: info.Size()})
		return nil
	})
	return out, err
}

func enumerateRemote(ctx context.Context, sh *Shell, src Path) ([]mirrorItem, error) {
	c, err := sh.clientFor(src)
	if err != nil {
		return nil, err
	}
	prefix := ""
	if src.Key != "" {
		prefix = strings.TrimSuffix(src.Key, "/") + "/"
	}
	var out []mirrorItem
	err = c.ListRecursive(ctx, src.Bucket, src.Key, func(e s3fs.Entry) error {
		out = append(out, mirrorItem{rel: strings.TrimPrefix(e.Key, prefix), key: e.Key, size: e.Size})
		return nil
	})
	return out, err
}

// mirrorLocalRemote uploads a local tree to a remote prefix.
func mirrorLocalRemote(ctx context.Context, sh *Shell, local string, dst Path, o mirrorOpts) error {
	if dst.IsRoot() || dst.IsAlias() || dst.Bucket == "" {
		return fmt.Errorf("mirror: target must include a bucket")
	}
	items, err := enumerateLocal(local)
	if err != nil {
		return err
	}
	c, err := sh.client(dst.Alias)
	if err != nil {
		return err
	}

	srcSet := map[string]bool{}
	var work []mirrorItem
	for _, it := range items {
		if !matchFilters(filepath.Base(it.rel), it.rel, o.includes, o.excludes) {
			continue
		}
		it.dstKey = joinKey(dst.Key, it.rel)
		srcSet[it.dstKey] = true
		work = append(work, it)
	}

	stats := &mirrorStats{}
	var firstErr error
	var errMu sync.Mutex
	record := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
		stats.inc(&stats.failed)
	}
	prog := ui.NewProgress()
	parallel(o.parallel, len(work), func(i int) {
		it := work[i]
		fi, err := os.Stat(it.key)
		if err != nil {
			record(err)
			return
		}
		if !o.overwrite {
			if e, err := c.Stat(ctx, dst.Bucket, it.dstKey); err == nil && e.Size == fi.Size() {
				stats.inc(&stats.skipped)
				return
			}
		}
		if o.dryRun {
			fmt.Fprintf(sh.stdout, "%s %s -> %s\n", ui.Warn.Sprint("would upload"), it.key, joinRemote(dst.Alias, dst.Bucket, it.dstKey))
			return
		}
		bar := prog.AddBar(it.rel, fi.Size(), 0)
		err = c.UploadFile(ctx, dst.Bucket, it.dstKey, it.key, s3fs.UploadOptions{
			PartSize:    sh.partSize,
			Concurrency: sh.concurrency,
			Progress:    func(d int64) { bar.IncrBy(int(d)) },
		})
		if err != nil {
			bar.Abort(true)
			record(fmt.Errorf("%s: %w", it.key, err))
			return
		}
		bar.SetTotal(fi.Size(), true)
		stats.inc(&stats.copied)
	})
	prog.Wait()

	if o.remove {
		err := c.ListRecursive(ctx, dst.Bucket, dst.Key, func(e s3fs.Entry) error {
			if srcSet[e.Key] {
				return nil
			}
			if o.dryRun {
				fmt.Fprintf(sh.stdout, "%s %s\n", ui.Warn.Sprint("would remove"), joinRemote(dst.Alias, dst.Bucket, e.Key))
				return nil
			}
			if err := c.RemoveObject(ctx, dst.Bucket, e.Key); err != nil {
				return err
			}
			stats.inc(&stats.removed)
			return nil
		})
		if err != nil {
			record(err)
		}
	}
	if firstErr != nil {
		return firstErr
	}
	printMirrorSummary(sh, stats, "uploaded")
	return nil
}

// mirrorRemoteLocal downloads a remote prefix into a local directory.
func mirrorRemoteLocal(ctx context.Context, sh *Shell, src Path, localDir string, o mirrorOpts) error {
	if src.IsRoot() || src.IsAlias() || src.Bucket == "" {
		return fmt.Errorf("mirror: source must include a bucket")
	}
	items, err := enumerateRemote(ctx, sh, src)
	if err != nil {
		return err
	}
	c, err := sh.clientFor(src)
	if err != nil {
		return err
	}

	srcSet := map[string]bool{}
	var work []mirrorItem
	for _, it := range items {
		if !matchFilters(filepath.Base(it.rel), it.rel, o.includes, o.excludes) {
			continue
		}
		srcSet[it.rel] = true
		work = append(work, it)
	}

	if !o.dryRun {
		if err := os.MkdirAll(localDir, 0o755); err != nil {
			return err
		}
	}

	stats := &mirrorStats{}
	var firstErr error
	var errMu sync.Mutex
	record := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
		stats.inc(&stats.failed)
	}

	prog := ui.NewProgress()
	parallel(o.parallel, len(work), func(i int) {
		it := work[i]
		local := filepath.Join(localDir, filepath.FromSlash(it.rel))
		if !o.overwrite {
			if fi, err := os.Stat(local); err == nil && fi.Size() == it.size {
				stats.inc(&stats.skipped)
				return
			}
		}
		if o.dryRun {
			fmt.Fprintf(sh.stdout, "%s %s -> %s\n", ui.Warn.Sprint("would download"), joinRemote(src.Alias, src.Bucket, it.key), local)
			return
		}
		bar := prog.AddBar(it.rel, it.size, 0)
		if _, err := c.DownloadFile(ctx, src.Bucket, it.key, local, false, func(d int64) { bar.IncrBy(int(d)) }); err != nil {
			bar.Abort(true)
			record(fmt.Errorf("%s: %w", it.key, err))
			return
		}
		bar.SetTotal(it.size, true)
		stats.inc(&stats.copied)
	})
	prog.Wait()

	if o.remove {
		_ = filepath.Walk(localDir, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(localDir, path)
			if err != nil {
				return nil
			}
			if srcSet[filepath.ToSlash(rel)] {
				return nil
			}
			if o.dryRun {
				fmt.Fprintf(sh.stdout, "%s %s\n", ui.Warn.Sprint("would remove"), path)
				return nil
			}
			if err := os.Remove(path); err != nil {
				record(err)
				return nil
			}
			stats.inc(&stats.removed)
			return nil
		})
	}
	if firstErr != nil {
		return firstErr
	}
	printMirrorSummary(sh, stats, "downloaded")
	return nil
}

// mirrorRemoteRemote syncs one remote prefix into another.
func mirrorRemoteRemote(ctx context.Context, sh *Shell, src, dst Path, o mirrorOpts) error {
	if src.IsRoot() || src.IsAlias() || src.Bucket == "" {
		return fmt.Errorf("mirror: source must include a bucket")
	}
	if dst.IsRoot() || dst.IsAlias() || dst.Bucket == "" {
		return fmt.Errorf("mirror: target must include a bucket")
	}
	items, err := enumerateRemote(ctx, sh, src)
	if err != nil {
		return err
	}
	srcC, err := sh.clientFor(src)
	if err != nil {
		return err
	}
	dstC, err := sh.client(dst.Alias)
	if err != nil {
		return err
	}

	srcSet := map[string]bool{}
	var work []mirrorItem
	for _, it := range items {
		if !matchFilters(filepath.Base(it.rel), it.rel, o.includes, o.excludes) {
			continue
		}
		it.dstKey = joinKey(dst.Key, it.rel)
		srcSet[it.dstKey] = true
		work = append(work, it)
	}

	stats := &mirrorStats{}
	var firstErr error
	var errMu sync.Mutex
	record := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
		stats.inc(&stats.failed)
	}

	prog := ui.NewProgress()
	parallel(o.parallel, len(work), func(i int) {
		it := work[i]
		if !o.overwrite {
			if e, err := dstC.Stat(ctx, dst.Bucket, it.dstKey); err == nil && e.Size == it.size {
				stats.inc(&stats.skipped)
				return
			}
		}
		if o.dryRun {
			fmt.Fprintf(sh.stdout, "%s %s -> %s\n", ui.Warn.Sprint("would copy"), joinRemote(src.Alias, src.Bucket, it.key), joinRemote(dst.Alias, dst.Bucket, it.dstKey))
			return
		}
		if src.Alias == dst.Alias {
			if err := srcC.Copy(ctx, src.Bucket, it.key, dst.Bucket, it.dstKey); err != nil {
				record(fmt.Errorf("%s: %w", it.key, err))
				return
			}
			stats.inc(&stats.copied)
			return
		}
		// Cross alias: stream through a temporary local file.
		tmp, err := os.CreateTemp("", "silo-mirror-*")
		if err != nil {
			record(err)
			return
		}
		tmpName := tmp.Name()
		tmp.Close()
		defer os.Remove(tmpName)
		bar := prog.AddBar(it.rel, it.size*2, 0)
		if _, err := srcC.DownloadFile(ctx, src.Bucket, it.key, tmpName, false, func(d int64) { bar.IncrBy(int(d)) }); err != nil {
			bar.Abort(true)
			record(fmt.Errorf("%s: %w", it.key, err))
			return
		}
		if err := dstC.UploadFile(ctx, dst.Bucket, it.dstKey, tmpName, s3fs.UploadOptions{
			PartSize:    sh.partSize,
			Concurrency: sh.concurrency,
			Progress:    func(d int64) { bar.IncrBy(int(d)) },
		}); err != nil {
			bar.Abort(true)
			record(fmt.Errorf("%s: %w", it.key, err))
			return
		}
		bar.SetTotal(it.size*2, true)
		stats.inc(&stats.copied)
	})
	prog.Wait()

	if o.remove {
		err := dstC.ListRecursive(ctx, dst.Bucket, dst.Key, func(e s3fs.Entry) error {
			if srcSet[e.Key] {
				return nil
			}
			if o.dryRun {
				fmt.Fprintf(sh.stdout, "%s %s\n", ui.Warn.Sprint("would remove"), joinRemote(dst.Alias, dst.Bucket, e.Key))
				return nil
			}
			if err := dstC.RemoveObject(ctx, dst.Bucket, e.Key); err != nil {
				return err
			}
			stats.inc(&stats.removed)
			return nil
		})
		if err != nil {
			record(err)
		}
	}
	if firstErr != nil {
		return firstErr
	}
	printMirrorSummary(sh, stats, "copied")
	return nil
}

func printMirrorSummary(sh *Shell, stats *mirrorStats, verb string) {
	stats.mu.Lock()
	defer stats.mu.Unlock()
	fmt.Fprintf(sh.stdout, "%s: %s %d, skipped %d, removed %d",
		ui.OK.Sprint("mirror"), verb, stats.copied, stats.skipped, stats.removed)
	if stats.failed > 0 {
		fmt.Fprintf(sh.stdout, ", %s %d", ui.Error.Sprint("failed"), stats.failed)
	}
	fmt.Fprintln(sh.stdout)
}
