// Package config loads and stores the MinIO client (mc) configuration file so
// that silo is a drop-in companion for an existing mc installation.
//
// The on-disk format is the same as mc's ~/.mc/config.json:
//
//	{
//	  "version": "10",
//	  "aliases": {
//	    "play": {
//	      "url": "https://play.min.io",
//	      "accessKey": "...",
//	      "secretKey": "...",
//	      "api": "S3v4",
//	      "path": "auto"
//	    }
//	  }
//	}
//
// Compatibility rules:
//   - Unknown top-level keys are preserved when the file is rewritten.
//   - Unknown per-alias keys are preserved for aliases silo did not modify.
//   - Aliases supplied via MC_HOST_<name> environment variables are readable
//     but never written back to the file.
//   - The file is written atomically with mode 0600, like mc.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Alias mirrors a single entry under "aliases" in mc's config.json.
type Alias struct {
	URL       string `json:"url"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	API       string `json:"api"`
	Path      string `json:"path"`
	Lookup    string `json:"lookup,omitempty"`
	Region    string `json:"region,omitempty"`
}

// Config is a loaded mc configuration file.
type Config struct {
	path       string
	raw        map[string]json.RawMessage
	rawAliases map[string]json.RawMessage
	envAliases map[string]bool
	Version    string
	Aliases    map[string]Alias
}

// envAliasPrefix is the prefix mc uses for aliases defined by environment
// variables, e.g. MC_HOST_play=https://key:secret@play.min.io.
const envAliasPrefix = "MC_HOST_"

// DefaultPath returns the location of the mc configuration file. It honours
// MC_CONFIG_DIR and otherwise searches the directories used by the different
// mc builds (the official one uses ~/.mc, some builds and forks use ~/mc, and
// on Windows a few installs keep it under AppData). When no existing file is
// found, the canonical ~/.mc/config.json is returned so new aliases have a
// predictable home.
func DefaultPath() (string, error) {
	if dir := os.Getenv("MC_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "config.json"), nil
	}
	candidates, err := candidatePaths()
	if err != nil {
		return "", err
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return candidates[0], nil
}

// candidatePaths lists the config.json locations searched, in priority order.
func candidatePaths() ([]string, error) {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".mc"),
			filepath.Join(home, "mc"),
		)
	}
	if appdata := os.Getenv("APPDATA"); appdata != "" {
		dirs = append(dirs, filepath.Join(appdata, "mc"))
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		dirs = append(dirs, filepath.Join(local, "mc"))
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("cannot determine home directory")
	}
	paths := make([]string, len(dirs))
	for i, d := range dirs {
		paths[i] = filepath.Join(d, "config.json")
	}
	return paths, nil
}

// Load reads the mc configuration. A missing file yields an empty config so
// that the user can create aliases from inside silo.
func Load() (*Config, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads the mc configuration from an explicit path. Environment
// aliases (MC_HOST_*) are merged in afterwards.
func LoadFrom(path string) (*Config, error) {
	c := &Config{
		path:       path,
		raw:        map[string]json.RawMessage{},
		rawAliases: map[string]json.RawMessage{},
		envAliases: map[string]bool{},
		Version:    "10",
		Aliases:    map[string]Alias{},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.loadEnvAliases()
			return c, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	// Tolerate a UTF-8 BOM, which some Windows editors add.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(data)) == 0 {
		c.loadEnvAliases()
		return c, nil
	}
	if err := json.Unmarshal(data, &c.raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if v, ok := c.raw["version"]; ok {
		c.Version = decodeVersion(v)
	}
	if a, ok := c.raw["aliases"]; ok {
		if err := json.Unmarshal(a, &c.Aliases); err != nil {
			return nil, fmt.Errorf("parse aliases in %s: %w", path, err)
		}
		if err := json.Unmarshal(a, &c.rawAliases); err != nil {
			return nil, fmt.Errorf("parse aliases in %s: %w", path, err)
		}
	}
	if c.Aliases == nil {
		c.Aliases = map[string]Alias{}
	}
	if c.rawAliases == nil {
		c.rawAliases = map[string]json.RawMessage{}
	}

	c.loadEnvAliases()
	return c, nil
}

// decodeVersion accepts both the string ("10") and legacy numeric forms.
func decodeVersion(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return "10"
}

// loadEnvAliases merges MC_HOST_<name> environment aliases. They take
// precedence over file aliases for the current session but are not persisted.
func (c *Config) loadEnvAliases() {
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		key, val := kv[:eq], kv[eq+1:]
		if !strings.HasPrefix(key, envAliasPrefix) {
			continue
		}
		name := key[len(envAliasPrefix):]
		if name == "" {
			continue
		}
		a, ok := parseEnvAlias(val)
		if !ok {
			continue
		}
		c.Aliases[name] = a
		c.envAliases[name] = true
	}
}

// parseEnvAlias parses the value of an MC_HOST_* variable, which mc formats as
// scheme://accessKey:secretKey@host[:port].
func parseEnvAlias(raw string) (Alias, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Alias{}, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return Alias{}, false
	}
	clean := *u
	clean.User = nil
	a := Alias{URL: clean.String(), API: "S3v4", Path: "auto"}
	if u.User != nil {
		a.AccessKey = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			a.SecretKey = pw
		}
	}
	return a, true
}

// Path returns the file the config was loaded from.
func (c *Config) Path() string { return c.path }

// Names returns the sorted alias names, including environment aliases.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Aliases))
	for name := range c.Aliases {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Get returns the alias and whether it exists.
func (c *Config) Get(name string) (Alias, bool) {
	a, ok := c.Aliases[name]
	return a, ok
}

// IsEnvAlias reports whether the alias comes from an MC_HOST_* variable.
func (c *Config) IsEnvAlias(name string) bool { return c.envAliases[name] }

// SetAlias creates or replaces an alias. The alias is marked as user owned so
// that it will be persisted even if a same-named environment alias existed.
func (c *Config) SetAlias(name string, a Alias) {
	c.Aliases[name] = a
	delete(c.envAliases, name)
	if raw, err := json.Marshal(a); err == nil {
		c.rawAliases[name] = raw
	}
}

// RemoveAlias deletes an alias, returning false if it did not exist.
func (c *Config) RemoveAlias(name string) bool {
	if _, ok := c.Aliases[name]; !ok {
		return false
	}
	delete(c.Aliases, name)
	delete(c.rawAliases, name)
	delete(c.envAliases, name)
	return true
}

// Save writes the configuration back to disk preserving unknown top-level keys
// and per-alias keys of aliases silo did not modify. Environment aliases are
// never written. The file is written with mode 0600 like mc does.
func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// Start from the aliases already on disk so unknown per-alias fields
	// survive, then add any aliases created in this session.
	aliasRaw := make(map[string]json.RawMessage, len(c.Aliases))
	for name, raw := range c.rawAliases {
		aliasRaw[name] = raw
	}
	for name := range c.Aliases {
		if _, ok := aliasRaw[name]; ok {
			continue // unchanged on-disk entry (possibly overridden by env)
		}
		if c.envAliases[name] {
			continue // pure environment alias: do not persist
		}
		raw, err := json.Marshal(c.Aliases[name])
		if err != nil {
			return err
		}
		aliasRaw[name] = raw
	}
	aliases, err := json.Marshal(aliasRaw)
	if err != nil {
		return err
	}

	raw := make(map[string]json.RawMessage, len(c.raw)+2)
	for k, v := range c.raw {
		raw[k] = v
	}
	ver, err := json.Marshal(c.Version)
	if err != nil {
		return err
	}
	raw["version"] = ver
	raw["aliases"] = aliases

	out, err := json.MarshalIndent(raw, "", "\t")
	if err != nil {
		return err
	}
	out = append(out, '\n')

	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return fmt.Errorf("replace %s: %w", c.path, err)
	}
	return nil
}
