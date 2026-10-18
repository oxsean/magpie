package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

const addTokenUsage = "usage: magpie accounts add claude --token [--name <name>] [--yes]  (the token from `claude setup-token`, typed or piped in)"

// addClaudeToken: `magpie accounts add claude --token` adds a Claude
// account by the token `claude setup-token` prints (#2392). The token is
// typed unseen or piped in, never an argument, which other programs could
// read in the process list.
func addClaudeToken(args []string) error {
	name, yes := "", false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--token":
		case a == "--yes" || a == "-y":
			yes = true
		case a == "--name" && i+1 < len(args):
			name = args[i+1]
			i++
		case strings.HasPrefix(a, "--name="):
			name = strings.TrimPrefix(a, "--name=")
		case strings.HasPrefix(a, "sk-ant-"):
			// echoed back, it would land in the terminal's scrollback
			return errors.New("the token isn't taken as an argument: type it when asked, or pipe it in")
		default:
			return fmt.Errorf("%q: %s", a, addTokenUsage)
		}
	}
	name = strings.TrimSpace(name)
	if strings.ContainsAny(name, "\r\n") {
		return errors.New("a name is one line")
	}
	tty := promptTerminal(int(os.Stdin.Fd()))
	fmt.Println(bold.Render("!"), provider.ClaudeRisk)
	fmt.Println(bold.Render("!"), "magpie tries the token with one word at Haiku through Claude Code before it keeps it, and again each time its allowance is refreshed.")
	if !yes {
		if !tty {
			return errors.New("piped in, the token is added only with --yes")
		}
		fmt.Print("Add it? [y/N] ")
		var answer string
		fmt.Scanln(&answer)
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			return errors.New("canceled")
		}
	}
	var token string
	if tty {
		fmt.Print("Paste the token from `claude setup-token` (it isn't shown): ")
		b, err := promptHidden(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return err
		}
		token = strings.TrimSpace(string(b))
	} else {
		line, err := stdin.ReadString('\n')
		if err != nil && line == "" {
			return errors.New("no token on stdin")
		}
		token = strings.TrimSpace(line)
	}
	if !strings.HasPrefix(token, "sk-ant-oat01-") || strings.ContainsAny(token, " \t") {
		return errors.New("that isn't a token from `claude setup-token` (it starts sk-ant-oat01-)")
	}
	ctx, stop := interruptContext()
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	fmt.Println(muted.Render("trying it with Claude Code…"))
	res, err := provider.ImportLogins(ctx, "claude", []string{strings.TrimSpace(name + " " + token)})
	if err != nil {
		return err
	}
	for _, r := range res {
		switch r.Status {
		case "added":
			fmt.Println(green.Render("✓"), "added", r.User, muted.Render("· in use beside Claude Code's own sign-in"))
			fmt.Println(faint.Render("  see its allowance: magpie accounts claude"))
			return nil
		case "exists":
			fmt.Println(muted.Render("="), r.User, muted.Render("· in magpie already with this token"))
			return nil
		default:
			return errors.New(r.Error)
		}
	}
	return errors.New("nothing added")
}
