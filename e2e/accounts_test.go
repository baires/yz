package e2e

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baires/yz/e2e/fakes"
	"github.com/baires/yz/internal/config"
)

const accountsCall = "GET /accounts?per_page=50"

func discoveryFixture(t *testing.T) (string, *fakes.CFAPI, map[string]string) {
	t.Helper()
	dir, api, _, env := setupFixture(t)
	cfg := loadSavedConfig(t, dir)
	cfg.AccountID = ""
	if err := config.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	api.Configure(func(a *fakes.CFAPI) { a.ManagedEnabled = true })
	return dir, api, env
}

func TestAccountDiscovery(t *testing.T) {
	cases := []struct {
		name, input, message string
		configure            func(*fakes.CFAPI)
		calls                []string
	}{
		{name: "single", input: "1\n" + fakes.AccessKey + "\n" + fakes.SecretKey + "\n", calls: append([]string{accountsCall}, domainCalls("photos")...)},
		{name: "multiple", input: "2\n1\n" + fakes.AccessKey + "\n" + fakes.SecretKey + "\n", configure: func(a *fakes.CFAPI) {
			a.Accounts = []map[string]string{{"id": strings.Repeat("2", 32), "name": "Work"}, {"id": fakes.AccountID, "name": "Personal"}}
		}, calls: append([]string{accountsCall}, domainCalls("photos")...)},
		{name: "paged", input: "2\n1\n" + fakes.AccessKey + "\n" + fakes.SecretKey + "\n", configure: func(a *fakes.CFAPI) {
			a.AccountsPaged = true
			a.Accounts = []map[string]string{{"id": strings.Repeat("2", 32), "name": "Work"}, {"id": fakes.AccountID, "name": "Personal"}}
		}, calls: append([]string{accountsCall, "GET /accounts?per_page=50&page=2"}, domainCalls("photos")...)},
		{name: "none", message: "no accessible Cloudflare accounts", configure: func(a *fakes.CFAPI) { a.Accounts = []map[string]string{} }, calls: []string{accountsCall}},
		{name: "invalid_id", message: "invalid Cloudflare account", configure: func(a *fakes.CFAPI) { a.Accounts[0]["id"] = "../bad" }, calls: []string{accountsCall}},
		{name: "unsafe_name", message: "invalid Cloudflare account", configure: func(a *fakes.CFAPI) { a.Accounts[0]["name"] = "name\x1b[2J" }, calls: []string{accountsCall}},
		{name: "selection", message: "invalid account selection", input: "9\n", configure: func(a *fakes.CFAPI) {
			a.Accounts = append(a.Accounts, map[string]string{"id": strings.Repeat("2", 32), "name": "Work"})
		}, calls: []string{accountsCall}},
		{name: "eof", message: "input ended", configure: func(a *fakes.CFAPI) {
			a.Accounts = append(a.Accounts, map[string]string{"id": strings.Repeat("2", 32), "name": "Work"})
		}, calls: []string{accountsCall}},
		{name: "outage", message: "HTTP 500", configure: func(a *fakes.CFAPI) { a.FailPath = accountsCall; a.Status = 500 }, calls: []string{accountsCall}},
		{name: "unsuccessful", message: "Cloudflare API", configure: func(a *fakes.CFAPI) { a.FailPath = accountsCall; a.Unsuccessful = true }, calls: []string{accountsCall}},
		{name: "malformed", message: "invalid Cloudflare", configure: func(a *fakes.CFAPI) { a.FailPath = accountsCall; a.Malformed = true }, calls: []string{accountsCall}},
		{name: "bad_result", message: "invalid Cloudflare", configure: func(a *fakes.CFAPI) { a.FailPath = accountsCall; a.MalformedResult = true }, calls: []string{accountsCall}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scenario(t, "account discovery: "+tc.name, func(t *testing.T) {
				dir, api, env := discoveryFixture(t)
				if tc.configure != nil {
					api.Configure(tc.configure)
				}
				before, err := os.ReadFile(filepath.Join(dir, config.FileName))
				if err != nil {
					t.Fatal(err)
				}
				res := runYzInput(t, env, tc.input, "setup")
				if tc.message == "" {
					assertSetupSuccess(t, res)
					if loadSavedConfig(t, dir).AccountID != fakes.AccountID {
						t.Error("wrong account saved")
					}
					if strings.Contains(res.Stderr, "Account ID (from") {
						t.Error("manual account ID still requested")
					}
					if tc.name == "single" && strings.Contains(res.Stderr, "Choose an account") {
						t.Error("single account not auto-selected")
					}
					if tc.name != "single" && !strings.Contains(res.Stderr, "Work") {
						t.Error("account names missing")
					}
				} else {
					if res.ExitCode != 1 || !strings.Contains(res.Stderr, tc.message) {
						t.Fatalf("result = %+v; want %q", res, tc.message)
					}
					after, err := os.ReadFile(filepath.Join(dir, config.FileName))
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(before, after) {
						t.Error("discovery failure changed config")
					}
				}
				assertCalls(t, api, tc.calls)
			})
		})
	}
}

func TestAccountScopeUpgrade(t *testing.T) {
	scenario(t, "old login reauthorizes for memberships.read", func(t *testing.T) {
		dir, api, env := discoveryFixture(t)
		api.Configure(func(a *fakes.CFAPI) { a.AccountScopeNeeded = true })
		oauth := fakes.NewOAuthServer(t)
		env["YZ_OAUTH_BASE_URL"] = oauth.URL()
		sr := startSetupInput(t, env, "1\n"+fakes.AccessKey+"\n"+fakes.SecretKey+"\n")
		authorize := sr.authorizeURL(t)
		u, err := url.Parse(authorize)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(u.Query().Get("scope"), "memberships.read") {
			t.Error("memberships.read not requested")
		}
		// Return the captured URL to the browser driver.
		sr.urlCh <- authorize
		playBrowser(t, sr)
		assertSetupSuccess(t, sr.wait(t))
		access, _ := oauth.LastTokens()
		if loadSavedConfig(t, dir).AccessToken != access {
			t.Error("new login tokens not persisted")
		}
		assertCalls(t, api, append([]string{accountsCall, accountsCall}, domainCalls("photos")...))
	})
}

func TestAccountScopeStillForbidden(t *testing.T) {
	scenario(t, "account discovery stops after one scope retry", func(t *testing.T) {
		_, api, env := discoveryFixture(t)
		api.Configure(func(a *fakes.CFAPI) { a.FailPath = accountsCall; a.Status = 403 })
		oauth := fakes.NewOAuthServer(t)
		env["YZ_OAUTH_BASE_URL"] = oauth.URL()
		sr := startSetupInput(t, env, "")
		playBrowser(t, sr)
		res := sr.wait(t)
		if res.ExitCode != 1 || !strings.Contains(res.Stderr, "memberships.read") || !strings.Contains(res.Stderr, "registered") {
			t.Fatalf("missing scope remedy: %+v", res)
		}
		if oauth.AuthorizeCount() != 1 {
			t.Error("scope retry was not bounded")
		}
		assertCalls(t, api, []string{accountsCall, accountsCall})
	})
}
