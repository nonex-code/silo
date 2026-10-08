package shell

import (
	"context"
	"fmt"
	"strings"

	"github.com/nonex-code/silo/internal/ui"
)

func init() {
	register(&command{
		name:    "buckets",
		usage:   "buckets [-a] [alias]",
		summary: "list buckets of an alias (-a lists every alias)",
		group:   "buckets",
		run:     cmdBuckets,
	})
	register(&command{
		name:    "mb",
		usage:   "mb <bucket...>",
		summary: "make one or more buckets",
		group:   "buckets",
		run:     cmdMb,
	})
	register(&command{
		name:    "rb",
		usage:   "rb [-f] <bucket...>",
		summary: "remove buckets (-f empties them first)",
		group:   "buckets",
		run:     cmdRb,
	})
}

// bucketArg resolves a bucket argument, which may be relative to the current
// alias or given as /alias/bucket.
func (sh *Shell) bucketArg(arg string) (string, string, error) {
	p := resolvePath(sh.cwd, arg)
	switch {
	case p.Alias == "":
		return "", "", fmt.Errorf("no alias selected; use /<alias>/<bucket>")
	case p.Bucket == "":
		return "", "", fmt.Errorf("%s: not a bucket", arg)
	}
	return p.Alias, p.Bucket, nil
}

func cmdBuckets(ctx context.Context, sh *Shell, args []string) error {
	pa, err := parseArgs(args, []flagDef{{Names: []string{"a", "all"}}})
	if err != nil {
		return err
	}
	pos := pa.Positional()
	if pa.Has("all") {
		for _, n := range sh.cfg.Names() {
			fmt.Fprintf(sh.stdout, "%s:\n", ui.Alias.Sprint(n))
			c, err := sh.client(n)
			if err != nil {
				sh.printError(err)
				continue
			}
			buckets, err := c.ListBuckets(ctx)
			if err != nil {
				sh.printError(fmt.Errorf("%s: %w", n, err))
				continue
			}
			for _, b := range buckets {
				fmt.Fprintf(sh.stdout, "  %s\n", ui.Bucket.Sprint(b.Name))
			}
		}
		return nil
	}
	alias := ""
	if len(pos) > 0 {
		alias = pos[0]
	} else {
		alias = sh.cwd.Alias
	}
	if alias == "" {
		// List all aliases when at the root.
		for _, n := range sh.cfg.Names() {
			fmt.Fprintln(sh.stdout, ui.Alias.Sprint(n))
		}
		return nil
	}
	c, err := sh.client(alias)
	if err != nil {
		return err
	}
	buckets, err := c.ListBuckets(ctx)
	if err != nil {
		return err
	}
	for _, b := range buckets {
		fmt.Fprintln(sh.stdout, ui.Bucket.Sprint(b.Name))
	}
	return nil
}

func cmdMb(ctx context.Context, sh *Shell, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mb <bucket...>")
	}
	for _, arg := range args {
		alias, bucket, err := sh.bucketArg(arg)
		if err != nil {
			sh.printError(err)
			continue
		}
		c, err := sh.client(alias)
		if err != nil {
			sh.printError(err)
			continue
		}
		if err := c.MakeBucket(ctx, bucket); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "exist") {
				sh.warn("%s/%s already exists", alias, bucket)
				continue
			}
			sh.printError(fmt.Errorf("%s/%s: %w", alias, bucket, err))
			continue
		}
		fmt.Fprintf(sh.stdout, "%s %s/%s\n", ui.OK.Sprint("created bucket"), alias, bucket)
	}
	return nil
}

func cmdRb(ctx context.Context, sh *Shell, args []string) error {
	pa, err := parseArgs(args, []flagDef{{Names: []string{"f", "force"}}})
	if err != nil {
		return err
	}
	pos := pa.Positional()
	if len(pos) == 0 {
		return fmt.Errorf("usage: rb [-f] <bucket...>")
	}
	for _, arg := range pos {
		alias, bucket, err := sh.bucketArg(arg)
		if err != nil {
			sh.printError(err)
			continue
		}
		c, err := sh.client(alias)
		if err != nil {
			sh.printError(err)
			continue
		}
		if pa.Has("force") {
			if err := removePrefix(ctx, sh, c, bucket, ""); err != nil {
				sh.printError(err)
				continue
			}
		}
		if err := c.RemoveBucket(ctx, bucket); err != nil {
			sh.printError(fmt.Errorf("%s/%s: %w", alias, bucket, err))
			continue
		}
		fmt.Fprintf(sh.stdout, "%s %s/%s\n", ui.OK.Sprint("removed bucket"), alias, bucket)
	}
	return nil
}
