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
		name:    "put",
		usage:   "put [-r] [-c] [-n] [--parallel N] <local...> [remote]",
		summary: "upload local files/directories",
		group:   "transfer",
		run:     cmdPut,
	})
	register(&command{
		name:    "get",
		usage:   "get [-r] [-c] [-n] [--parallel N] <remote...> [local]",
		summary: "download objects/prefixes",
		group:   "transfer",
		run:     cmdGet,
	})
	register(&command{
		name:    "cp",
		usage:   "cp [-r] [--parallel N] <source...> <target>",
		summary: "copy objects (server side within an alias, streamed across aliases)",
		group:   "transfer",
		run:     cmdCp,
	})
	register(&command{
		name:    "mv",
		usage:   "mv [-r] [--parallel N] <source...> <target>",
		summary: "move objects (copy then delete)",
		group:   "transfer",
		run:     cmdMv,
	})
}

// transferFlags are shared by put/get/cp/mv.
func transferFlags() []flagDef {
	return []flagDef{
		{Names: []string{"r", "recursive"}},
		{Names: []string{"c", "continue", "resume"}},
		{Names: []string{"n", "no-clobber"}},
		{Names: []string{"parallel", "p"}, Value: true},
	}
}

func parallelFlag(pa *parsedArgs, def int) int {
	if v := pa.Get("parallel"); v != "" {
		n := 0
		fmt.Sscanf(v, "%d", &n)
		if n > 0 {
			return n
		}
	}
	return def
}

// splitSourceDest returns the source arguments and the destination argument.
// With a single argument the destination is "" (meaning the current directory).
func splitSourceDest(pos []string) (sources []string, dest string) {
	if len(pos) <= 1 {
		return pos, ""
	}
	return pos[:len(pos)-1], pos[len(pos)-1]
}

// destInfo describes how a remote destination argument should be interpreted.
type destInfo struct {
	dir  bool   // treat destination as a directory/prefix
	path Path   // resolved destination
	key  string // explicit object key when !dir
}

func (sh *Shell) resolveDest(ctx context.Context, raw string, single bool) (destInfo, error) {
	if raw == "" {
		raw = "."
	}
	p := resolvePath(sh.cwd, raw)
	if p.IsRoot() || p.IsAlias() {
		return destInfo{}, fmt.Errorf("%s: destination must include a bucket", raw)
	}
	if p.Bucket == "" {
		return destInfo{}, fmt.Errorf("%s: destination must include a bucket", raw)
	}
	if p.Key == "" {
		return destInfo{dir: true, path: p}, nil
	}
	if strings.HasSuffix(raw, "/") {
		p.Key = strings.TrimSuffix(p.Key, "/")
		return destInfo{dir: true, path: p}, nil
	}
	// If it already exists as a directory/prefix, treat it as one.
	if entry, err := sh.statPath(ctx, p); err == nil && entry.IsDir {
		return destInfo{dir: true, path: p}, nil
	}
	if !single {
		return destInfo{dir: true, path: p}, nil
	}
	return destInfo{dir: false, path: p, key: p.Key}, nil
}

// localFile is a file on the local disk queued for upload.
type localFile struct {
	abs string
	rel string
}

func collectLocal(srcs []string, recursive bool) ([]localFile, error) {
	var out []localFile
	seen := map[string]bool{}
	for _, s := range srcs {
		matches, err := filepath.Glob(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s, err)
		}
		if len(matches) == 0 {
			matches = []string{s}
		}
		for _, m := range matches {
			fi, err := os.Stat(m)
			if err != nil {
				return nil, err
			}
			if fi.IsDir() {
				if !recursive {
					return nil, fmt.Errorf("%s is a directory (use -r)", m)
				}
				base := filepath.Base(filepath.Clean(m))
				err := filepath.Walk(m, func(path string, info os.FileInfo, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}
					if info.IsDir() {
						return nil
					}
					if seen[path] {
						return nil
					}
					seen[path] = true
					rel, _ := filepath.Rel(m, path)
					out = append(out, localFile{abs: path, rel: filepath.ToSlash(filepath.Join(base, rel))})
					return nil
				})
				if err != nil {
					return nil, err
				}
			} else {
				if seen[m] {
					continue
				}
				seen[m] = true
				out = append(out, localFile{abs: m, rel: filepath.Base(m)})
			}
		}
	}
	return out, nil
}

// remoteFile is an object queued for download/copy.
type remoteFile struct {
	path Path
	rel  string
}

func (sh *Shell) collectRemote(ctx context.Context, srcs []string, recursive bool) ([]remoteFile, error) {
	var out []remoteFile
	for _, src := range srcs {
		var paths []Path
		if hasGlob(src) {
			g, err := sh.globS3(ctx, src)
			if err != nil {
				return nil, err
			}
			paths = g
		} else {
			paths = []Path{resolvePath(sh.cwd, src)}
		}
		for _, p := range paths {
			if p.IsRoot() || p.IsAlias() {
				return nil, fmt.Errorf("%s: not an object", p)
			}
			entry, err := sh.statPath(ctx, p)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", p, err)
			}
			if !entry.IsDir {
				out = append(out, remoteFile{path: p, rel: p.Base()})
				continue
			}
			if !recursive {
				return nil, fmt.Errorf("%s is a directory (use -r)", p)
			}
			c, err := sh.clientFor(p)
			if err != nil {
				return nil, err
			}
			if p.Key == "" {
				err = c.ListRecursive(ctx, p.Bucket, "", func(e s3fs.Entry) error {
					out = append(out, remoteFile{path: Path{Alias: p.Alias, Bucket: p.Bucket, Key: e.Key}, rel: e.Key})
					return nil
				})
			} else {
				base := p.Base()
				prefix := strings.TrimSuffix(p.Key, "/") + "/"
				err = c.ListRecursive(ctx, p.Bucket, p.Key, func(e s3fs.Entry) error {
					rel := strings.TrimPrefix(e.Key, prefix)
					out = append(out, remoteFile{path: Path{Alias: p.Alias, Bucket: p.Bucket, Key: e.Key}, rel: filepath.ToSlash(filepath.Join(base, rel))})
					return nil
				})
			}
			if err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// parallel runs fn for i in [0,total) using up to n workers.
func parallel(n, total int, fn func(i int)) {
	if total == 0 {
		return
	}
	if n <= 0 || n > total {
		n = total
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				fn(i)
			}
		}()
	}
	for i := 0; i < total; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
}

func cmdPut(ctx context.Context, sh *Shell, args []string) error {
	pa, err := parseArgs(args, transferFlags())
	if err != nil {
		return err
	}
	recursive := pa.Has("recursive")
	resume := pa.Has("continue")
	noClobber := pa.Has("no-clobber")
	pos := pa.Positional()
	if len(pos) == 0 {
		return fmt.Errorf("usage: put [-r] [-c] [-n] <local...> [remote]")
	}
	srcs, dstRaw := splitSourceDest(pos)
	files, err := collectLocal(srcs, recursive)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("nothing to upload")
	}
	di, err := sh.resolveDest(ctx, dstRaw, len(files) == 1)
	if err != nil {
		return err
	}
	if !di.dir && len(files) != 1 {
		return fmt.Errorf("destination %s is not a directory", dstRaw)
	}

	c, err := sh.client(di.path.Alias)
	if err != nil {
		return err
	}

	prog := ui.NewProgress()
	var errMu sync.Mutex
	var firstErr error
	var done, skipped int
	recordErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
	}

	parallel(parallelFlag(pa, sh.concurrency), len(files), func(i int) {
		f := files[i]
		key := di.key
		if di.dir {
			key = joinKey(di.path.Key, f.rel)
		}
		fi, statErr := os.Stat(f.abs)
		if statErr != nil {
			recordErr(fmt.Errorf("%s: %w", f.abs, statErr))
			return
		}
		if noClobber {
			if _, err := c.Stat(ctx, di.path.Bucket, key); err == nil {
				sh.warn("skipping existing %s", joinRemote(di.path.Alias, di.path.Bucket, key))
				errMu.Lock()
				skipped++
				errMu.Unlock()
				return
			}
		}
		bar := prog.AddBar(f.rel, fi.Size(), 0)
		err := c.UploadFile(ctx, di.path.Bucket, key, f.abs, s3fs.UploadOptions{
			PartSize:    sh.partSize,
			Concurrency: sh.concurrency,
			Resume:      resume,
			Progress:    func(d int64) { bar.IncrBy(int(d)) },
		})
		if err != nil {
			bar.Abort(true)
			recordErr(fmt.Errorf("%s: %w", f.abs, err))
			return
		}
		bar.SetTotal(fi.Size(), true)
		errMu.Lock()
		done++
		errMu.Unlock()
	})
	prog.Wait()
	if firstErr != nil {
		return firstErr
	}
	printTransferSummary(sh, "uploaded", done, skipped)
	return nil
}

func cmdGet(ctx context.Context, sh *Shell, args []string) error {
	pa, err := parseArgs(args, transferFlags())
	if err != nil {
		return err
	}
	recursive := pa.Has("recursive")
	resume := pa.Has("continue")
	noClobber := pa.Has("no-clobber")
	pos := pa.Positional()
	if len(pos) == 0 {
		return fmt.Errorf("usage: get [-r] [-c] [-n] <remote...> [local]")
	}
	srcs, dstRaw := splitSourceDest(pos)
	if dstRaw == "" {
		dstRaw = "."
	}
	files, err := sh.collectRemote(ctx, srcs, recursive)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("nothing to download")
	}
	dirDest := len(files) > 1 || strings.HasSuffix(dstRaw, "/") || isDir(dstRaw)

	prog := ui.NewProgress()
	var errMu sync.Mutex
	var firstErr error
	var done, skipped int
	recordErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
	}

	parallel(parallelFlag(pa, sh.concurrency), len(files), func(i int) {
		f := files[i]
		local := dstRaw
		if dirDest {
			local = filepath.Join(dstRaw, filepath.FromSlash(f.rel))
		}
		c, err := sh.client(f.path.Alias)
		if err != nil {
			recordErr(err)
			return
		}
		info, err := c.Stat(ctx, f.path.Bucket, f.path.Key)
		if err != nil {
			recordErr(fmt.Errorf("%s: %w", f.path, err))
			return
		}
		if noClobber {
			if fi, err := os.Stat(local); err == nil && fi.Size() == info.Size {
				sh.warn("skipping existing %s", local)
				errMu.Lock()
				skipped++
				errMu.Unlock()
				return
			}
		}
		var initial int64
		if resume {
			if fi, err := os.Stat(local); err == nil && fi.Size() < info.Size {
				initial = fi.Size()
			}
		}
		bar := prog.AddBar(f.rel, info.Size, initial)
		res, err := c.DownloadFile(ctx, f.path.Bucket, f.path.Key, local, resume, func(d int64) { bar.IncrBy(int(d)) })
		if err != nil {
			bar.Abort(true)
			recordErr(fmt.Errorf("%s: %w", f.path, err))
			return
		}
		bar.SetTotal(info.Size, true)
		if res.Skipped {
			sh.warn("skipping existing %s", local)
			errMu.Lock()
			skipped++
			errMu.Unlock()
			return
		}
		errMu.Lock()
		done++
		errMu.Unlock()
	})
	prog.Wait()
	if firstErr != nil {
		return firstErr
	}
	printTransferSummary(sh, "downloaded", done, skipped)
	return nil
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func cmdCp(ctx context.Context, sh *Shell, args []string) error {
	return cpOrMv(ctx, sh, args, false)
}

func cmdMv(ctx context.Context, sh *Shell, args []string) error {
	return cpOrMv(ctx, sh, args, true)
}

func cpOrMv(ctx context.Context, sh *Shell, args []string, move bool) error {
	name := "cp"
	if move {
		name = "mv"
	}
	pa, err := parseArgs(args, transferFlags())
	if err != nil {
		return err
	}
	recursive := pa.Has("recursive")
	pos := pa.Positional()
	if len(pos) < 2 {
		return fmt.Errorf("usage: %s [-r] <source...> <target>", name)
	}
	srcs, dstRaw := splitSourceDest(pos)
	files, err := sh.collectRemote(ctx, srcs, recursive)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("nothing to copy")
	}
	di, err := sh.resolveDest(ctx, dstRaw, len(files) == 1)
	if err != nil {
		return err
	}
	if !di.dir && len(files) != 1 {
		return fmt.Errorf("target %s is not a directory", dstRaw)
	}

	prog := ui.NewProgress()
	var errMu sync.Mutex
	var firstErr error
	var done int
	recordErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
	}

	parallel(parallelFlag(pa, sh.concurrency), len(files), func(i int) {
		f := files[i]
		dstKey := di.key
		if di.dir {
			dstKey = joinKey(di.path.Key, f.rel)
		}
		srcC, err := sh.client(f.path.Alias)
		if err != nil {
			recordErr(err)
			return
		}
		dstC, err := sh.client(di.path.Alias)
		if err != nil {
			recordErr(err)
			return
		}

		dstPath := Path{Alias: di.path.Alias, Bucket: di.path.Bucket, Key: dstKey}

		if f.path.Alias == di.path.Alias {
			// Server side copy.
			if err := srcC.Copy(ctx, f.path.Bucket, f.path.Key, di.path.Bucket, dstKey); err != nil {
				recordErr(fmt.Errorf("%s -> %s: %w", f.path, dstPath, err))
				return
			}
		} else {
			// Cross alias: stream through a temporary local file so large
			// objects get multipart + resume support.
			info, err := srcC.Stat(ctx, f.path.Bucket, f.path.Key)
			if err != nil {
				recordErr(fmt.Errorf("%s: %w", f.path, err))
				return
			}
			tmp, err := os.CreateTemp("", "silo-copy-*")
			if err != nil {
				recordErr(err)
				return
			}
			tmpName := tmp.Name()
			tmp.Close()
			defer os.Remove(tmpName)
			bar := prog.AddBar(f.rel, info.Size*2, 0)
			if _, err := srcC.DownloadFile(ctx, f.path.Bucket, f.path.Key, tmpName, false, func(d int64) { bar.IncrBy(int(d)) }); err != nil {
				bar.Abort(true)
				recordErr(fmt.Errorf("%s: %w", f.path, err))
				return
			}
			if err := dstC.UploadFile(ctx, di.path.Bucket, dstKey, tmpName, s3fs.UploadOptions{
				PartSize:    sh.partSize,
				Concurrency: sh.concurrency,
				Progress:    func(d int64) { bar.IncrBy(int(d)) },
			}); err != nil {
				bar.Abort(true)
				recordErr(fmt.Errorf("%s: %w", dstPath, err))
				return
			}
			bar.SetTotal(info.Size*2, true)
		}

		if move {
			if err := srcC.RemoveObject(ctx, f.path.Bucket, f.path.Key); err != nil {
				recordErr(fmt.Errorf("%s: remove source: %w", f.path, err))
			}
		}
		errMu.Lock()
		done++
		errMu.Unlock()
	})
	prog.Wait()
	if firstErr != nil {
		return firstErr
	}
	verb := "copied"
	if move {
		verb = "moved"
	}
	printTransferSummary(sh, verb, done, len(files)-done)
	return nil
}

// printTransferSummary reports how many objects were transferred and, when
// some were skipped (e.g. with -n), how many.
func printTransferSummary(sh *Shell, verb string, done, skipped int) {
	fmt.Fprintf(sh.stdout, "%s %d file(s)", ui.OK.Sprint(verb), done)
	if skipped > 0 {
		fmt.Fprintf(sh.stdout, ", %d skipped", skipped)
	}
	fmt.Fprintln(sh.stdout)
}

func joinRemote(alias, bucket, key string) string {
	return "/" + alias + "/" + bucket + "/" + key
}
