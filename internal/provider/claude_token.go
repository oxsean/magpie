package provider

// A Claude account added by its setup-token (#2392): the long-lived token
// `claude setup-token` prints, which Claude Code takes from
// CLAUDE_CODE_OAUTH_TOKEN. Claude Code runs on it in a config directory of
// its own, as on any saved account, with the token in its env and no
// sign-in in the directory. The token is never refreshed, and it can read
// no profile: the account is known by the name the user gives it, and its
// /usage tells no allowance. Its allowance is what Claude Code tells as it
// answers (rate_limit_event), from a request magpie routes or a one-word
// ask at Haiku (claudeTokenLimits).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// claudeTokenPrefix starts every Claude OAuth access token, a setup-token's
// included.
const claudeTokenPrefix = "sk-ant-oat01-"

// claudeTokenLapse is why a setup-token account Anthropic refused can't be
// used: a setup-token isn't refreshed, so only a new one mends it.
const claudeTokenLapse = "Anthropic refused this setup-token; run `claude setup-token` and add the new token in magpie"

// errClaudeTokenSwitch is why a setup-token account isn't switched in to
// Claude Code: its sign-in holds no refresh token, which Claude Code needs.
var errClaudeTokenSwitch = errors.New("an account added by its setup-token runs beside Claude Code's own sign-in and can't be switched in to Claude Code")

// claudeTokenPlan is a setup-token account's plan, which no token tells:
// its row says how it was added.
const claudeTokenPlan = "setup-token"

// claudeTokenAuth is a setup-token account's Auth in logins.json.
type claudeTokenAuth struct {
	SetupToken string `json:"setupToken"`
}

// claudeSetupToken is the setup-token a saved Claude login runs on; "" for
// one signed in in the browser.
func claudeSetupToken(l savedLogin) string {
	if l.Agent != "claude" {
		return ""
	}
	var a claudeTokenAuth
	if json.Unmarshal(l.Auth, &a) != nil {
		return ""
	}
	return a.SetupToken
}

func claudeTokenVersion(token string) string {
	sum := sha256.Sum256([]byte("setup-token\x00" + token))
	return hex.EncodeToString(sum[:])
}

// ClaudeDirToken is the setup-token of the saved Claude account whose
// config directory is dir, which a Claude Code run there is given as
// CLAUDE_CODE_OAUTH_TOKEN; "" for any other directory.
func ClaudeDirToken(dir string) string {
	if dir == "" {
		return ""
	}
	loginsMu.Lock()
	defer loginsMu.Unlock()
	for _, l := range readLogins() {
		if tok := claudeSetupToken(l); tok != "" && claudeAccountDir(l.User) == dir {
			return tok
		}
	}
	return ""
}

// ClaudeTokenRefused says a Claude Code run's error is Anthropic refusing
// its OAuth token outright: for a setup-token, which nothing refreshes,
// that is for good.
func ClaudeTokenRefused(message string) bool {
	return strings.Contains(message, "OAuth access token is invalid") ||
		strings.Contains(message, "OAuth access token has been revoked") ||
		strings.Contains(message, "OAuth token revoked")
}

// claudeCLILimits asks Claude Code one word at Haiku in configDir, with
// token as its CLAUDE_CODE_OAUTH_TOKEN when not "", and gives the
// allowance its rate_limit_event told. The gateway sets it.
var claudeCLILimits func(ctx context.Context, configDir, token string) ([]ClaudeLimit, error)

// LimitsClaudeVia sets how a setup-token account's allowance is asked.
func LimitsClaudeVia(f func(ctx context.Context, configDir, token string) ([]ClaudeLimit, error)) {
	claudeCLILimits = f
}

// claudeTokenLimits asks the allowance of the account token runs on, in
// config directory dir; an error when Claude Code told none.
func claudeTokenLimits(ctx context.Context, dir, token string) ([]ClaudeLimit, error) {
	f := claudeCLILimits
	if f == nil {
		return nil, errClaudeCannotRun
	}
	ctx, cancel := context.WithTimeout(ctx, claudeWait)
	defer cancel()
	ls, err := f(ctx, dir, token)
	if err != nil {
		return nil, err
	}
	if len(claudeLimitWindows(ls)) == 0 {
		return nil, errors.New("Claude Code answered and told no allowance")
	}
	return ls, nil
}

// readTokenClaudeUsage is the allowance of the setup-token account user,
// as Claude Code tells it answering one word at Haiku.
func readTokenClaudeUsage(ctx context.Context, user string) ([]QuotaWindow, error) {
	dir, err := claudeSavedDir(user)
	if err != nil {
		return []QuotaWindow{}, err
	}
	ls, err := claudeTokenLimits(ViaLogin(ctx, "claude", user), dir, "")
	if err != nil {
		if ClaudeTokenRefused(err.Error()) {
			NoteClaudeSignInFailure(user, ClaudeLoginVersion(user), err.Error())
		}
		return []QuotaWindow{}, err
	}
	return claudeLimitWindows(ls), nil
}

// claudeTokenImport is a setup-token read from an import's text, and the
// name given before it on its line, if any.
func claudeTokenImport(line string) (loginImport, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 || !strings.HasPrefix(fields[len(fields)-1], claudeTokenPrefix) {
		return loginImport{}, false
	}
	tok := fields[len(fields)-1]
	name := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.TrimSuffix(line, tok)), ":"))
	return loginImport{setupToken: tok, name: name}, true
}

// claudeTokenName is the name a setup-token account goes by when the user
// gave none: told apart by its token, which it doesn't show.
func claudeTokenName(token string) string {
	return "setup-token " + claudeTokenVersion(token)[:6]
}

// importClaudeToken tries the setup-token e carries with Claude Code, in a
// config directory of its own, and keeps it under its name, its allowance
// what that try told.
func importClaudeToken(ctx context.Context, e loginImport, kept []savedLogin) ImportedAccount {
	name := e.name
	if name == "" {
		name = claudeTokenName(e.setupToken)
	}
	fail := func(msg string) ImportedAccount {
		return ImportedAccount{User: name, Status: "failed", Error: msg}
	}
	for _, k := range kept {
		if claudeSetupToken(k) == e.setupToken {
			return ImportedAccount{User: k.User, Status: "exists"}
		}
		if strings.EqualFold(strings.TrimSpace(k.User), name) {
			return fail(fmt.Sprintf("a Claude account named %q is in magpie already; give this token another name", name))
		}
	}
	if err := os.MkdirAll(claudeDirsRoot(), 0o700); err != nil {
		return fail(err.Error())
	}
	dir, err := os.MkdirTemp(claudeDirsRoot(), "token-")
	if err != nil {
		return fail(err.Error())
	}
	defer os.RemoveAll(dir)
	ls, err := claudeTokenLimits(ctx, dir, e.setupToken)
	if err != nil {
		if ClaudeTokenRefused(err.Error()) {
			return fail("Anthropic refused this token; run `claude setup-token` for a new one")
		}
		return fail(err.Error())
	}
	auth, _ := json.Marshal(claudeTokenAuth{SetupToken: e.setupToken})
	l := savedLogin{Agent: "claude", User: name, Plan: claudeTokenPlan, Auth: auth}
	if _, err := addLogin(l); err != nil {
		return fail(err.Error())
	}
	NoteClaudeLimits(name, ls)
	return ImportedAccount{User: name, Status: "added", Plan: claudeTokenPlan}
}

// claudeTokenLogin says the saved Claude account user runs on a
// setup-token.
func claudeTokenLogin(user string) bool {
	loginsMu.Lock()
	defer loginsMu.Unlock()
	for _, l := range readLogins() {
		if l.Agent == "claude" && strings.EqualFold(l.User, user) {
			return claudeSetupToken(l) != ""
		}
	}
	return false
}
