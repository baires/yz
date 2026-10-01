package e2e

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baires/yz/e2e/fakes"
	"github.com/baires/yz/internal/config"
)

func TestTerminalSetupLogin(t *testing.T) {
	scenario(t, "terminal login shows spinner and a usable URL", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "yz")
		api := fakes.NewCFAPI(t)
		s3 := fakes.NewCredentialServer(t)
		oauth := fakes.NewOAuthServer(t)
		api.ManagedEnabled = true
		env := map[string]string{
			"YZ_CONFIG_DIR": dir, "YZ_API_BASE_URL": api.URL(),
			"YZ_R2_ENDPOINT": s3.URL(), "YZ_OAUTH_BASE_URL": oauth.URL(),
		}
		run := startTerminal(t, env, "setup")
		run.Resize(40, 24)
		run.Await("yz  /  setup")
		view := run.Await("Open this URL")
		if !spinnerVisible(view) {
			t.Fatalf("login view has no spinner: %q", view)
		}
		rawURL := recoverAuthorizeURL(t, view)
		if !strings.Contains(rawURL, "client_id=") || !strings.Contains(rawURL, "code_challenge=") ||
			!strings.Contains(rawURL, "redirect_uri=") || !strings.Contains(rawURL, "state=") ||
			!strings.Contains(rawURL, "scope=") {
			t.Fatalf("authorize URL lost query parameters: %q", rawURL)
		}
		resp, err := browserClient.Get(rawURL)
		if err != nil {
			t.Fatalf("fetching authorize URL: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("authorize status = %d, want 302", resp.StatusCode)
		}
		run.Interrupt()
		res := run.Wait()
		if res.Stdout != "" {
			t.Fatalf("stdout = %q, want empty", res.Stdout)
		}
		access, refresh := oauth.LastTokens()
		if access != "" && strings.Contains(res.Stderr, access) {
			t.Fatal("access token rendered")
		}
		if refresh != "" && strings.Contains(res.Stderr, refresh) {
			t.Fatal("refresh token rendered")
		}
	})
}

func TestTerminalSetupSelection(t *testing.T) {
	scenario(t, "terminal setup selects accounts and filtered buckets", func(t *testing.T) {
		_, api, env := discoveryFixture(t)
		api.Configure(func(a *fakes.CFAPI) {
			a.Accounts = []map[string]string{{"id": fakes.AccountID, "name": "Personal"}}
			a.BucketNames = []string{"photos"}
		})
		single := startTerminal(t, env, "setup")
		single.Await("yz  /  setup")
		singleView := single.Await("Using Cloudflare account")
		if strings.Contains(stripSGR(singleView), "Choose an account") {
			t.Fatalf("single account showed a list: %q", singleView)
		}
		single.Interrupt()
		single.Wait()

		workID := strings.Repeat("2", 32)
		dir, api, s3, env := setupFixture(t)
		cfg := loadSavedConfig(t, dir)
		cfg.AccountID = ""
		if err := config.Save(dir, cfg); err != nil {
			t.Fatal(err)
		}
		api.Configure(func(a *fakes.CFAPI) {
			a.ManagedEnabled = true
			a.Accounts = []map[string]string{
				{"id": workID, "name": "Work"},
				{"id": fakes.AccountID, "name": "Personal"},
			}
			a.BucketNames = []string{"photos", "blog-exports", "decorai-dev"}
		})
		s3.Bucket = "blog-exports"
		run := startTerminal(t, env, "setup")
		run.Resize(80, 24)
		accounts := run.Await(workID)
		if !strings.Contains(accounts, fakes.AccountID) {
			t.Fatalf("account IDs missing: %q", accounts)
		}
		run.Send("j")
		run.Send("\r")
		run.Await("blog-exports")
		run.Send("/")
		run.Send("blog")
		run.Await("> blog-exports")
		run.Send("\r")
		run.Send("\r")
		run.Await("Access Key")
		run.Send(fakes.AccessKey + "\r")
		run.Await("Secret Access Key")
		run.Send(fakes.SecretKey + "\r")
		res := run.Wait()
		if res.ExitCode != 0 {
			t.Fatalf("exit = %d, stderr %q", res.ExitCode, res.Stderr)
		}
		cfg = loadSavedConfig(t, dir)
		if cfg.AccountID != fakes.AccountID || cfg.Bucket != "blog-exports" {
			t.Fatalf("saved account/bucket = %s %s", cfg.AccountID, cfg.Bucket)
		}
		if strings.Contains(res.Stderr, fakes.SecretKey) || strings.Contains(res.Stderr, fakes.AccessKey) {
			t.Fatal("credentials rendered")
		}
		if strings.Contains(res.Stderr, "setupModel") || strings.Contains(res.Stdout, "setupModel") {
			t.Fatal("model dump rendered")
		}
	})
}

func TestTerminalSetupResize(t *testing.T) {
	scenario(t, "terminal setup preserves selection across resize", func(t *testing.T) {
		_, api, env := discoveryFixture(t)
		api.Configure(func(a *fakes.CFAPI) {
			a.BucketNames = []string{"photos", "blog-exports"}
		})
		run := startTerminal(t, env, "setup")
		run.Resize(80, 24)
		run.Await("yz  /  setup")
		run.Await("blog-exports")
		run.Send("j")
		run.Await("> blog-exports")
		run.Resize(20, 6)
		run.Await("resize")
		run.Resize(80, 24)
		view := run.Await("blog-exports")
		if !selectionKept(view, "blog-exports") {
			t.Fatalf("selection lost after resize: %q", view)
		}
		run.Interrupt()
		res := run.Wait()
		if res.ExitCode != 130 {
			t.Fatalf("exit = %d, want 130", res.ExitCode)
		}
		run.AssertTerminalRestored()
	})
}

func TestTerminalSetupCancel(t *testing.T) {
	scenario(t, "terminal setup cancel exits 130 and restores the terminal", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "yz")
		oauth := fakes.NewOAuthServer(t)
		api := fakes.NewCFAPI(t)
		env := map[string]string{
			"YZ_CONFIG_DIR": dir, "YZ_OAUTH_BASE_URL": oauth.URL(), "YZ_API_BASE_URL": api.URL(),
		}
		login := startTerminal(t, env, "setup")
		login.Await("yz  /  setup")
		login.Interrupt()
		loginRes := login.Wait()
		if loginRes.ExitCode != 130 || loginRes.Stdout != "" {
			t.Fatalf("login cancel = %+v", loginRes)
		}
		login.AssertTerminalRestored()
		if _, err := config.Load(dir); err == nil && loadSavedConfig(t, dir).AccessToken != "" {
			t.Fatal("cancelled login saved a token")
		}

		saved, api, listEnv := discoveryFixture(t)
		before, err := osReadConfig(saved)
		if err != nil {
			t.Fatal(err)
		}
		list := startTerminal(t, listEnv, "setup")
		list.Await("Choose")
		list.Interrupt()
		listRes := list.Wait()
		if listRes.ExitCode != 130 {
			t.Fatalf("list cancel exit = %d, stderr %q", listRes.ExitCode, listRes.Stderr)
		}
		list.AssertTerminalRestored()
		after, err := osReadConfig(saved)
		if err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatal("list cancel changed config")
		}

		api.Configure(func(a *fakes.CFAPI) {
			a.Delay = 5 * time.Second
			a.Accounts = []map[string]string{
				{"id": fakes.AccountID, "name": "Personal"},
				{"id": strings.Repeat("2", 32), "name": "Work"},
			}
		})
		cfg := loadSavedConfig(t, saved)
		cfg.AccountID = ""
		if err := config.Save(saved, cfg); err != nil {
			t.Fatal(err)
		}
		network := startTerminal(t, listEnv, "setup")
		network.Await("yz  /  setup")
		network.Interrupt()
		networkRes := network.Wait()
		if networkRes.ExitCode != 130 {
			t.Fatalf("network cancel exit = %d, stderr %q", networkRes.ExitCode, networkRes.Stderr)
		}
		network.AssertTerminalRestored()
	})
}

func spinnerVisible(view string) bool {
	for _, frame := range []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏", "|", "/", "-"} {
		if strings.Contains(view, frame) {
			return true
		}
	}
	return false
}

func recoverAuthorizeURL(t *testing.T, view string) string {
	t.Helper()
	clean := strings.ReplaceAll(stripSGR(view), "\r", "")
	start := strings.Index(clean, "http://")
	if start < 0 {
		start = strings.Index(clean, "https://")
	}
	if start < 0 {
		t.Fatalf("authorize URL missing: %q", view)
	}
	rest := clean[start:]
	var b strings.Builder
	for line := range strings.SplitSeq(rest, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, " ") {
			if b.Len() > 0 {
				break
			}
			continue
		}
		b.WriteString(line)
		if strings.Contains(line, "scope=") {
			break
		}
	}
	return b.String()
}

func stripSGR(s string) string {
	return sgrPattern.ReplaceAllString(s, "")
}

func selectionKept(view, name string) bool {
	return strings.Contains(stripSGR(view), "> "+name)
}

func osReadConfig(dir string) (string, error) {
	cfg, err := config.Load(dir)
	if err != nil {
		return "", err
	}
	return cfg.AccountID + cfg.Bucket + cfg.S3AccessKeyID + cfg.URLBase, nil
}
