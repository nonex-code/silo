package shell

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

type testEnv struct {
	sh  *Shell
	out *bytes.Buffer
	err *bytes.Buffer
	dir string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	backend := s3mem.New()
	faker := gofakes3.New(backend, gofakes3.WithAutoBucket(true))
	ts := httptest.NewServer(faker.Server())
	t.Cleanup(ts.Close)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := fmt.Sprintf(`{"version":"10","aliases":{
"t":{"url":%q,"accessKey":"a","secretKey":"b","api":"S3v4","path":"auto"},
"u":{"url":%q,"accessKey":"a","secretKey":"b","api":"S3v4","path":"auto"}}}`, ts.URL, ts.URL)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	sh, err := New(Options{ConfigPath: cfgPath, PartSize: 5 << 20, Concurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	env := &testEnv{sh: sh, out: &bytes.Buffer{}, err: &bytes.Buffer{}, dir: dir}
	sh.stdout = env.out
	sh.stderr = env.err
	return env
}

func (e *testEnv) run(t *testing.T, cmd string) string {
	t.Helper()
	e.out.Reset()
	e.err.Reset()
	if err := e.sh.Eval(context.Background(), cmd); err != nil {
		t.Fatalf("command %q failed: %v (stderr: %s)", cmd, err, e.err.String())
	}
	return e.out.String()
}

func TestVFSAndBuckets(t *testing.T) {
	env := newTestEnv(t)

	if got := env.run(t, "ls /"); !strings.Contains(got, "t") || !strings.Contains(got, "u") {
		t.Fatalf("ls / = %q, want aliases t and u", got)
	}
	env.run(t, "mb t/bucket")
	if got := env.run(t, "ls /t"); !strings.Contains(got, "bucket") {
		t.Fatalf("ls /t = %q, want bucket", got)
	}
	if got := env.run(t, "cd /t/bucket"); got != "" {
		t.Fatalf("cd produced output: %q", got)
	}
	if got := env.run(t, "pwd"); !strings.Contains(got, "/t/bucket") {
		t.Fatalf("pwd = %q", got)
	}
}

func TestPutGetCatStat(t *testing.T) {
	env := newTestEnv(t)
	content := "hello silo\nsecond line\n"
	local := filepath.Join(env.dir, "hello.txt")
	if err := os.WriteFile(local, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	env.run(t, "mb t/buck1")
	env.run(t, fmt.Sprintf("put %q t/buck1/dir/", local))

	if got := env.run(t, "cat t/buck1/dir/hello.txt"); got != content {
		t.Fatalf("cat = %q, want %q", got, content)
	}
	if got := env.run(t, "stat t/buck1/dir/hello.txt"); !strings.Contains(got, "hello.txt") {
		t.Fatalf("stat = %q", got)
	}

	out := filepath.Join(env.dir, "out.txt")
	env.run(t, fmt.Sprintf("get %q %q", "t/buck1/dir/hello.txt", out))
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("downloaded %q, want %q", got, content)
	}

	// Resumable download: pre-seed the first 6 bytes.
	resume := filepath.Join(env.dir, "resume.txt")
	if err := os.WriteFile(resume, []byte(content[:6]), 0o644); err != nil {
		t.Fatal(err)
	}
	env.run(t, fmt.Sprintf("get -c %q %q", "t/buck1/dir/hello.txt", resume))
	got, err = os.ReadFile(resume)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("resumed download %q, want %q", got, content)
	}
}

func TestCrossAliasCopyMoveDelete(t *testing.T) {
	env := newTestEnv(t)
	content := "cross alias payload\n"
	local := filepath.Join(env.dir, "p.txt")
	if err := os.WriteFile(local, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	env.run(t, "mb t/buck1")
	env.run(t, "mb u/buck2")
	env.run(t, fmt.Sprintf("put %q t/buck1/p.txt", local))

	// Cross alias copy goes through a temporary local file.
	env.run(t, "cp t/buck1/p.txt u/buck2/")
	if got := env.run(t, "cat u/buck2/p.txt"); got != content {
		t.Fatalf("cross-alias copy cat = %q", got)
	}
	// Source must still exist.
	env.run(t, "stat t/buck1/p.txt")

	// Same alias move is a server side copy plus delete.
	env.run(t, "mv t/buck1/p.txt t/buck1/moved/")
	if _, err := os.Stat(local); err != nil {
		t.Fatal(err)
	}
	if got := env.run(t, "cat t/buck1/moved/p.txt"); got != content {
		t.Fatalf("same-alias move cat = %q", got)
	}
	if err := env.sh.Eval(context.Background(), "stat t/buck1/p.txt"); err == nil {
		t.Fatalf("source still present after mv")
	}

	env.run(t, "rm t/buck1/moved/p.txt")
	if err := env.sh.Eval(context.Background(), "stat t/buck1/moved/p.txt"); err == nil {
		t.Fatalf("object still present after rm")
	}
}

func TestMultipartUploadAndRecursiveList(t *testing.T) {
	env := newTestEnv(t)
	env.run(t, "mb t/big")

	// 34 MiB exceeds SinglePutLimit (32 MiB) so this exercises multipart with
	// 5 MiB parts and two concurrent workers.
	size := int64(34 << 20)
	local := filepath.Join(env.dir, "big.bin")
	f, err := os.Create(local)
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte{0x5a}, 1<<20)
	for written := int64(0); written < size; written += int64(len(chunk)) {
		if _, err := f.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()

	env.run(t, fmt.Sprintf("put %q t/big/big.bin", local))

	// The upload state file must be cleaned up on success.
	if _, err := os.Stat(local + ".silo-upload.json"); err == nil {
		t.Fatalf("upload state file was not removed")
	}

	if got := env.run(t, "find t/big"); !strings.Contains(got, "big.bin") {
		t.Fatalf("find = %q", got)
	}
	if got := env.run(t, "du t/big"); !strings.Contains(got, "34") {
		t.Fatalf("du = %q, want ~34 (MiB)", got)
	}

	out := filepath.Join(env.dir, "big.out")
	env.run(t, fmt.Sprintf("get %q %q", "t/big/big.bin", out))
	fi, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != size {
		t.Fatalf("downloaded size = %d, want %d", fi.Size(), size)
	}
}

func TestPutRecursiveDirectory(t *testing.T) {
	env := newTestEnv(t)
	env.run(t, "mb t/tree")

	src := filepath.Join(env.dir, "src")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("b"), 0o644)

	// -r must be detected for directories to be uploaded recursively.
	env.run(t, fmt.Sprintf("put -r %q t/tree/dst/", src))

	for _, key := range []string{"dst/src/a.txt", "dst/src/sub/b.txt"} {
		if got := env.run(t, "cat t/tree/"+key); got == "" {
			t.Fatalf("missing uploaded object %s", key)
		}
	}

	// rm -r removes the whole prefix.
	env.run(t, "rm -r t/tree/dst")
	if got := env.run(t, "find t/tree"); strings.Contains(got, "a.txt") {
		t.Fatalf("recursive rm left objects behind: %q", got)
	}
}

func TestMkdirAndLsNoDuplicates(t *testing.T) {
	env := newTestEnv(t)
	env.run(t, "mb t/dirs")
	env.run(t, "mkdir t/dirs/folder")

	got := env.run(t, "ls t/dirs")
	if n := strings.Count(got, "folder"); n != 1 {
		t.Fatalf("ls showed folder %d times, want 1: %q", n, got)
	}

	// A folder marker plus real objects must still appear once.
	local := filepath.Join(env.dir, "f.txt")
	os.WriteFile(local, []byte("x"), 0o644)
	env.run(t, fmt.Sprintf("put %q t/dirs/folder/f.txt", local))
	got = env.run(t, "ls t/dirs")
	if n := strings.Count(got, "folder"); n != 1 {
		t.Fatalf("ls showed folder %d times after put, want 1: %q", n, got)
	}
}

func TestListHidesCurrentDirMarker(t *testing.T) {
	env := newTestEnv(t)
	env.run(t, "mb t/deep")
	// Create nested folder markers, mimicking mc's `mc mb`/`mc cp` layout.
	env.run(t, "mkdir t/deep/parent")
	env.run(t, "mkdir t/deep/parent/child")
	local := filepath.Join(env.dir, "f.txt")
	os.WriteFile(local, []byte("x"), 0o644)
	env.run(t, fmt.Sprintf("put %q t/deep/parent/child/f.txt", local))

	// Listing the parent must show its child once and must not list itself.
	got := env.run(t, "ls t/deep/parent")
	if n := strings.Count(got, "child"); n != 1 {
		t.Fatalf("ls parent showed child %d times, want 1: %q", n, got)
	}
	if strings.Contains(got, "parent") {
		t.Fatalf("ls parent showed its own marker: %q", got)
	}

	// Same for the deeper level: only real children appear.
	got = env.run(t, "ls t/deep/parent/child")
	if n := strings.Count(got, "child"); n != 0 {
		t.Fatalf("ls child showed its own marker: %q", got)
	}
	if !strings.Contains(got, "f.txt") {
		t.Fatalf("ls child = %q, want f.txt", got)
	}

	// And the grandparent sees parent exactly once.
	got = env.run(t, "ls t/deep")
	if n := strings.Count(got, "parent"); n != 1 {
		t.Fatalf("ls deep showed parent %d times, want 1: %q", n, got)
	}
}

func TestCompleter(t *testing.T) {
	env := newTestEnv(t)
	c := &completer{sh: env.sh}

	// Complete alias names at the root.
	suffixes, off := c.Do([]rune("cd /"), 4)
	if off != 1 {
		t.Fatalf("offset = %d, want 1", off)
	}
	found := false
	for _, s := range suffixes {
		if strings.HasPrefix(string(s), "t") || strings.HasPrefix(string(s), "u") {
			found = true
		}
	}
	if !found {
		t.Fatalf("root completion = %v, want alias entries", suffixes)
	}

	// Complete command names.
	suffixes, _ = c.Do([]rune("pu"), 2)
	if len(suffixes) == 0 || string(suffixes[0]) != "t" {
		t.Fatalf("command completion = %v, want suffix t", suffixes)
	}

	env.run(t, "mb t/buck1")
	suffixes, _ = c.Do([]rune("ls t/b"), 6)
	if len(suffixes) == 0 {
		t.Fatalf("bucket completion returned nothing")
	}
}
