package shell

import "strings"

// Path is a location in the virtual filesystem. The hierarchy is:
//
//	/                 root, lists aliases
//	/<alias>          lists buckets of an alias
//	/<alias>/<bucket> lists objects/prefixes of a bucket
//	/<alias>/<bucket>/<key...>
//
// An empty Path is the root.
type Path struct {
	Alias  string
	Bucket string
	Key    string
}

// IsRoot reports whether the path is the filesystem root.
func (p Path) IsRoot() bool { return p.Alias == "" }

// IsAlias reports whether the path points at an alias namespace.
func (p Path) IsAlias() bool { return p.Alias != "" && p.Bucket == "" }

// IsBucket reports whether the path points at a bucket.
func (p Path) IsBucket() bool { return p.Bucket != "" && p.Key == "" }

// IsObject reports whether the path points at a concrete key.
func (p Path) IsObject() bool { return p.Key != "" }

// String renders the path in the canonical absolute form.
func (p Path) String() string {
	if p.Alias == "" {
		return "/"
	}
	s := "/" + p.Alias
	if p.Bucket != "" {
		s += "/" + p.Bucket
		if p.Key != "" {
			s += "/" + p.Key
		}
	}
	return s
}

// Dir returns the parent path.
func (p Path) Dir() Path {
	switch {
	case p.Key != "":
		i := strings.LastIndex(p.Key, "/")
		if i < 0 {
			p.Key = ""
		} else {
			p.Key = p.Key[:i]
		}
	case p.Bucket != "":
		p.Bucket = ""
	case p.Alias != "":
		p.Alias = ""
	}
	return p
}

// Base returns the last component of the path.
func (p Path) Base() string {
	switch {
	case p.Key != "":
		if i := strings.LastIndex(p.Key, "/"); i >= 0 {
			return p.Key[i+1:]
		}
		return p.Key
	case p.Bucket != "":
		return p.Bucket
	case p.Alias != "":
		return p.Alias
	default:
		return "/"
	}
}

// resolvePath turns a user supplied argument into an absolute Path relative to
// cwd. It understands "/", ".", ".." and a leading "~".
func resolvePath(cwd Path, arg string) Path {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return cwd
	}
	if arg == "~" {
		return Path{}
	}
	if strings.HasPrefix(arg, "~/") {
		arg = "/" + arg[2:]
	}

	base := cwd
	if strings.HasPrefix(arg, "/") {
		base = Path{}
	}

	for _, seg := range strings.Split(arg, "/") {
		switch seg {
		case "", ".":
			continue
		case "..":
			base = base.Dir()
			continue
		}
		switch {
		case base.Alias == "":
			base.Alias = seg
		case base.Bucket == "":
			base.Bucket = seg
		default:
			if base.Key == "" {
				base.Key = seg
			} else {
				base.Key += "/" + seg
			}
		}
	}
	return base
}

// joinObject returns a new Path pointing at key inside dir.
func (p Path) joinObject(key string) Path {
	p.Key = key
	return p
}
