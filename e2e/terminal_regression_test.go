package e2e

import (
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baires/yz/e2e/fakes"
	"github.com/baires/yz/internal/config"
)

func TestTerminalNoColor(t *testing.T) {
	scenario(t, "NO_COLOR keeps navigation and strips styling color", func(t *testing.T) {
		_, api, env := discoveryFixture(t)
		api.Configure(func(a *fakes.CFAPI) {
			a.BucketNames = []string{"photos", "blog-exports"}
		})
		env["NO_COLOR"] = "1"
		run := startTerminal(t, env, "setup")
		run.Await("yz  /  setup")
		run.Await("blog-exports")
		run.Send("j")
		selected := run.Await("> blog-exports")
		if colorSequence(selected) {
			t.Fatalf("NO_COLOR stderr still has styling color: %q", selected)
		}
		run.Interrupt()
		res := run.Wait()
		if res.ExitCode != 130 {
			t.Fatalf("exit = %d, want 130", res.ExitCode)
		}
		run.AssertTerminalRestored()
	})
}

func TestTerminalStartupFailure(t *testing.T) {
	scenario(t, "disconnected terminal releases the login listener", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "yz")
		oauth := fakes.NewOAuthServer(t)
		env := map[string]string{
			"YZ_CONFIG_DIR": dir, "YZ_OAUTH_BASE_URL": oauth.URL(),
		}
		run := startTerminal(t, env, "setup")
		run.Await("Open this URL")
		view := run.Await("redirect_uri=")
		rawURL := recoverAuthorizeURL(t, view)
		run.Hangup()
		res := run.Wait()
		if res.ExitCode == 137 {
			t.Fatalf("terminal hangup left a child that had to be killed; stderr %q", res.Stderr)
		}
		if !listenerClosed(t, rawURL) {
			t.Fatal("OAuth listener still accepting connections after hangup")
		}
		if cfg, err := config.Load(dir); err == nil && cfg.AccessToken != "" {
			t.Fatal("hangup saved an access token")
		}
		run.AssertTerminalRestored()
	})
}

func TestTerminalLateOAuthCallback(t *testing.T) {
	scenario(t, "OAuth callback after cancel does not resume setup", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "yz")
		oauth := fakes.NewOAuthServer(t)
		env := map[string]string{
			"YZ_CONFIG_DIR": dir, "YZ_OAUTH_BASE_URL": oauth.URL(),
		}
		run := startTerminal(t, env, "setup")
		run.Await("Open this URL")
		view := run.Await("redirect_uri=")
		rawURL := recoverAuthorizeURL(t, view)
		run.Interrupt()
		res := run.Wait()
		if res.ExitCode != 130 {
			t.Fatalf("exit = %d, want 130; stderr %q", res.ExitCode, res.Stderr)
		}
		run.AssertTerminalRestored()
		if !listenerClosed(t, rawURL) {
			t.Fatal("listener still open after cancel")
		}
		late := callbackURL(t, rawURL)
		client := &http.Client{Timeout: 300 * time.Millisecond}
		if resp, err := client.Get(late); err == nil {
			_ = resp.Body.Close()
			t.Fatalf("late callback got HTTP %d", resp.StatusCode)
		}
		if cfg, err := config.Load(dir); err == nil && cfg.AccessToken != "" {
			t.Fatal("late callback saved a token")
		}
		if strings.Contains(res.Stderr, "logged in") {
			t.Fatalf("cancel resumed login: %q", res.Stderr)
		}
	})
}

func TestTerminalCleanup(t *testing.T) {
	scenario(t, "plain dumb output and terminal exit restore state", func(t *testing.T) {
		_, _, _, env := shareFixture(t, "https://files.example")
		dumb := startTerminal(t, mergeEnv(env, map[string]string{"TERM": "dumb"}), "missing-file.png")
		dumbRes := dumb.Wait()
		if dumbRes.ExitCode != 1 {
			t.Fatalf("dumb exit = %d, want 1; stderr %q", dumbRes.ExitCode, dumbRes.Stderr)
		}
		if strings.Contains(dumbRes.Stderr, "\x1b") {
			t.Fatalf("TERM=dumb emitted ANSI: %q", dumbRes.Stderr)
		}
		dumb.AssertTerminalRestored()

		missing := startTerminal(t, env, "missing-file.png")
		missingRes := missing.Wait()
		if missingRes.ExitCode != 1 || missingRes.Stdout != "" {
			t.Fatalf("missing file result = %+v", missingRes)
		}
		missing.AssertTerminalRestored()

		path := writeSizedFile(t, "ok.png", 16)
		ok := startTerminal(t, env, path)
		okRes := ok.Wait()
		if okRes.ExitCode != 0 || strings.TrimSpace(okRes.Stdout) == "" {
			t.Fatalf("success result = %+v", okRes)
		}
		ok.AssertTerminalRestored()
	})
}

func colorSequence(s string) bool {
	for _, seq := range []string{"\x1b[38;", "\x1b[48;", "\x1b[31m", "\x1b[32m", "\x1b[1;"} {
		if strings.Contains(s, seq) {
			return true
		}
	}
	return false
}

func listenerClosed(t *testing.T, authorizeURL string) bool {
	t.Helper()
	host := callbackURL(t, authorizeURL)
	parsed, err := url.Parse(host)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", parsed.Host, 200*time.Millisecond)
	if err != nil {
		return true
	}
	_ = conn.Close()
	return false
}

func callbackURL(t *testing.T, authorizeURL string) string {
	t.Helper()
	parsed, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	redirect, err := url.Parse(parsed.Query().Get("redirect_uri"))
	if err != nil || redirect.Host == "" {
		t.Fatalf("redirect_uri missing in %q", authorizeURL)
	}
	return redirect.String() + "?code=late&state=late"
}
