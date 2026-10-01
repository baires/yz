package e2e

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baires/yz/e2e/fakes"
	"github.com/baires/yz/internal/config"
)

// browserClient does not follow redirects: the test inspects the 302 from
// the fake authorize endpoint, then follows it to yz's callback itself.
var browserClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// setupRun tracks an in-flight `yz setup` process while the test plays the
// browser: stderr is scanned live for the authorize URL.
type setupRun struct {
	cmd     *exec.Cmd
	urlCh   chan string
	drained chan struct{}
	pipe    *io.PipeWriter
	mu      sync.Mutex
	stdout  bytes.Buffer
	stderr  bytes.Buffer
}

// startSetup launches `yz setup` with the same env handling as runYz.
func startSetup(t *testing.T, env map[string]string) *setupRun {
	input := ""
	if env["YZ_API_BASE_URL"] != "" {
		input = fakes.SetupInput
	}
	return startSetupInput(t, env, input)
}

func startSetupInput(t *testing.T, env map[string]string, input string) *setupRun {
	t.Helper()
	merged := make(map[string]string, len(env)+1)
	maps.Copy(merged, env)
	if _, ok := merged["YZ_CONFIG_DIR"]; !ok {
		merged["YZ_CONFIG_DIR"] = t.TempDir()
	}
	cmdEnv := os.Environ()
	for k, v := range merged {
		cmdEnv = append(cmdEnv, k+"="+v)
	}
	pr, pw := io.Pipe()
	cmd := exec.Command(binPath, "setup")
	cmd.Env = cmdEnv
	cmd.Stdin = strings.NewReader(input)
	sr := &setupRun{cmd: cmd, urlCh: make(chan string, 1), drained: make(chan struct{}), pipe: pw}
	cmd.Stdout = &sr.stdout
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		t.Fatalf("starting yz setup: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = pw.Close()
		_ = pr.Close()
	})
	go func() {
		defer close(sr.drained)
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := scanner.Text()
			sr.mu.Lock()
			sr.stderr.WriteString(line + "\n")
			sr.mu.Unlock()
			if strings.HasPrefix(line, "http") {
				sr.urlCh <- line
			}
		}
	}()
	return sr
}

// authorizeURL waits for yz to print the authorize URL on stderr.
func (sr *setupRun) authorizeURL(t *testing.T) string {
	t.Helper()
	select {
	case u := <-sr.urlCh:
		return u
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the authorize URL on stderr")
		return ""
	}
}

// wait collects the process result once setup exits.
func (sr *setupRun) wait(t *testing.T) runResult {
	t.Helper()
	err := sr.cmd.Wait()
	_ = sr.pipe.Close()
	<-sr.drained
	sr.mu.Lock()
	defer sr.mu.Unlock()
	res := runResult{Stdout: sr.stdout.String(), Stderr: sr.stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		res.ExitCode = 0
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		t.Fatalf("waiting for yz setup: %v", err)
	}
	return res
}

// playBrowser follows the authorize URL the way a real browser would:
// GET the URL (expecting the 302 from the fake), then GET the redirect
// target (yz's localhost callback). The final status is returned so
// failure scenarios can assert on it without this helper failing them.
func playBrowser(t *testing.T, sr *setupRun) int {
	t.Helper()
	resp, err := browserClient.Get(sr.authorizeURL(t))
	if err != nil {
		t.Fatalf("fetching authorize URL: %v", err)
	}
	loc := resp.Header.Get("Location")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound || loc == "" {
		t.Fatalf("authorize response = %d with Location %q, want 302", resp.StatusCode, loc)
	}
	cb, err := browserClient.Get(loc)
	if err != nil {
		t.Fatalf("following callback redirect: %v", err)
	}
	defer func() { _ = cb.Body.Close() }()
	return cb.StatusCode
}

func assertNoConfig(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, config.FileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("config file exists in %q, want none", dir)
	}
}

// loadSavedConfig asserts the saved config parses, validates, and passes the
// 0600 permission gate (config.Load enforces all three), then returns it.
func loadSavedConfig(t *testing.T, dir string) *config.Config {
	t.Helper()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("saved config does not load: %v", err)
	}
	return cfg
}

// assertConfigDirClean checks the deferred save invariants: no leftover temp
// files, dir mode 0700, file mode 0600.
func assertConfigDirClean(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading config dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != config.FileName {
			t.Errorf("unexpected file %q in config dir (leftover temp file?)", e.Name())
		}
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("config dir mode = %o, want 700", info.Mode().Perm())
	}
	finfo, err := os.Stat(filepath.Join(dir, config.FileName))
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	if finfo.Mode().Perm() != 0o600 {
		t.Errorf("config file mode = %o, want 600", finfo.Mode().Perm())
	}
}

// seedConfig writes a token-bearing config via the production save path.
func seedConfig(t *testing.T, dir, access, refresh string, expiry time.Time) {
	t.Helper()
	if err := config.Save(dir, &config.Config{
		AccessToken:  access,
		RefreshToken: refresh,
		TokenExpiry:  expiry,
	}); err != nil {
		t.Fatalf("seeding config: %v", err)
	}
}

func holdPort(t *testing.T, port string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatalf("holding port %s: %v", port, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
}

func oauthEnv(t *testing.T, dir string, fake *fakes.OAuthServer) map[string]string {
	t.Helper()
	api := fakes.NewCFAPI(t)
	api.ManagedEnabled = true
	s3 := fakes.NewCredentialServer(t)
	return map[string]string{
		"YZ_CONFIG_DIR": dir, "YZ_OAUTH_BASE_URL": fake.URL(),
		"YZ_API_BASE_URL": api.URL(), "YZ_R2_ENDPOINT": s3.URL(),
	}
}

func TestHeadlessLogin(t *testing.T) {
	scenario(t, "headless login", func(t *testing.T) {
		fake := fakes.NewOAuthServer(t)
		fake.SetExpiresIn(600)
		dir := filepath.Join(t.TempDir(), "yz")
		sr := startSetup(t, oauthEnv(t, dir, fake))
		if status := playBrowser(t, sr); status != http.StatusOK {
			t.Errorf("callback status = %d, want 200", status)
		}
		res := sr.wait(t)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		if res.Stdout != "" {
			t.Errorf("stdout = %q, want empty", res.Stdout)
		}
		if !bytes.Contains([]byte(res.Stderr), []byte("logged in")) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "logged in")
		}
		cfg := loadSavedConfig(t, dir)
		access, refresh := fake.LastTokens()
		if cfg.AccessToken == "" || cfg.AccessToken != access {
			t.Errorf("config access token = %q, want fake-issued %q", cfg.AccessToken, access)
		}
		if cfg.RefreshToken == "" || cfg.RefreshToken != refresh {
			t.Errorf("config refresh token = %q, want fake-issued %q", cfg.RefreshToken, refresh)
		}
		if d := time.Until(cfg.TokenExpiry); d < 550*time.Second || d > 650*time.Second {
			t.Errorf("token expires in %v, want ~600s (from expires_in)", d)
		}
		if fake.AuthorizeCount() != 1 {
			t.Errorf("authorize count = %d, want 1", fake.AuthorizeCount())
		}
		assertConfigDirClean(t, dir)
	})
}

func TestHeadlessLoginClientIDOverride(t *testing.T) {
	scenario(t, "headless login with YZ_OAUTH_CLIENT_ID override", func(t *testing.T) {
		fake := fakes.NewOAuthServer(t)
		fake.ExpectClientID("fork-registered-client-id")
		dir := filepath.Join(t.TempDir(), "yz")
		env := oauthEnv(t, dir, fake)
		env["YZ_OAUTH_CLIENT_ID"] = "fork-registered-client-id"
		sr := startSetup(t, env)
		if status := playBrowser(t, sr); status != http.StatusOK {
			t.Errorf("callback status = %d, want 200", status)
		}
		res := sr.wait(t)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		if fake.AuthorizeCount() != 1 {
			t.Errorf("authorize count = %d, want 1", fake.AuthorizeCount())
		}
		assertConfigDirClean(t, dir)
	})
}

func TestSetupConfigDirUnwritable(t *testing.T) {
	scenario(t, "config dir not creatable on save", func(t *testing.T) {
		parent := t.TempDir()
		if err := os.Chmod(parent, 0o500); err != nil {
			t.Fatalf("chmod parent: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
		dir := filepath.Join(parent, "yz")
		fake := fakes.NewOAuthServer(t)
		sr := startSetup(t, oauthEnv(t, dir, fake))
		playBrowser(t, sr)
		res := sr.wait(t)
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		if !bytes.Contains([]byte(res.Stderr), []byte("config dir")) {
			t.Errorf("stderr = %q, want it to name the config dir failure", res.Stderr)
		}
	})
}

func TestLoginDenied(t *testing.T) {
	scenario(t, "login denied by user", func(t *testing.T) {
		fake := fakes.NewOAuthServer(t)
		fake.Deny()
		dir := t.TempDir()
		sr := startSetup(t, oauthEnv(t, dir, fake))
		playBrowser(t, sr)
		res := sr.wait(t)
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		if !bytes.Contains([]byte(res.Stderr), []byte("denied")) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "denied")
		}
		if n := len(fake.TokenRequests()); n != 0 {
			t.Errorf("token requests after denial = %d, want 0", n)
		}
		assertNoConfig(t, dir)
	})
}

func TestLoginStateMismatch(t *testing.T) {
	scenario(t, "state mismatch", func(t *testing.T) {
		fake := fakes.NewOAuthServer(t)
		fake.WrongState()
		dir := t.TempDir()
		sr := startSetup(t, oauthEnv(t, dir, fake))
		playBrowser(t, sr)
		res := sr.wait(t)
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		if !bytes.Contains([]byte(res.Stderr), []byte("state")) {
			t.Errorf("stderr = %q, want it to name the state mismatch", res.Stderr)
		}
		if n := len(fake.TokenRequests()); n != 0 {
			t.Errorf("token requests after state mismatch = %d, want 0", n)
		}
		assertNoConfig(t, dir)
	})
}

func TestLoginCallbackNoCode(t *testing.T) {
	scenario(t, "callback with no code", func(t *testing.T) {
		fake := fakes.NewOAuthServer(t)
		fake.NoCode()
		dir := t.TempDir()
		sr := startSetup(t, oauthEnv(t, dir, fake))
		playBrowser(t, sr)
		res := sr.wait(t)
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		assertNoConfig(t, dir)
	})
}

func TestLoginTokenRejected400(t *testing.T) {
	scenario(t, "token endpoint 4xx", func(t *testing.T) {
		fake := fakes.NewOAuthServer(t)
		fake.FailToken(http.StatusBadRequest)
		dir := t.TempDir()
		sr := startSetup(t, oauthEnv(t, dir, fake))
		playBrowser(t, sr)
		res := sr.wait(t)
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		if !bytes.Contains([]byte(res.Stderr), []byte("run yz setup")) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "run yz setup")
		}
		assertNoConfig(t, dir)
	})
}

func TestLoginToken500(t *testing.T) {
	scenario(t, "token endpoint 5xx", func(t *testing.T) {
		fake := fakes.NewOAuthServer(t)
		fake.FailToken(http.StatusInternalServerError)
		dir := t.TempDir()
		sr := startSetup(t, oauthEnv(t, dir, fake))
		playBrowser(t, sr)
		res := sr.wait(t)
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		assertNoConfig(t, dir)
	})
}

func TestLoginTokenUnreachable(t *testing.T) {
	scenario(t, "token endpoint unreachable", func(t *testing.T) {
		fake := fakes.NewOAuthServer(t)
		dir := t.TempDir()
		sr := startSetup(t, oauthEnv(t, dir, fake))
		// Authorize against the live fake, then kill it before yz exchanges
		// the code: the exchange hits connection refused.
		resp, err := browserClient.Get(sr.authorizeURL(t))
		if err != nil {
			t.Fatalf("fetching authorize URL: %v", err)
		}
		loc := resp.Header.Get("Location")
		_ = resp.Body.Close()
		fake.Close()
		cb, err := browserClient.Get(loc)
		if err != nil {
			t.Fatalf("following callback redirect: %v", err)
		}
		_ = cb.Body.Close()
		res := sr.wait(t)
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		if !bytes.Contains([]byte(res.Stderr), []byte("reaching cloudflare")) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "reaching cloudflare")
		}
		assertNoConfig(t, dir)
	})
}

func TestLoginAllPortsBusy(t *testing.T) {
	scenario(t, "all callback ports busy", func(t *testing.T) {
		holdPort(t, "8974")
		holdPort(t, "8975")
		holdPort(t, "8976")
		fake := fakes.NewOAuthServer(t)
		dir := t.TempDir()
		res := runYz(t, oauthEnv(t, dir, fake), "setup")
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		if !bytes.Contains([]byte(res.Stderr), []byte("8974")) {
			t.Errorf("stderr = %q, want it to name the port range", res.Stderr)
		}
		if n := fake.AuthorizeCount(); n != 0 {
			t.Errorf("authorize count = %d, want 0 (no login attempted)", n)
		}
		assertNoConfig(t, dir)
	})
}

func TestLoginPortFallback(t *testing.T) {
	scenario(t, "port 8974 busy falls back to 8975", func(t *testing.T) {
		holdPort(t, "8974")
		fake := fakes.NewOAuthServer(t)
		dir := t.TempDir()
		sr := startSetup(t, oauthEnv(t, dir, fake))
		playBrowser(t, sr)
		res := sr.wait(t)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		if got := fake.LastRedirectURI(); got != "http://127.0.0.1:8975/callback" {
			t.Errorf("redirect_uri = %q, want the 8975 callback", got)
		}
		loadSavedConfig(t, dir)
	})
}

func TestSetupAlreadyLoggedIn(t *testing.T) {
	scenario(t, "re-run setup with valid tokens", func(t *testing.T) {
		fake := fakes.NewOAuthServer(t)
		dir := t.TempDir()
		seedConfig(t, dir, "seed-access", "seed-refresh", time.Now().Add(time.Hour))
		res := runYz(t, oauthEnv(t, dir, fake), "setup")
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		if !bytes.Contains([]byte(res.Stderr), []byte("already logged in")) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "already logged in")
		}
		if n := fake.AuthorizeCount(); n != 0 {
			t.Errorf("authorize count = %d, want 0 (no re-login)", n)
		}
		if n := len(fake.TokenRequests()); n != 0 {
			t.Errorf("token requests = %d, want 0", n)
		}
	})
}

func TestSetupRefreshExpired(t *testing.T) {
	scenario(t, "expired access token triggers transparent refresh", func(t *testing.T) {
		fake := fakes.NewOAuthServer(t)
		fake.SeedRefreshToken("rt-seed")
		dir := t.TempDir()
		seedConfig(t, dir, "old-access", "rt-seed", time.Now().Add(-time.Hour))
		res := runYz(t, oauthEnv(t, dir, fake), "setup")
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		if !bytes.Contains([]byte(res.Stderr), []byte("refreshed")) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "refreshed")
		}
		if n := fake.AuthorizeCount(); n != 0 {
			t.Errorf("authorize count = %d, want 0 (no re-login)", n)
		}
		reqs := fake.TokenRequests()
		if len(reqs) != 1 || reqs[0].GrantType != "refresh_token" || reqs[0].RefreshToken != "rt-seed" {
			t.Errorf("token requests = %+v, want one refresh_token grant with rt-seed", reqs)
		}
		cfg := loadSavedConfig(t, dir)
		access, refresh := fake.LastTokens()
		if cfg.AccessToken != access || cfg.AccessToken == "old-access" {
			t.Errorf("config access token = %q, want refreshed %q", cfg.AccessToken, access)
		}
		if cfg.RefreshToken != refresh || cfg.RefreshToken == "rt-seed" {
			t.Errorf("config refresh token = %q, want rotated %q", cfg.RefreshToken, refresh)
		}
		if time.Until(cfg.TokenExpiry) <= 0 {
			t.Errorf("token expiry %v is not in the future", cfg.TokenExpiry)
		}
	})
}

func TestSetupRefreshRevoked(t *testing.T) {
	scenario(t, "revoked refresh token falls back to full login", func(t *testing.T) {
		fake := fakes.NewOAuthServer(t)
		dir := t.TempDir()
		seedConfig(t, dir, "old-access", "rt-revoked", time.Now().Add(-time.Hour))
		sr := startSetup(t, oauthEnv(t, dir, fake))
		playBrowser(t, sr)
		res := sr.wait(t)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		if !bytes.Contains([]byte(res.Stderr), []byte("starting a new login")) {
			t.Errorf("stderr = %q, want it to announce the new login", res.Stderr)
		}
		if n := fake.AuthorizeCount(); n != 1 {
			t.Errorf("authorize count = %d, want 1 (fallback login)", n)
		}
		reqs := fake.TokenRequests()
		if len(reqs) == 0 || reqs[0].GrantType != "refresh_token" || reqs[0].RefreshToken != "rt-revoked" {
			t.Errorf("token requests = %+v, want a rejected refresh attempt first", reqs)
		}
		cfg := loadSavedConfig(t, dir)
		access, _ := fake.LastTokens()
		if cfg.AccessToken != access {
			t.Errorf("config access token = %q, want freshly issued %q", cfg.AccessToken, access)
		}
	})
}

func TestSetupSIGINT(t *testing.T) {
	scenario(t, "SIGINT while waiting for callback", func(t *testing.T) {
		fake := fakes.NewOAuthServer(t)
		dir := t.TempDir()
		sr := startSetup(t, oauthEnv(t, dir, fake))
		sr.authorizeURL(t) // ensures the listener is up and waiting
		if err := sr.cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatalf("sending SIGINT: %v", err)
		}
		res := sr.wait(t)
		if res.ExitCode != 130 {
			t.Errorf("exit code = %d, want 130 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		if res.Stdout != "" {
			t.Errorf("stdout = %q, want empty", res.Stdout)
		}
		if !strings.Contains(res.Stderr, "yz: interrupted") {
			t.Errorf("missing interruption diagnostic: %q", res.Stderr)
		}
		holdPort(t, "8974") // cancellation must release the callback listener
		assertNoConfig(t, dir)
	})
}

func TestLoginListenerTimeout(t *testing.T) {
	scenario(t, "listener timeout", func(t *testing.T) {
		fake := fakes.NewOAuthServer(t)
		dir := t.TempDir()
		env := oauthEnv(t, dir, fake)
		env["YZ_LOGIN_TIMEOUT"] = "300ms"
		res := runYz(t, env, "setup")
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		if !bytes.Contains([]byte(res.Stderr), []byte("timed out")) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "timed out")
		}
		holdPort(t, "8974") // timeout must release the callback listener
		assertNoConfig(t, dir)
	})
}

func TestSetupOverwritesCorruptConfig(t *testing.T) {
	scenario(t, "setup overwrites corrupt config", func(t *testing.T) {
		fake := fakes.NewOAuthServer(t)
		dir := t.TempDir()
		writeConfigFile(t, dir, "{not json", 0o600)
		sr := startSetup(t, oauthEnv(t, dir, fake))
		playBrowser(t, sr)
		res := sr.wait(t)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		cfg := loadSavedConfig(t, dir)
		if cfg.AccessToken == "" || cfg.RefreshToken == "" {
			t.Errorf("config after re-setup = %+v, want fresh tokens", cfg)
		}
	})
}

func TestRefreshOutagePreservesConfig(t *testing.T) {
	scenario(t, "refresh outage preserves saved config", func(t *testing.T) {
		fake := fakes.NewOAuthServer(t)
		fake.FailToken(http.StatusInternalServerError)
		dir := t.TempDir()
		seedConfig(t, dir, "old-access", "rt-seed", time.Now().Add(-time.Hour))
		before, err := os.ReadFile(filepath.Join(dir, config.FileName))
		if err != nil {
			t.Fatal(err)
		}
		res := runYz(t, oauthEnv(t, dir, fake), "setup")
		if res.ExitCode != 1 || !strings.Contains(res.Stderr, "HTTP 500") {
			t.Fatalf("refresh outage result = %+v", res)
		}
		if fake.AuthorizeCount() != 0 {
			t.Error("refresh outage started a new login")
		}
		after, err := os.ReadFile(filepath.Join(dir, config.FileName))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Error("failed refresh changed saved config")
		}
	})
}

func TestLoginMalformedToken(t *testing.T) {
	scenario(t, "malformed token response", func(t *testing.T) {
		fake := fakes.NewOAuthServer(t)
		fake.SetExpiresIn(0)
		dir := t.TempDir()
		sr := startSetup(t, oauthEnv(t, dir, fake))
		playBrowser(t, sr)
		res := sr.wait(t)
		if res.ExitCode != 1 || !strings.Contains(res.Stderr, "invalid OAuth token response") {
			t.Fatalf("malformed token result = %+v", res)
		}
		assertNoConfig(t, dir)
	})
}
