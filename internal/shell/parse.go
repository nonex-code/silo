package shell

import (
	"fmt"
	"strings"
)

// tokenize splits a command line into words, honouring single quotes, double
// quotes and backslash escapes. It intentionally does not implement pipes or
// redirections.
func tokenize(line string) ([]string, error) {
	var (
		toks     []string
		cur      strings.Builder
		inSingle bool
		inDouble bool
		escaped  bool
		started  bool
	)
	for _, r := range line {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
			started = true
		case r == '\\' && !inSingle:
			escaped = true
			started = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			started = true
		case r == '"' && !inSingle:
			inDouble = !inDouble
			started = true
		case (r == ' ' || r == '\t') && !inSingle && !inDouble:
			if started {
				toks = append(toks, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if escaped {
		return nil, fmt.Errorf("trailing backslash")
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("unterminated quote")
	}
	if started {
		toks = append(toks, cur.String())
	}
	return toks, nil
}

// flagDef describes one accepted flag. Names[0] is the canonical name; the
// remaining names are aliases (with or without leading dashes).
type flagDef struct {
	Names []string
	Value bool
}

// parsedArgs holds the result of parsing a command's tokens.
type parsedArgs struct {
	flags map[string][]string
	pos   []string
}

// Has reports whether a flag was present.
func (a *parsedArgs) Has(name string) bool {
	_, ok := a.flags[name]
	return ok
}

// Get returns the last value supplied for a flag.
func (a *parsedArgs) Get(name string) string {
	v := a.flags[name]
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

// Positional returns the non-flag arguments.
func (a *parsedArgs) Positional() []string { return a.pos }

// GetAll returns every value supplied for a repeatable flag.
func (a *parsedArgs) GetAll(name string) []string { return a.flags[name] }

type flagLookup struct {
	names []string
	value bool
}

// parseArgs parses tokens according to defs. Flags may appear anywhere and
// support "-x", "--x", "-x=y", "--x=y" and clustered short booleans "-rf".
// A flag is registered under every name (alias) listed in its definition, so
// callers may query either the short or the long form.
func parseArgs(tokens []string, defs []flagDef) (*parsedArgs, error) {
	lookup := map[string]flagLookup{}
	for _, d := range defs {
		if len(d.Names) == 0 {
			continue
		}
		names := make([]string, 0, len(d.Names))
		for _, n := range d.Names {
			names = append(names, strings.TrimLeft(n, "-"))
		}
		fl := flagLookup{names: names, value: d.Value}
		for _, n := range names {
			lookup[n] = fl
		}
	}

	res := &parsedArgs{flags: map[string][]string{}}
	afterDoubleDash := false

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if afterDoubleDash || tok == "-" || !strings.HasPrefix(tok, "-") {
			res.pos = append(res.pos, tok)
			continue
		}
		if tok == "--" {
			afterDoubleDash = true
			continue
		}

		body := strings.TrimLeft(tok, "-")
		name := body
		inlineVal := ""
		hasInline := false
		if eq := strings.IndexByte(body, '='); eq >= 0 {
			name = body[:eq]
			inlineVal = body[eq+1:]
			hasInline = true
		}

		if fl, ok := lookup[name]; ok {
			val := inlineVal
			if fl.value && !hasInline {
				if i+1 >= len(tokens) {
					return nil, fmt.Errorf("flag -%s requires a value", name)
				}
				i++
				val = tokens[i]
			}
			for _, n := range fl.names {
				res.flags[n] = append(res.flags[n], val)
			}
			continue
		}

		// Try clustered short boolean flags such as -rf.
		if !strings.HasPrefix(tok, "--") && len(name) > 1 {
			allOK := true
			for _, c := range name {
				if fl, ok := lookup[string(c)]; !ok || fl.value {
					allOK = false
					break
				}
			}
			if allOK {
				for _, c := range name {
					fl := lookup[string(c)]
					for _, n := range fl.names {
						res.flags[n] = append(res.flags[n], "")
					}
				}
				continue
			}
		}

		return nil, fmt.Errorf("unknown flag %s", tok)
	}
	return res, nil
}

// completionWords returns the positional words typed so far, used by the tab
// completer. It is a best-effort parse that never fails.
func completionWords(tokens []string) []string { return tokens }
