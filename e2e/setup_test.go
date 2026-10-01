package e2e

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/baires/yz/e2e/fakes"
	"github.com/baires/yz/internal/config"
)

const bucketPath = "/accounts/" + fakes.AccountID + "/r2/buckets"

func setupFixture(t *testing.T) (string, *fakes.CFAPI, *fakes.CredentialServer, map[string]string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "yz")
	seedConfig(t, dir, "seed-access", "seed-refresh", time.Now().Add(time.Hour))
	cfg := loadSavedConfig(t, dir)
	cfg.AccountID = fakes.AccountID
	if err := config.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	api := fakes.NewCFAPI(t)
	s3 := fakes.NewCredentialServer(t)
	env := map[string]string{
		"YZ_CONFIG_DIR": dir, "YZ_API_BASE_URL": api.URL(), "YZ_R2_ENDPOINT": s3.URL(),
	}
	return dir, api, s3, env
}

func domainCalls(bucket string) []string {
	return []string{
		"GET " + bucketPath, "GET " + bucketPath + "/" + bucket + "/domains/custom",
		"GET " + bucketPath + "/" + bucket + "/domains/managed",
	}
}

func assertCalls(t *testing.T, api *fakes.CFAPI, want []string) {
	t.Helper()
	if got := api.Calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("API calls = %v, want %v", got, want)
	}
}

func assertSetupSuccess(t *testing.T, res runResult) {
	t.Helper()
	if res.ExitCode != 0 || res.Stdout != "" || !strings.Contains(res.Stderr, "setup complete") {
		t.Fatalf("setup result = %+v", res)
	}
	if strings.Contains(res.Stderr, fakes.SecretKey) || strings.Contains(res.Stderr, fakes.AccessKey) {
		t.Fatal("credentials leaked into output")
	}
}

func TestSetupManagedDomain(t *testing.T) {
	scenario(t, "setup enables r2.dev after confirmation", func(t *testing.T) {
		dir, api, s3, env := setupFixture(t)
		res := runYzInput(t, env, fakes.SetupInput, "setup")
		assertSetupSuccess(t, res)
		cfg := loadSavedConfig(t, dir)
		if cfg.AccountID != fakes.AccountID || cfg.Bucket != "photos" || cfg.URLBase != "https://pub-test.r2.dev" ||
			cfg.S3AccessKeyID != fakes.AccessKey || cfg.S3Secret != fakes.SecretKey {
			t.Fatal("R2 config does not match chosen settings")
		}
		assertCalls(t, api, append(domainCalls("photos"), "PUT "+bucketPath+"/photos/domains/managed"))
		if !reflect.DeepEqual(s3.Calls(), []string{"HEAD /photos"}) {
			t.Errorf("S3 calls = %v", s3.Calls())
		}
		assertConfigDirClean(t, dir)
	})
}

func TestSetupCustomDomain(t *testing.T) {
	scenario(t, "setup prefers active custom domain", func(t *testing.T) {
		dir, api, _, env := setupFixture(t)
		api.CustomDomain, api.CustomEnabled, api.CustomActive = "cdn.example.com", true, true
		api.ManagedEnabled = true
		assertSetupSuccess(t, runYzInput(t, env, fakes.SetupInput, "setup"))
		if loadSavedConfig(t, dir).URLBase != "https://cdn.example.com" {
			t.Error("custom domain not selected")
		}
		assertCalls(t, api, domainCalls("photos"))
	})
}

func TestSetupExistingManagedDomain(t *testing.T) {
	scenario(t, "setup reuses enabled managed domain", func(t *testing.T) {
		dir, api, _, env := setupFixture(t)
		api.ManagedEnabled = true
		assertSetupSuccess(t, runYzInput(t, env, fakes.SetupInput, "setup"))
		if loadSavedConfig(t, dir).URLBase != "https://pub-test.r2.dev" {
			t.Error("managed domain not selected")
		}
		assertCalls(t, api, domainCalls("photos"))
	})
}

func TestSetupInactiveCustomDomain(t *testing.T) {
	for _, mode := range []string{"pending", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			scenario(t, "setup ignores "+mode+" custom domain", func(t *testing.T) {
				dir, api, _, env := setupFixture(t)
				api.CustomDomain = "pending.example.com"
				api.CustomEnabled, api.CustomActive = mode == "pending", mode == "disabled"
				api.ManagedEnabled = true
				assertSetupSuccess(t, runYzInput(t, env, fakes.SetupInput, "setup"))
				if loadSavedConfig(t, dir).URLBase != "https://pub-test.r2.dev" {
					t.Error("inactive custom domain used")
				}
				assertCalls(t, api, domainCalls("photos"))
			})
		})
	}
}

func TestSetupPlainCompatibility(t *testing.T) {
	scenario(t, "plain setup keeps piped answers, saved settings, and call order", func(t *testing.T) {
		dir, api, s3, env := setupFixture(t)
		cfg := loadSavedConfig(t, dir)
		cfg.AccountID = ""
		if err := config.Save(dir, cfg); err != nil {
			t.Fatal(err)
		}
		workID := strings.Repeat("2", 32)
		api.Configure(func(a *fakes.CFAPI) {
			a.Accounts = []map[string]string{
				{"id": workID, "name": "Work"},
				{"id": fakes.AccountID, "name": "Personal"},
			}
			a.BucketNames = []string{"archive", "photos"}
		})
		input := "2\n2\n" + fakes.AccessKey + "\n" + fakes.SecretKey + "\ny\n"
		assertSetupSuccess(t, runYzInput(t, env, input, "setup"))
		saved := loadSavedConfig(t, dir)
		if saved.AccountID != fakes.AccountID || saved.Bucket != "photos" || saved.URLBase != "https://pub-test.r2.dev" {
			t.Fatalf("piped selection saved %+v", saved)
		}
		want := append([]string{accountsCall}, append(domainCalls("photos"), "PUT "+bucketPath+"/photos/domains/managed")...)
		assertCalls(t, api, want)
		if !reflect.DeepEqual(s3.Calls(), []string{"HEAD /photos"}) {
			t.Fatalf("S3 calls = %v", s3.Calls())
		}

		api.Configure(func(a *fakes.CFAPI) { a.ManagedEnabled = true })
		assertSetupSuccess(t, runYzInput(t, env, "", "setup"))
		if !reflect.DeepEqual(loadSavedConfig(t, dir), saved) {
			t.Fatal("saved settings were not reused")
		}

		fresh, declinedAPI, _, declinedEnv := setupFixture(t)
		declined := loadSavedConfig(t, fresh)
		declined.AccountID = ""
		if err := config.Save(fresh, declined); err != nil {
			t.Fatal(err)
		}
		declinedAPI.Configure(func(a *fakes.CFAPI) {
			a.Accounts = []map[string]string{{"id": fakes.AccountID, "name": "Personal"}}
		})
		declineInput := "1\n" + fakes.AccessKey + "\n" + fakes.SecretKey + "\nn\n"
		assertSetupSuccess(t, runYzInput(t, declinedEnv, declineInput, "setup"))
		if loadSavedConfig(t, fresh).URLBase != "" {
			t.Fatal("declined public access stored a URL")
		}
		for _, call := range declinedAPI.Calls() {
			if strings.HasPrefix(call, "PUT ") {
				t.Fatalf("declined public access performed %s", call)
			}
		}
	})
}

func TestSetupDeclinesPublicAccess(t *testing.T) {
	for _, answer := range []string{"n", ""} {
		t.Run("answer_"+answer, func(t *testing.T) {
			scenario(t, "setup leaves bucket private on answer "+answer, func(t *testing.T) {
				dir, api, _, env := setupFixture(t)
				input := "1\n" + fakes.AccessKey + "\n" + fakes.SecretKey + "\n" + answer + "\n"
				assertSetupSuccess(t, runYzInput(t, env, input, "setup"))
				if loadSavedConfig(t, dir).URLBase != "" {
					t.Error("declined public access stored a public URL")
				}
				assertCalls(t, api, domainCalls("photos"))
			})
		})
	}
}

func TestSetupBucketPagination(t *testing.T) {
	scenario(t, "setup selects bucket from second page", func(t *testing.T) {
		dir, api, s3, env := setupFixture(t)
		api.Paged, api.ManagedEnabled = true, true
		api.BucketNames = []string{"archive", "photos"}
		input := strings.Replace(fakes.SetupInput, "1\n", "2\n", 1)
		assertSetupSuccess(t, runYzInput(t, env, input, "setup"))
		if loadSavedConfig(t, dir).Bucket != "photos" {
			t.Error("wrong bucket selected")
		}
		assertCalls(t, api, []string{
			"GET " + bucketPath, "GET " + bucketPath + "?cursor=next",
			"GET " + bucketPath + "/photos/domains/custom", "GET " + bucketPath + "/photos/domains/managed",
		})
		if !reflect.DeepEqual(s3.Calls(), []string{"HEAD /photos"}) {
			t.Error("selected bucket was not validated")
		}
	})
}

func TestSetupFailures(t *testing.T) {
	type failureCase struct {
		name, message, input string
		configure            func(*fakes.CFAPI, *fakes.CredentialServer)
		calls                []string
		head                 bool
	}
	getBuckets := []string{"GET " + bucketPath}
	allDomains := domainCalls("photos")
	withPUT := append(domainCalls("photos"), "PUT "+bucketPath+"/photos/domains/managed")
	cases := []failureCase{
		{name: "invalid_account", message: "account ID", input: "../bad\n", calls: []string{}},
		{name: "no_buckets", message: "create an R2 bucket", configure: func(a *fakes.CFAPI, s *fakes.CredentialServer) { a.BucketNames = []string{} }, calls: getBuckets},
		{name: "invalid_selection", message: "bucket selection", input: "9\n", calls: getBuckets},
		{name: "api_forbidden", message: "HTTP 403", configure: func(a *fakes.CFAPI, s *fakes.CredentialServer) { a.FailPath = "GET " + bucketPath; a.Status = 403 }, calls: getBuckets},
		{name: "api_outage", message: "HTTP 500", configure: func(a *fakes.CFAPI, s *fakes.CredentialServer) { a.FailPath = "GET " + bucketPath; a.Status = 500 }, calls: getBuckets},
		{name: "api_unsuccessful", message: "Cloudflare API", configure: func(a *fakes.CFAPI, s *fakes.CredentialServer) {
			a.FailPath = "GET " + bucketPath
			a.Unsuccessful = true
		}, calls: getBuckets},
		{name: "malformed_json", message: "invalid Cloudflare", configure: func(a *fakes.CFAPI, s *fakes.CredentialServer) { a.FailPath = "GET " + bucketPath; a.Malformed = true }, calls: getBuckets},
		{name: "malformed_result", message: "invalid Cloudflare", configure: func(a *fakes.CFAPI, s *fakes.CredentialServer) {
			a.FailPath = "GET " + bucketPath
			a.MalformedResult = true
		}, calls: getBuckets},
		{name: "invalid_domain", message: "domain", configure: func(a *fakes.CFAPI, s *fakes.CredentialServer) {
			a.ManagedEnabled = true
			a.ManagedDomain = "evil.example/path"
		}, calls: allDomains},
		{name: "malformed_credentials", message: "Access Key ID", input: "1\nbad\n", calls: allDomains},
		{name: "wrong_secret", message: "credentials rejected", input: strings.Replace(fakes.SetupInput, fakes.SecretKey, strings.Repeat("c", 64), 1), calls: allDomains, head: true},
		{name: "wrong_key", message: "credentials rejected", input: strings.Replace(fakes.SetupInput, fakes.AccessKey, strings.Repeat("c", 32), 1), calls: allDomains, head: true},
		{name: "s3_outage", message: "HTTP 503", configure: func(a *fakes.CFAPI, s *fakes.CredentialServer) { s.Status = 503 }, calls: allDomains, head: true},
		{name: "enable_failed", message: "HTTP 500", configure: func(a *fakes.CFAPI, s *fakes.CredentialServer) {
			a.FailPath = "PUT " + bucketPath + "/photos/domains/managed"
			a.Status = 500
		}, calls: withPUT, head: true},
		{name: "enable_ineffective", message: "not enabled", configure: func(a *fakes.CFAPI, s *fakes.CredentialServer) { a.EnableIneffective = true }, calls: withPUT, head: true},
		{name: "input_eof", message: "input ended", input: "", calls: getBuckets},
		{name: "input_too_long", message: "input", input: strings.Repeat("a", 5000) + "\n", calls: getBuckets},
		{name: "repeated_cursor", message: "pagination", configure: func(a *fakes.CFAPI, s *fakes.CredentialServer) {
			a.Paged = true
			a.RepeatCursor = true
			a.BucketNames = []string{"photos", "archive"}
		}, calls: []string{"GET " + bucketPath, "GET " + bucketPath + "?cursor=next"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scenario(t, "setup failure: "+tc.name, func(t *testing.T) {
				dir, api, s3, env := setupFixture(t)
				if tc.name == "invalid_account" {
					cfg := loadSavedConfig(t, dir)
					cfg.AccountID = "../bad"
					if err := config.Save(dir, cfg); err != nil {
						t.Fatal(err)
					}
				}
				if tc.configure != nil {
					tc.configure(api, s3)
				}
				before, err := os.ReadFile(filepath.Join(dir, config.FileName))
				if err != nil {
					t.Fatal(err)
				}
				input := tc.input
				if input == "" && tc.name != "input_eof" {
					input = fakes.SetupInput
				}
				res := runYzInput(t, env, input, "setup")
				if res.ExitCode != 1 || res.Stdout != "" || !strings.Contains(res.Stderr, tc.message) {
					t.Fatalf("result = %+v; want exit 1 and %q", res, tc.message)
				}
				if strings.Contains(res.Stderr, fakes.SecretKey) || strings.Contains(res.Stderr, fakes.AccessKey) {
					t.Error("credentials leaked")
				}
				after, err := os.ReadFile(filepath.Join(dir, config.FileName))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Error("failed setup changed saved config")
				}
				assertCalls(t, api, tc.calls)
				wantHead := []string{}
				if tc.head {
					wantHead = []string{"HEAD /photos"}
				}
				if !reflect.DeepEqual(s3.Calls(), wantHead) {
					t.Errorf("S3 calls = %v, want %v", s3.Calls(), wantHead)
				}
			})
		})
	}
}

func TestSetupRerun(t *testing.T) {
	scenario(t, "setup rerun reuses validated settings", func(t *testing.T) {
		dir, api, s3, env := setupFixture(t)
		api.ManagedEnabled = true
		assertSetupSuccess(t, runYzInput(t, env, fakes.SetupInput, "setup"))
		before := loadSavedConfig(t, dir)
		assertSetupSuccess(t, runYzInput(t, env, "", "setup"))
		after := loadSavedConfig(t, dir)
		if !reflect.DeepEqual(before, after) {
			t.Error("rerun changed config")
		}
		assertCalls(t, api, append(domainCalls("photos"), domainCalls("photos")...))
		if !reflect.DeepEqual(s3.Calls(), []string{"HEAD /photos", "HEAD /photos"}) {
			t.Error("rerun did not revalidate credentials")
		}
	})
}

func TestSetupAPIUnreachable(t *testing.T) {
	scenario(t, "setup API connection failure", func(t *testing.T) {
		dir, api, _, env := setupFixture(t)
		api.Close()
		res := runYzInput(t, env, fakes.SetupInput, "setup")
		if res.ExitCode != 1 || !strings.Contains(res.Stderr, "connection") {
			t.Fatalf("result = %+v", res)
		}
		if loadSavedConfig(t, dir).Bucket != "" {
			t.Error("failed API saved R2 config")
		}
	})
}

func TestSetupFullLogin(t *testing.T) {
	scenario(t, "fresh OAuth login continues through R2 setup", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "yz")
		api := fakes.NewCFAPI(t)
		s3 := fakes.NewCredentialServer(t)
		oauth := fakes.NewOAuthServer(t)
		env := map[string]string{"YZ_CONFIG_DIR": dir, "YZ_API_BASE_URL": api.URL(), "YZ_R2_ENDPOINT": s3.URL(), "YZ_OAUTH_BASE_URL": oauth.URL()}
		sr := startSetupInput(t, env, fakes.SetupInput)
		playBrowser(t, sr)
		assertSetupSuccess(t, sr.wait(t))
		cfg := loadSavedConfig(t, dir)
		access, refresh := oauth.LastTokens()
		if cfg.AccessToken != access || cfg.RefreshToken != refresh || cfg.Bucket != "photos" || cfg.S3Secret != fakes.SecretKey {
			t.Fatal("full login did not persist OAuth and R2 settings together")
		}
		assertConfigDirClean(t, dir)
		assertCalls(t, api, append([]string{accountsCall}, append(domainCalls("photos"), "PUT "+bucketPath+"/photos/domains/managed")...))
	})
}

func TestSetupPromptSIGINT(t *testing.T) {
	scenario(t, "SIGINT while setup waits for input", func(t *testing.T) {
		dir, api, _, env := setupFixture(t)
		before, err := os.ReadFile(filepath.Join(dir, config.FileName))
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(binPath, "setup")
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		inputRead, inputWrite, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = inputRead.Close() }()
		defer func() { _ = inputWrite.Close() }()
		cmd.Stdin = inputRead
		output, err := cmd.StderrPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill() })
		prompt := make(chan struct{})
		go func() {
			var buf [1]byte
			line := ""
			for {
				_, err := output.Read(buf[:])
				if err != nil {
					return
				}
				line += string(buf[:])
				if strings.Contains(line, "Choose a bucket") {
					close(prompt)
					_, _ = io.Copy(io.Discard, output)
					return
				}
			}
		}()
		select {
		case <-prompt:
		case <-time.After(5 * time.Second):
			t.Fatal("setup did not prompt for bucket")
		}
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		err = cmd.Wait()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 130 {
			t.Fatalf("SIGINT result = %v", err)
		}
		after, err := os.ReadFile(filepath.Join(dir, config.FileName))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Error("interrupted setup changed config")
		}
		assertCalls(t, api, []string{"GET " + bucketPath})
	})
}

func TestSetupConfigWriteFailure(t *testing.T) {
	scenario(t, "setup preserves config when file write fails", func(t *testing.T) {
		dir, api, _, env := setupFixture(t)
		api.ManagedEnabled = true
		before, err := os.ReadFile(filepath.Join(dir, config.FileName))
		if err != nil {
			t.Fatal(err)
		}
		// A zero file-size limit makes the real config write fail with EFBIG.
		// The shell only sets the process limit; the compiled binary does setup.
		cmd := exec.Command("sh", "-c", `ulimit -f 0; exec "$1" setup`, "sh", binPath)
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		cmd.Stdin = strings.NewReader(fakes.SetupInput)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err = cmd.Run()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Fatalf("write failure exit = %v; stderr = %q", err, stderr.String())
		}
		if !strings.Contains(stderr.String(), "writing config") || stdout.Len() != 0 {
			t.Fatalf("write failure output = %q", stderr.String())
		}
		after, err := os.ReadFile(filepath.Join(dir, config.FileName))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Error("write failure replaced existing config")
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != config.FileName {
			t.Error("write failure left temporary files")
		}
	})
}
