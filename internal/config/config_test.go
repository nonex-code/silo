package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadSavePreservesUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original := `{
  "version": "10",
  "aliases": {
    "play": {"url": "https://play.min.io", "accessKey": "ak", "secretKey": "sk", "api": "S3v4", "path": "auto"}
  },
  "futureField": {"nested": [1, 2, 3]}
}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != "10" {
		t.Fatalf("version = %q", c.Version)
	}
	a, ok := c.Get("play")
	if !ok || a.URL != "https://play.min.io" || a.API != "S3v4" {
		t.Fatalf("alias = %+v ok=%v", a, ok)
	}

	c.SetAlias("new", Alias{URL: "http://localhost:9000", AccessKey: "x", SecretKey: "y", API: "S3v4", Path: "on"})
	if !c.RemoveAlias("play") {
		t.Fatal("RemoveAlias returned false")
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	// File permissions must match mc (0600). Windows does not model POSIX
	// permissions, so only check where they are meaningful.
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Fatalf("permissions = %o, want 600", perm)
		}
	}

	raw := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["futureField"]; !ok {
		t.Fatalf("unknown top-level key was dropped: %s", data)
	}

	reloaded, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Get("play"); ok {
		t.Fatal("removed alias still present")
	}
	if _, ok := reloaded.Get("new"); !ok {
		t.Fatal("added alias missing")
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Aliases) != 0 {
		t.Fatalf("expected no aliases, got %v", c.Aliases)
	}
	if c.Version == "" {
		t.Fatal("expected a default version")
	}
}

func TestDefaultPathFindsMcDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MC_CONFIG_DIR", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))

	// Nothing on disk yet: fall back to the canonical location.
	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".mc", "config.json"); got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}

	// A build that keeps its config in ~/mc must be discovered.
	mcDir := filepath.Join(home, "mc")
	if err := os.MkdirAll(mcDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mcFile := filepath.Join(mcDir, "config.json")
	if err := os.WriteFile(mcFile, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != mcFile {
		t.Fatalf("DefaultPath() = %q, want %q", got, mcFile)
	}
}

func TestSavePreservesPerAliasUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original := `{
  "version": "10",
  "aliases": {
    "keep": {"url":"https://a","accessKey":"ak","secretKey":"sk","api":"S3v4","path":"auto","vendorExtra":{"x":1}},
    "touch": {"url":"https://b","accessKey":"ak","secretKey":"sk","api":"S3v4","path":"auto"}
  }
}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}

	// Modify one alias and add another; the untouched alias keeps its extras.
	a, _ := c.Get("touch")
	a.URL = "https://c"
	c.SetAlias("touch", a)
	c.SetAlias("brand", Alias{URL: "https://d", AccessKey: "ak", SecretKey: "sk", API: "S3v4", Path: "auto"})
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "vendorExtra") {
		t.Fatalf("per-alias unknown field dropped: %s", data)
	}

	reloaded, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := reloaded.Get("touch"); a.URL != "https://c" {
		t.Fatalf("modified alias not updated: %+v", a)
	}
	if _, ok := reloaded.Get("brand"); !ok {
		t.Fatal("added alias missing")
	}
}

func TestLoadBOMAndNumericVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"version":10,"aliases":{"x":{"url":"https://h","accessKey":"a","secretKey":"b","api":"S3v4","path":"auto"}}}`
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte(body)...)
	if err := os.WriteFile(path, withBOM, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != "10" {
		t.Fatalf("version = %q, want 10", c.Version)
	}
	if _, ok := c.Get("x"); !ok {
		t.Fatal("alias x missing after BOM-laden file")
	}
}

func TestEnvAliasesAreReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := `{"version":"10","aliases":{"shared":{"url":"https://file","accessKey":"fa","secretKey":"fs","api":"S3v4","path":"auto"}}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MC_HOST_play", "https://ak:sk@play.min.io:9000")
	t.Setenv("MC_HOST_shared", "https://envak:envsk@env.example.com")

	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	play, ok := c.Get("play")
	if !ok {
		t.Fatal("env alias play missing")
	}
	if play.URL != "https://play.min.io:9000" || play.AccessKey != "ak" || play.SecretKey != "sk" {
		t.Fatalf("env alias parsed wrong: %+v", play)
	}
	if !c.IsEnvAlias("play") {
		t.Fatal("play should be marked as env alias")
	}

	// The env alias overrides the file alias for this session.
	shared, _ := c.Get("shared")
	if shared.URL != "https://env.example.com" || !c.IsEnvAlias("shared") {
		t.Fatalf("env alias should override file alias: %+v", shared)
	}

	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "play.min.io") || strings.Contains(string(data), "env.example.com") {
		t.Fatalf("environment aliases must not be persisted: %s", data)
	}
	if !strings.Contains(string(data), "https://file") {
		t.Fatalf("on-disk alias overridden by env was lost: %s", data)
	}

	// Explicitly setting the name makes it durable again.
	c.SetAlias("play", Alias{URL: "https://mine", AccessKey: "a", SecretKey: "b", API: "S3v4", Path: "auto"})
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "https://mine") || c.IsEnvAlias("play") {
		t.Fatalf("alias set should persist and clear env flag: %s", data)
	}
}

func TestDefaultPathHonoursEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MC_CONFIG_DIR", dir)
	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "config.json"); got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}
