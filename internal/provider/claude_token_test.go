package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testSetupToken = "sk-ant-oat01-test-token-AAAA"

// fakeClaudeLimits stands in for Claude Code answering one word: f gets the
// config directory and the token the run is given, the saved account's when
// the ask names none.
func fakeClaudeLimits(t *testing.T, f func(dir, token string) ([]ClaudeLimit, error)) *atomic.Int32 {
	t.Helper()
	var runs atomic.Int32
	old := claudeCLILimits
	LimitsClaudeVia(func(_ context.Context, dir, token string) ([]ClaudeLimit, error) {
		runs.Add(1)
		if token == "" {
			token = ClaudeDirToken(dir)
		}
		return f(dir, token)
	})
	t.Cleanup(func() { claudeCLILimits = old })
	return &runs
}

func tokenLimits(token string) ([]ClaudeLimit, error) {
	if token != testSetupToken {
		return nil, errors.New("Claude Code: Failed to authenticate. API Error: 401 OAuth access token is invalid.")
	}
	reset := time.Now().Add(2 * time.Hour).Unix()
	return []ClaudeLimit{{Kind: "five_hour", Used: 0.22, ResetsAt: reset}, {Kind: "seven_day", Used: 0.09, ResetsAt: reset + 86400}}, nil
}

func TestClaudeTokenImportLine(t *testing.T) {
	for _, c := range []struct{ line, name string }{
		{testSetupToken, ""},
		{"work box " + testSetupToken, "work box"},
		{"headless: " + testSetupToken, "headless"},
		{"headless=" + testSetupToken, ""}, // one field: the token isn't the line's last field alone
	} {
		e, ok := claudeTokenImport(c.line)
		if c.line == "headless="+testSetupToken {
			if ok {
				t.Fatalf("%q read as a token: %+v", c.line, e)
			}
			continue
		}
		if !ok || e.setupToken != testSetupToken || e.name != c.name {
			t.Fatalf("%q: %+v, %v", c.line, e, ok)
		}
	}
	if _, ok := claudeTokenImport("sk-ant-ort01-a-refresh-token"); ok {
		t.Fatal("a refresh token read as a setup-token")
	}
	es, err := parseLoginImport("codex", testSetupToken)
	if err != nil || len(es) != 1 || es[0].setupToken != "" {
		t.Fatalf("a ChatGPT import took a Claude setup-token: %+v %v", es, err)
	}
}

// #2392: a setup-token is tried through Claude Code, kept under its name,
// in use beside Claude Code's own sign-in and never put in its place, and
// its allowance is what the try told.
func TestImportClaudeSetupToken(t *testing.T) {
	claudeThree(t)
	runs := fakeClaudeLimits(t, func(_, token string) ([]ClaudeLimit, error) { return tokenLimits(token) })
	ctx := context.Background()
	res, err := ImportLogins(ctx, "claude", []string{"headless " + testSetupToken})
	if err != nil || len(res) != 1 || res[0].Status != "added" || res[0].User != "headless" {
		t.Fatalf("import: %+v %v", res, err)
	}
	if runs.Load() != 1 {
		t.Fatalf("token tried %d times, want once", runs.Load())
	}
	var got *Login
	for _, l := range Logins("claude") {
		if l.Active && l.User != "pro@x.com" {
			t.Fatalf("Claude Code's own account moved to %q", l.User)
		}
		if l.User == "headless" {
			got = &l
		}
	}
	if got == nil || !got.Token || !got.On || got.Active || got.Lapsed != "" || got.Plan != claudeTokenPlan {
		t.Fatalf("token account: %+v", got)
	}
	if c, _, ok := claudeCredential(); !ok || c.OAuth.AccessToken == testSetupToken {
		t.Fatal("the setup-token was written as Claude Code's own sign-in")
	}
	ws := claudeLimitWindowsKept(t, "headless")
	if len(ws) != 2 || ws[0].Used != 22 || ws[1].Used != 9 {
		t.Fatalf("allowance told at import: %+v", ws)
	}

	// the same token again is the account already kept
	res, _ = ImportLogins(ctx, "claude", []string{testSetupToken})
	if len(res) != 1 || res[0].Status != "exists" || res[0].User != "headless" {
		t.Fatalf("again: %+v", res)
	}
	// another token under a name in use is turned away, untried
	before := runs.Load()
	res, _ = ImportLogins(ctx, "claude", []string{"max@x.com sk-ant-oat01-other"})
	if len(res) != 1 || res[0].Status != "failed" || runs.Load() != before {
		t.Fatalf("a taken name: %+v, %d tries", res, runs.Load()-before)
	}
	// a token Anthropic refuses is not kept
	res, _ = ImportLogins(ctx, "claude", []string{"bad sk-ant-oat01-refused"})
	if len(res) != 1 || res[0].Status != "failed" || !strings.Contains(res[0].Error, "claude setup-token") {
		t.Fatalf("refused token: %+v", res)
	}
	for _, l := range Logins("claude") {
		if l.User == "bad" {
			t.Fatal("a refused token was kept")
		}
	}
	if es, _ := os.ReadDir(claudeDirsRoot()); len(es) > 0 {
		for _, e := range es {
			if strings.HasPrefix(e.Name(), "token-") {
				t.Fatalf("a try's directory was left: %s", e.Name())
			}
		}
	}
}

func claudeLimitWindowsKept(t *testing.T, user string) []QuotaWindow {
	t.Helper()
	claudeUsage.Lock()
	defer claudeUsage.Unlock()
	return claudeUsage.m[strings.ToLower(user)].ws
}

func addTokenLogin(t *testing.T, name string) {
	t.Helper()
	fakeClaudeLimits(t, func(_, token string) ([]ClaudeLimit, error) { return tokenLimits(token) })
	res, err := ImportLogins(context.Background(), "claude", []string{name + " " + testSetupToken})
	if err != nil || len(res) != 1 || res[0].Status != "added" {
		t.Fatalf("import: %+v %v", res, err)
	}
}

// A run on a setup-token account is in a config directory with no sign-in
// in it, the token handed in its env alone.
func TestClaudeTokenDirHoldsNoSignIn(t *testing.T) {
	claudeThree(t)
	addTokenLogin(t, "headless")
	dir, err := claudeSavedDir("headless")
	if err != nil || dir != claudeAccountDir("headless") {
		t.Fatalf("dir %q, %v", dir, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".credentials.json")); !os.IsNotExist(err) {
		t.Fatalf("a sign-in was written in the token account's directory: %v", err)
	}
	if _, ok := readClaudeDir(dir); ok {
		t.Fatal("the token account's directory holds a sign-in")
	}
	if got := ClaudeDirToken(dir); got != testSetupToken {
		t.Fatalf("token for its directory: %q", got)
	}
	if got := ClaudeDirToken(claudeAccountDir("max@x.com")); got != "" {
		t.Fatalf("a signed-in account's directory was given a token: %q", got)
	}
	if err := SwitchLogin("claude", "headless"); err == nil || !strings.Contains(err.Error(), "setup-token") {
		t.Fatalf("switched in to a setup-token account: %v", err)
	}
}

// Anthropic refusing a setup-token lapses that account, for that token:
// nothing refreshes it. The same words about an account signed in in the
// browser, which Claude Code refreshes, lapse nothing.
func TestClaudeTokenRefusedLapses(t *testing.T) {
	claudeThree(t)
	addTokenLogin(t, "headless")
	msg := "Failed to authenticate. API Error: 401 OAuth access token is invalid."
	NoteClaudeSignInFailure("max@x.com", ClaudeLoginVersion("max@x.com"), msg)
	NoteClaudeSignInFailure("headless", "an older token", msg)
	for _, l := range Logins("claude") {
		if l.Lapsed != "" {
			t.Fatalf("%s lapsed: %s", l.User, l.Lapsed)
		}
	}
	NoteClaudeSignInFailure("headless", ClaudeLoginVersion("headless"), msg)
	for _, l := range Logins("claude") {
		if (l.User == "headless") != (l.Lapsed == claudeTokenLapse) {
			t.Fatalf("%s: %q", l.User, l.Lapsed)
		}
	}
	if _, err := claudeSavedDir("headless"); err == nil || !ClaudeSignInRequired(err.Error()) {
		t.Fatalf("a refused token is still run on: %v", err)
	}
}

// A setup-token's /usage tells no allowance: its card's refresh asks one
// word through Claude Code in its own directory instead, and never runs
// /usage.
func TestRefreshClaudeTokenUsage(t *testing.T) {
	claudeThree(t)
	var text atomic.Value
	text.Store("Total cost: $0.0000")
	fakeClaudeUsage(t, &text, nil)
	addTokenLogin(t, "headless")
	var dirs []string
	runs := fakeClaudeLimits(t, func(dir, token string) ([]ClaudeLimit, error) {
		dirs = append(dirs, dir)
		ls, err := tokenLimits(token)
		ls[0].Used = 0.5
		return ls, err
	})
	var inToken atomic.Int32
	UsageClaudeVia(func(_ context.Context, dir string) (string, error) {
		if dir == claudeAccountDir("headless") {
			inToken.Add(1)
		}
		return text.Load().(string), nil
	})
	RefreshUsage(context.Background(), "claude", "headless")
	q := LoginUsage(context.Background(), "claude")["headless"]
	if q.Error != "" || len(q.Windows) != 2 || q.Windows[0].Used != 50 {
		t.Fatalf("card: %+v", q)
	}
	if runs.Load() != 1 || len(dirs) != 1 || dirs[0] != claudeAccountDir("headless") {
		t.Fatalf("asked %d times in %v", runs.Load(), dirs)
	}
	if inToken.Load() != 0 {
		t.Fatal("/usage ran for a setup-token account")
	}
}

// With Claude Code signed out, a setup-token added is served in its place
// from a directory of its own: it is never written as Claude Code's own
// sign-in, which would have no refresh token.
func TestClaudeTokenWithClaudeCodeSignedOut(t *testing.T) {
	claudeHome(t)
	addTokenLogin(t, "headless")
	if _, _, ok := claudeCredential(); ok {
		t.Fatal("the setup-token was written as Claude Code's own sign-in")
	}
	p, ok := claudeStandInAccount()
	if !ok || p.Account.User != "headless" {
		t.Fatalf("stand-in: %+v %v", p.Account, ok)
	}
	dir, own, err := p.Account.Token(context.Background())
	if err != nil || !own || dir != claudeAccountDir("headless") {
		t.Fatalf("stand-in runs in %q (%v, %v)", dir, own, err)
	}
}
