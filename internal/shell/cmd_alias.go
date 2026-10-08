package shell

import (
	"context"
	"fmt"
	"strings"

	"github.com/nonex-code/silo/internal/config"
	"github.com/nonex-code/silo/internal/ui"
)

func init() {
	register(&command{
		name:    "alias",
		usage:   "alias [list | set <name> <url> <access> <secret> [api] | remove <name>]",
		summary: "manage mc aliases (stored in the mc config file)",
		group:   "config",
		run:     cmdAlias,
	})
	registerAlias("aliases", "alias")

	register(&command{
		name:    "config",
		usage:   "config",
		summary: "show the path of the mc configuration file",
		group:   "config",
		run: func(ctx context.Context, sh *Shell, args []string) error {
			fmt.Fprintf(sh.stdout, "%s %s\n", ui.Info.Sprint("config file:"), sh.cfg.Path())
			return nil
		},
	})
}

func cmdAlias(ctx context.Context, sh *Shell, args []string) error {
	if len(args) == 0 {
		return listAliases(sh)
	}
	switch args[0] {
	case "list", "ls":
		return listAliases(sh)
	case "set", "add":
		return setAlias(sh, args[1:])
	case "remove", "rm", "delete":
		return removeAlias(sh, args[1:])
	case "use":
		if len(args) != 2 {
			return fmt.Errorf("usage: alias use <name>")
		}
		if _, ok := sh.cfg.Get(args[1]); !ok {
			return fmt.Errorf("alias %q not found", args[1])
		}
		sh.cwd = Path{Alias: args[1]}
		sh.updatePrompt()
		return nil
	default:
		return fmt.Errorf("unknown alias subcommand %q", args[0])
	}
}

func listAliases(sh *Shell) error {
	names := sh.cfg.Names()
	if len(names) == 0 {
		fmt.Fprintln(sh.stdout, ui.Info.Sprint("no aliases configured"))
		return nil
	}
	fmt.Fprintf(sh.stdout, "%-14s %-34s %-22s %-8s %-6s %-6s\n",
		ui.Bold.Sprint("NAME"), ui.Bold.Sprint("URL"), ui.Bold.Sprint("ACCESS KEY"),
		ui.Bold.Sprint("API"), ui.Bold.Sprint("PATH"), ui.Bold.Sprint("SOURCE"))
	for _, n := range names {
		a, _ := sh.cfg.Get(n)
		source := "file"
		if sh.cfg.IsEnvAlias(n) {
			source = "env"
		}
		fmt.Fprintf(sh.stdout, "%-14s %-34s %-22s %-8s %-6s %-6s\n",
			ui.Alias.Sprint(n), a.URL, maskAccess(a.AccessKey), defaultStr(a.API, "S3v4"), defaultStr(a.Path, "auto"), source)
	}
	return nil
}

func setAlias(sh *Shell, args []string) error {
	pa, err := parseArgs(args, []flagDef{
		{Names: []string{"path"}, Value: true},
		{Names: []string{"api"}, Value: true},
		{Names: []string{"region"}, Value: true},
	})
	if err != nil {
		return err
	}
	pos := pa.Positional()
	if len(pos) < 4 {
		return fmt.Errorf("usage: alias set <name> <url> <access> <secret> [api]")
	}
	name, url := pos[0], pos[1]
	if strings.ContainsAny(name, "/ \t") {
		return fmt.Errorf("invalid alias name %q", name)
	}
	a := config.Alias{
		URL:       url,
		AccessKey: pos[2],
		SecretKey: pos[3],
		API:       "S3v4",
		Path:      "auto",
	}
	if len(pos) >= 5 {
		a.API = pos[4]
	}
	if v := pa.Get("api"); v != "" {
		a.API = v
	}
	if v := pa.Get("path"); v != "" {
		a.Path = v
	}
	if v := pa.Get("region"); v != "" {
		a.Region = v
	}
	// Preserve existing optional fields if only updating.
	if old, ok := sh.cfg.Get(name); ok {
		if a.Path == "auto" && old.Path != "" {
			a.Path = old.Path
		}
		a.Lookup = old.Lookup
		if a.Region == "" {
			a.Region = old.Region
		}
	}
	sh.cfg.SetAlias(name, a)
	if err := sh.cfg.Save(); err != nil {
		return err
	}
	delete(sh.clients, name)
	fmt.Fprintf(sh.stdout, "%s %s\n", ui.OK.Sprint("added alias"), ui.Alias.Sprint(name))
	return nil
}

func removeAlias(sh *Shell, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: alias remove <name>")
	}
	if !sh.cfg.RemoveAlias(args[0]) {
		return fmt.Errorf("alias %q not found", args[0])
	}
	delete(sh.clients, args[0])
	if err := sh.cfg.Save(); err != nil {
		return err
	}
	fmt.Fprintf(sh.stdout, "%s %s\n", ui.OK.Sprint("removed alias"), args[0])
	// If we were inside the removed alias, go back to the root.
	if sh.cwd.Alias == args[0] {
		sh.cwd = Path{}
		sh.updatePrompt()
	}
	return nil
}

func maskAccess(s string) string {
	if len(s) <= 4 {
		return s
	}
	return s[:2] + strings.Repeat("*", len(s)-4) + s[len(s)-2:]
}

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
