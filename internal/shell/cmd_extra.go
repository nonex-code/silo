package shell

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"strings"

	"github.com/nonex-code/silo/internal/ui"
)

func init() {
	register(&command{
		name:    "which",
		usage:   "which <path>",
		summary: "resolve a path and show whether it is an object or a directory",
		group:   "objects",
		run:     cmdWhich,
	})

	register(&command{
		name:    "hash",
		usage:   "hash [--type md5|sha1|sha256] <file...>",
		summary: "compute a checksum of remote objects",
		group:   "objects",
		run: func(ctx context.Context, sh *Shell, args []string) error {
			return cmdHash(ctx, sh, args, "")
		},
	})
	register(&command{
		name:    "md5",
		usage:   "md5 <file...>",
		summary: "compute the MD5 checksum of objects",
		group:   "objects",
		run: func(ctx context.Context, sh *Shell, args []string) error {
			return cmdHash(ctx, sh, args, "md5")
		},
	})
	register(&command{
		name:    "sha1",
		usage:   "sha1 <file...>",
		summary: "compute the SHA-1 checksum of objects",
		group:   "objects",
		run: func(ctx context.Context, sh *Shell, args []string) error {
			return cmdHash(ctx, sh, args, "sha1")
		},
	})
	register(&command{
		name:    "sha256",
		usage:   "sha256 <file...>",
		summary: "compute the SHA-256 checksum of objects",
		group:   "objects",
		run: func(ctx context.Context, sh *Shell, args []string) error {
			return cmdHash(ctx, sh, args, "sha256")
		},
	})
}

func cmdWhich(ctx context.Context, sh *Shell, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: which <path>")
	}
	for _, arg := range args {
		p := resolvePath(sh.cwd, arg)
		entry, err := sh.statPath(ctx, p)
		if err != nil {
			sh.printError(fmt.Errorf("%s: %w", p, err))
			continue
		}
		kind := "object"
		switch {
		case entry.IsAlias:
			kind = "alias"
		case entry.IsBucket:
			kind = "bucket"
		case entry.IsDir:
			kind = "directory"
		}
		fmt.Fprintf(sh.stdout, "%s\t%s\n", p.String(), ui.Info.Sprint(kind))
	}
	return nil
}

func cmdHash(ctx context.Context, sh *Shell, args []string, force string) error {
	var defs []flagDef
	if force == "" {
		defs = []flagDef{{Names: []string{"type", "t"}, Value: true}}
	}
	pa, err := parseArgs(args, defs)
	if err != nil {
		return err
	}
	algo := force
	if algo == "" {
		algo = pa.Get("type")
	}
	if algo == "" {
		algo = "md5"
	}
	newHash, err := hasherFor(algo)
	if err != nil {
		return err
	}
	files := pa.Positional()
	if len(files) == 0 {
		return fmt.Errorf("usage: hash [--type %s] <file...>", algo)
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
		h := newHash()
		_, err = io.Copy(h, rc)
		rc.Close()
		if err != nil {
			sh.printError(fmt.Errorf("%s: %w", p, err))
			continue
		}
		fmt.Fprintf(sh.stdout, "%s  %s\n", hex.EncodeToString(h.Sum(nil)), p.String())
	}
	return nil
}

func hasherFor(algo string) (func() hash.Hash, error) {
	switch strings.ToLower(algo) {
	case "md5":
		return md5.New, nil
	case "sha1":
		return sha1.New, nil
	case "sha256":
		return sha256.New, nil
	default:
		return nil, fmt.Errorf("unsupported hash type %q (use md5, sha1 or sha256)", algo)
	}
}
