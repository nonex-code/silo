package shell

import (
	"context"
	"fmt"
	"sort"

	"github.com/nonex-code/silo/internal/ui"
)

// command describes a shell command.
type command struct {
	name    string
	usage   string
	summary string
	group   string
	run     func(ctx context.Context, sh *Shell, args []string) error
}

var commands = map[string]*command{}

// aliases maps alternative command names to their canonical command.
var commandAliases = map[string]string{}

func register(c *command) {
	commands[c.name] = c
}

func registerAlias(alias, canonical string) {
	commandAliases[alias] = canonical
}

func lookupCommand(name string) (*command, bool) {
	if canon, ok := commandAliases[name]; ok {
		name = canon
	}
	c, ok := commands[name]
	return c, ok
}

// helpCommand prints the command list or details for one command.
func helpCommand(ctx context.Context, sh *Shell, args []string) error {
	if len(args) > 0 {
		c, ok := lookupCommand(args[0])
		if !ok {
			return fmt.Errorf("unknown command %q", args[0])
		}
		fmt.Fprintf(sh.stdout, "%s\n", ui.Bold.Sprintf("usage: %s", c.usage))
		if c.summary != "" {
			fmt.Fprintf(sh.stdout, "\n%s\n", c.summary)
		}
		if canon, isAlias := commandAliases[args[0]]; isAlias {
			fmt.Fprintf(sh.stdout, "\n%s\n", ui.Info.Sprintf("%s is an alias for %s", args[0], canon))
		}
		return nil
	}

	fmt.Fprintf(sh.stdout, "\n%s\n", ui.Bold.Sprint("usage"))
	fmt.Fprintf(sh.stdout, "  %-9s %-27s %s\n", "human", "silo", "interactive prompt, Tab completion, history")
	fmt.Fprintf(sh.stdout, "  %-9s %-27s %s\n", "agent", "silo -c \"ls /bkt/prefix\"", "one command; add --json for machine output")
	fmt.Fprintf(sh.stdout, "  %-9s %-27s %s\n", "batch", "printf 'cd /a\\nls\\n' | silo", "many lines in one process (state kept)")
	fmt.Fprintf(sh.stdout, "  %-9s %-27s %s\n", "flags", "silo -c \"put -r ./d /a/b/\"", "command flags stay inside the quotes")
	fmt.Fprintf(sh.stdout, "%s\n", ui.Info.Sprint("  all forms run the same grammar; -c is the agent entry point. With -c use absolute\n  paths (/alias/bucket/key): cwd does not persist across processes."))

	groups := map[string][]*command{}
	var order []string
	for _, c := range commands {
		if _, seen := groups[c.group]; !seen {
			order = append(order, c.group)
		}
		groups[c.group] = append(groups[c.group], c)
	}
	sort.Strings(order)
	for _, g := range order {
		if g == "" {
			g = "misc"
		}
		fmt.Fprintf(sh.stdout, "\n%s\n", ui.Bold.Sprint(g))
		list := groups[g]
		sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })
		for _, c := range list {
			fmt.Fprintf(sh.stdout, "  %s  %s\n", ui.OK.Sprintf("%-12s", c.name), c.summary)
		}
	}
	fmt.Fprintf(sh.stdout, "\n%s\n", ui.Info.Sprint("tip: use 'help <command>' for usage details"))

	if len(commandAliases) > 0 {
		type pair struct{ alias, canon string }
		pairs := make([]pair, 0, len(commandAliases))
		for a, c := range commandAliases {
			pairs = append(pairs, pair{a, c})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].alias < pairs[j].alias })
		fmt.Fprintf(sh.stdout, "\n%s\n", ui.Bold.Sprint("aliases"))
		for _, p := range pairs {
			fmt.Fprintf(sh.stdout, "  %s  %s\n", ui.OK.Sprintf("%-12s", p.alias), p.canon)
		}
	}
	return nil
}
