package shell

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWhichAndHash(t *testing.T) {
	env := newTestEnv(t)
	env.run(t, "mb t/bkt")
	content := "hello silo\n"
	local := filepath.Join(env.dir, "h.txt")
	if err := os.WriteFile(local, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	env.run(t, fmt.Sprintf("put %q t/bkt/h.txt", local))

	if got := env.run(t, "which t/bkt/h.txt"); !strings.Contains(got, "object") {
		t.Fatalf("which object = %q", got)
	}
	if got := env.run(t, "which t/bkt"); !strings.Contains(got, "bucket") {
		t.Fatalf("which bucket = %q", got)
	}

	wantMD5 := md5.Sum([]byte(content))
	if got := env.run(t, "md5 t/bkt/h.txt"); !strings.HasPrefix(got, hex.EncodeToString(wantMD5[:])) {
		t.Fatalf("md5 = %q, want prefix %s", got, hex.EncodeToString(wantMD5[:]))
	}
	wantSHA := sha256.Sum256([]byte(content))
	if got := env.run(t, "hash -t sha256 t/bkt/h.txt"); !strings.HasPrefix(got, hex.EncodeToString(wantSHA[:])) {
		t.Fatalf("sha256 = %q, want prefix %s", got, hex.EncodeToString(wantSHA[:]))
	}
}

func TestGrep(t *testing.T) {
	env := newTestEnv(t)
	env.run(t, "mb t/grp")
	content := "alpha\nbeta\ngamma\n"
	local := filepath.Join(env.dir, "g.txt")
	if err := os.WriteFile(local, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	env.run(t, fmt.Sprintf("put %q t/grp/g.txt", local))

	if got := env.run(t, "grep -n beta t/grp/g.txt"); !strings.Contains(got, "2:beta") {
		t.Fatalf("grep -n = %q, want 2:beta", got)
	}
	if got := env.run(t, "grep -i BETA t/grp/g.txt"); !strings.Contains(got, "beta") {
		t.Fatalf("grep -i = %q", got)
	}
	if got := env.run(t, "grep -c a t/grp/g.txt"); !strings.Contains(got, ":3") {
		t.Fatalf("grep -c = %q, want count 3", got)
	}
	if got := env.run(t, "grep -l beta t/grp"); !strings.Contains(got, "g.txt") {
		t.Fatalf("grep -l recursive = %q", got)
	}
	// With -v every non-matching line is printed.
	if got := env.run(t, "grep -c -v beta t/grp/g.txt"); !strings.Contains(got, ":2") {
		t.Fatalf("grep -v -c = %q, want 2", got)
	}
}

func TestLongListingFormat(t *testing.T) {
	env := newTestEnv(t)
	env.run(t, "mb t/bkt")
	local := filepath.Join(env.dir, "f.txt")
	os.WriteFile(local, []byte("hello"), 0o644)
	env.run(t, fmt.Sprintf("put %q t/bkt/f.txt", local))

	got := env.run(t, "ll t/bkt")
	for _, want := range []string{"MODE", "SIZE", "MODIFIED", "ETAG", "CLASS", "NAME", "f.txt"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ll output missing %q:\n%s", want, got)
		}
	}
	// ll must be one entry per line (plus the summary/header lines).
	if lines := strings.Count(strings.TrimRight(got, "\n"), "\n") + 1; lines != 3 {
		t.Fatalf("ll printed %d lines, want 3:\n%s", lines, got)
	}
}

func TestHiddenEntries(t *testing.T) {
	env := newTestEnv(t)
	env.run(t, "mb t/bkt")
	env.run(t, "touch t/bkt/.secret t/bkt/visible")

	if got := env.run(t, "ls t/bkt"); strings.Contains(got, ".secret") {
		t.Fatalf("ls showed hidden entry: %q", got)
	}
	if got := env.run(t, "ls -a t/bkt"); !strings.Contains(got, ".secret") {
		t.Fatalf("ls -a hid .secret: %q", got)
	}
}

func TestRmdir(t *testing.T) {
	env := newTestEnv(t)
	env.run(t, "mb t/rgr")
	env.run(t, "mkdir -p t/rgr/a/b/c")

	local := filepath.Join(env.dir, "keep.txt")
	os.WriteFile(local, []byte("keep"), 0o644)
	env.run(t, fmt.Sprintf("put %q t/rgr/a/keep.txt", local))

	// Refuses to remove a non-empty prefix.
	env.run(t, "rmdir t/rgr/a")
	if !strings.Contains(env.err.String(), "not empty") {
		t.Fatalf("rmdir non-empty stderr = %q", env.err.String())
	}
	if got := env.run(t, "cat t/rgr/a/keep.txt"); string(got) != "keep" {
		t.Fatalf("rmdir removed data: %q", got)
	}

	// After deleting the object, -p removes the whole empty chain.
	env.run(t, "rm t/rgr/a/keep.txt")
	env.run(t, "rmdir -p t/rgr/a/b/c")
	if got := env.run(t, "ls t/rgr"); strings.Contains(got, "a") {
		t.Fatalf("rmdir -p left prefixes behind: %q", got)
	}
}

func TestRecursiveRemovalClearsMarkers(t *testing.T) {
	env := newTestEnv(t)
	env.run(t, "mb t/mir")
	env.run(t, "mkdir -p t/mir/deep/nested")
	local := filepath.Join(env.dir, "x.txt")
	os.WriteFile(local, []byte("x"), 0o644)
	env.run(t, fmt.Sprintf("put %q t/mir/deep/nested/x.txt", local))

	env.run(t, "rm -r t/mir/deep")
	if got := env.run(t, "ls t/mir"); strings.Contains(got, "deep") {
		t.Fatalf("rm -r left directory markers: %q", got)
	}
}

func TestHistoryClear(t *testing.T) {
	env := newTestEnv(t)
	env.sh.history = []string{"ls", "pwd"}
	env.sh.historyFile = filepath.Join(t.TempDir(), "hist")
	os.WriteFile(env.sh.historyFile, []byte("ls\npwd\n"), 0o600)
	if got := env.run(t, "history"); !strings.Contains(got, "pwd") {
		t.Fatalf("history = %q", got)
	}
	env.run(t, "history -c")
	if len(env.sh.history) != 0 {
		t.Fatalf("history not cleared: %v", env.sh.history)
	}
	if data, err := os.ReadFile(env.sh.historyFile); err != nil || len(data) != 0 {
		t.Fatalf("history file not cleared: %q err=%v", data, err)
	}
}

func TestMirrorLocalRemote(t *testing.T) {
	env := newTestEnv(t)
	env.run(t, "mb t/mir")

	src := filepath.Join(env.dir, "srcdir")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("aaa"), 0o644)
	os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("bbb"), 0o644)

	// Dry run must not create anything.
	env.run(t, fmt.Sprintf("mirror -n %q t/mir/dst/", src))
	if err := env.sh.Eval(context.Background(), "stat t/mir/dst/a.txt"); err == nil {
		t.Fatalf("dry-run mirror uploaded an object")
	}

	env.run(t, fmt.Sprintf("mirror %q t/mir/dst/", src))
	if got := env.run(t, "cat t/mir/dst/a.txt"); got != "aaa" {
		t.Fatalf("mirror a.txt = %q", got)
	}
	if got := env.run(t, "cat t/mir/dst/sub/b.txt"); got != "bbb" {
		t.Fatalf("mirror sub/b.txt = %q", got)
	}

	// A second run skips the unchanged files.
	if got := env.run(t, fmt.Sprintf("mirror %q t/mir/dst/", src)); !strings.Contains(got, "skipped 2") {
		t.Fatalf("second mirror = %q, want skipped 2", got)
	}

	// --remove deletes remote-only objects.
	env.run(t, "touch t/mir/dst/extra.txt")
	env.run(t, fmt.Sprintf("mirror --remove %q t/mir/dst/", src))
	if err := env.sh.Eval(context.Background(), "stat t/mir/dst/extra.txt"); err == nil {
		t.Fatalf("mirror --remove left extra.txt behind")
	}
}

func TestMirrorRemoteLocal(t *testing.T) {
	env := newTestEnv(t)
	env.run(t, "mb t/mir")
	env.run(t, "mkdir -p t/mir/src/nested")
	local := filepath.Join(env.dir, "up.txt")
	os.WriteFile(local, []byte("payload"), 0o644)
	env.run(t, fmt.Sprintf("put %q t/mir/src/up.txt", local))
	env.run(t, "mkdir t/mir/src/nested")

	dest := filepath.Join(env.dir, "download")
	env.run(t, fmt.Sprintf("mirror t/mir/src/ %q", dest))

	got, err := os.ReadFile(filepath.Join(dest, "up.txt"))
	if err != nil || string(got) != "payload" {
		t.Fatalf("mirror download = %q, err %v", got, err)
	}
}

func TestBucketCommandsAndInfo(t *testing.T) {
	env := newTestEnv(t)
	env.run(t, "mb t/one t/two")
	if got := env.run(t, "buckets t"); !strings.Contains(got, "one") || !strings.Contains(got, "two") {
		t.Fatalf("buckets = %q", got)
	}
	if got := env.run(t, "buckets -a"); !strings.Contains(got, "t:") || !strings.Contains(got, "u:") {
		t.Fatalf("buckets -a = %q", got)
	}
	if got := env.run(t, "info"); !strings.Contains(got, "version:") || !strings.Contains(got, "concurrency:") {
		t.Fatalf("info = %q", got)
	}
	env.run(t, "mb t/one")
	if !strings.Contains(env.err.String(), "already exists") {
		t.Fatalf("mb existing stderr = %q", env.err.String())
	}
	local := filepath.Join(env.dir, "z.txt")
	os.WriteFile(local, []byte("z"), 0o644)
	env.run(t, fmt.Sprintf("put %q t/one/z.txt", local))
	env.run(t, "rb -f t/one")
	if got := env.run(t, "buckets t"); strings.Contains(got, "one") {
		t.Fatalf("rb -f left bucket: %q", got)
	}
}

func TestCompleterFlags(t *testing.T) {
	env := newTestEnv(t)
	c := &completer{sh: env.sh}

	suffixes, _ := c.Do([]rune("put -"), 5)
	if len(suffixes) == 0 {
		t.Fatalf("put flag completion returned nothing")
	}
	seen := map[string]bool{}
	for _, s := range suffixes {
		seen[string(s)] = true
	}
	if !seen["r"] || !seen["-parallel"] {
		t.Fatalf("put flag suffixes = %v", suffixes)
	}

	suffixes, _ = c.Do([]rune("grep -"), 6)
	if len(suffixes) == 0 {
		t.Fatalf("grep flag completion returned nothing")
	}
}

func TestLongRecursiveNoDoubleSlash(t *testing.T) {
	env := newTestEnv(t)
	env.run(t, "mkdir t/grp/logs")
	env.run(t, "touch t/grp/logs/a.txt")
	got := env.run(t, "ll -R t/grp")
	if strings.Contains(got, "//") {
		t.Fatalf("ll -R contains double slash:\n%s", got)
	}
	if !strings.Contains(got, "logs/") {
		t.Fatalf("ll -R missing dir entry:\n%s", got)
	}
}

func TestDiff(t *testing.T) {
	env := newTestEnv(t)
	// Remote tree: a.txt(1), b.txt(2).
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("yy"), 0o644)
	env.run(t, "mb t/dif")
	env.run(t, fmt.Sprintf("put %q t/dif/a.txt", filepath.Join(dir, "a.txt")))
	env.run(t, fmt.Sprintf("put %q t/dif/b.txt", filepath.Join(dir, "b.txt")))

	// Local tree: a.txt(1) matches, b.txt(3) differs, c.txt(1) only local.
	local := t.TempDir()
	os.WriteFile(filepath.Join(local, "a.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(local, "b.txt"), []byte("zzz"), 0o644)
	os.WriteFile(filepath.Join(local, "c.txt"), []byte("c"), 0o644)

	got := env.run(t, fmt.Sprintf("diff %q t/dif/", local))
	if !strings.Contains(got, "! size differs: b.txt") {
		t.Fatalf("diff missing size change:\n%s", got)
	}
	if !strings.Contains(got, "< only in source: c.txt") {
		t.Fatalf("diff missing only-in-source:\n%s", got)
	}
	if !strings.Contains(got, "1 difference") && !strings.Contains(got, "2 difference") {
		t.Fatalf("diff summary:\n%s", got)
	}

	got = env.run(t, fmt.Sprintf("diff %q %q", local, local))
	if !strings.Contains(got, "no differences") {
		t.Fatalf("diff self = %q", got)
	}
}

func TestPutNoClobberSummary(t *testing.T) {
	env := newTestEnv(t)
	env.run(t, "mb t/clb")
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	os.WriteFile(f, []byte("hello"), 0o644)
	env.run(t, fmt.Sprintf("put %q t/clb/a.txt", f))
	got := env.run(t, fmt.Sprintf("put -n %q t/clb/a.txt", f))
	if !strings.Contains(got, "skipped") {
		t.Fatalf("put -n summary = %q", got)
	}
	if !strings.Contains(got, "uploaded 0 file(s)") {
		t.Fatalf("put -n summary = %q", got)
	}
}
