package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/netproxy"
	"github.com/yetone/magpie/internal/proc"
	"github.com/yetone/magpie/internal/provider"
)

// claudeProxy is the proxy a Claude Code run on an account's behalf goes
// through: the one ctx names (the account's own, or its subscription's),
// else the subscription's.
func claudeProxy(ctx context.Context) string {
	if c := netproxy.Choice(ctx); c != "" {
		return c
	}
	return provider.ProxyOf("claude")
}

// warmClaude sends a Claude account one "hi" through Claude Code, as the
// bridge runs it (claudeCLIArgs): none of its tools, settings or MCP
// servers, nothing kept on disk, at Haiku, the least an account's window
// is started by. configDir is a saved account's config directory, "" for
// the one Claude Code is signed in to.
func warmClaude(ctx context.Context, configDir string) error {
	return askClaude(ctx, configDir, "haiku")
}

// askClaude has Claude Code answer one "hi" at model, as warmClaude does;
// it is how a Claude account's model test runs (provider.ProbeClaudeVia),
// so the test asks Anthropic as Claude Code does, through Claude Code.
func askClaude(ctx context.Context, configDir, model string) error {
	binary, err := claudeBinary()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "magpie-claude-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	cmd := binary.command(ctx, claudeWarmArgs(model)...)
	cmd.Dir = tmp
	cmd.Stdin = strings.NewReader("hi")
	env, err := binary.proxied(ctx, cleanClaudeEnv(os.Environ()))
	if err != nil {
		return err
	}
	cmd.Env = binary.env(env, configDir)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	runErr := runClaudeTree(cmd)
	if res, ok := claudeResult(out.Bytes()); ok && res.IsError {
		return errors.New("Claude Code: " + clip(res.Result))
	}
	if runErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg == "" {
			return runErr
		}
		return errors.New("Claude Code: " + clip(msg))
	}
	return nil
}

func init() {
	provider.ProbeClaudeVia(askClaude)
	provider.UsageClaudeVia(claudeUsage)
	provider.LimitsClaudeVia(claudeAskLimits)
}

// claudeAskLimits has Claude Code answer one "hi" at Haiku, as warmClaude
// does, in configDir on token ("" for the saved account's own), and gives
// the allowance its rate_limit_event told: a setup-token's /usage tells
// none.
func claudeAskLimits(ctx context.Context, configDir, token string) ([]provider.ClaudeLimit, error) {
	binary, err := claudeBinary()
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "magpie-claude-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	cmd := binary.command(ctx, claudeLimitsArgs()...)
	cmd.Dir = tmp
	cmd.Stdin = strings.NewReader("hi")
	env, err := binary.proxied(ctx, cleanClaudeEnv(os.Environ()))
	if err != nil {
		return nil, err
	}
	cmd.Env = binary.envOn(env, configDir, token)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	runErr := runClaudeTree(cmd)
	limits, res, ok := claudeStreamLimits(out.Bytes())
	switch {
	case ok && res.IsError:
		return limits, errors.New("Claude Code: " + clip(res.Result))
	case len(limits) > 0:
		return limits, nil
	case runErr != nil:
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, runErr
		}
		return nil, errors.New("Claude Code: " + clip(msg))
	}
	return nil, errors.New("Claude Code told no allowance")
}

// claudeStreamLimits reads what claude -p --output-format stream-json
// printed: the windows its rate_limit_events told, and its result.
func claudeStreamLimits(b []byte) (limits []provider.ClaudeLimit, res claudeRun, ok bool) {
	for line := range bytes.SplitSeq(b, []byte("\n")) {
		var e struct {
			Type          string          `json:"type"`
			RateLimitInfo json.RawMessage `json:"rate_limit_info"`
		}
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch e.Type {
		case "rate_limit_event":
			limits = append(limits, claudeLimits(e.RateLimitInfo)...)
		case "result":
			ok = json.Unmarshal(line, &res) == nil
		}
	}
	return limits, res, ok
}

// claudeUsage is what Claude Code's /usage prints for the account in
// configDir (empty for its own): a command asking no model, run as
// askClaude runs it, with nothing of the user's settings and nothing kept.
func claudeUsage(ctx context.Context, configDir string) (string, error) {
	binary, err := claudeBinary()
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "magpie-claude-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	cmd := binary.command(ctx, claudeUsageArgs()...)
	cmd.Dir = tmp
	cmd.Stdin = strings.NewReader("")
	// CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC makes /usage skip its request
	// and print only a reading another run left in the last hour, so it stays
	// only when the user set it.
	env := cleanClaudeEnv(os.Environ())
	if os.Getenv("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC") == "" {
		env = slices.DeleteFunc(env, func(e string) bool {
			return strings.HasPrefix(e, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=")
		})
	}
	env = append(env, "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "DISABLE_AUTOUPDATER=1")
	env, err = binary.proxied(ctx, env)
	if err != nil {
		return "", err
	}
	cmd.Env = binary.env(env, configDir)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	runErr := runClaudeTree(cmd)
	if res, ok := claudeResult(out.Bytes()); ok {
		if res.IsError {
			return "", errors.New("Claude Code: " + clip(res.Result))
		}
		return res.Result, nil
	}
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		msg = strings.TrimSpace(out.String())
	}
	if msg == "" && runErr != nil {
		return "", runErr
	}
	return "", errors.New("Claude Code: " + clip(msg))
}

// runClaudeTree runs a one-off Claude Code (a test, a /usage) and ends,
// with it, whatever it started: its context ending kills only the
// process magpie started, and on Windows that is cmd.exe running npm's
// claude.cmd, whose node went on running with nobody waiting for it
// (#1894). In a tree, a job on Windows and a session elsewhere, what is
// left once the head is gone is ended with it, and on Windows when magpie
// exits mid-run.
func runClaudeTree(cmd *exec.Cmd) error {
	t, err := proc.StartTree(cmd)
	if err != nil {
		return err
	}
	return t.Wait()
}

// claudeRun is the result Claude Code prints for a -p run with
// --output-format json.
type claudeRun struct {
	Type    string `json:"type"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
}

// claudeResult reads the result from what claude -p --output-format json
// printed: one object, or, when the user turned on Claude Code's verbose
// output (its own config, which --setting-sources leaves on), an array of
// every message, the result last.
func claudeResult(b []byte) (claudeRun, bool) {
	var res claudeRun
	if json.Unmarshal(b, &res) == nil {
		return res, true
	}
	var all []claudeRun
	if json.Unmarshal(b, &all) != nil {
		return claudeRun{}, false
	}
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Type == "result" {
			return all[i], true
		}
	}
	return claudeRun{}, false
}

func claudeUsageArgs() []string {
	return []string{"-p", "/usage", "--output-format", "json",
		"--tools", "", "--strict-mcp-config", "--setting-sources", "", "--no-session-persistence"}
}

func claudeLimitsArgs() []string {
	return []string{"-p", "--output-format", "stream-json", "--verbose", "--model", "haiku",
		"--tools", "", "--strict-mcp-config", "--setting-sources", "", "--no-session-persistence"}
}

func claudeWarmArgs(model string) []string {
	return []string{"-p", "--output-format", "json", "--model", model,
		"--tools", "", "--strict-mcp-config", "--setting-sources", "", "--no-session-persistence"}
}

func clip(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
