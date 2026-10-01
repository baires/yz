package e2e

import (
	"strings"
	"testing"

	"github.com/baires/yz/e2e/fakes"
	"github.com/baires/yz/internal/config"
)

func TestTerminalCredentialRecovery(t *testing.T) {
	scenario(t, "terminal setup recovers invalid and rejected credentials", func(t *testing.T) {
		dir, api, s3, env := credentialSetup(t, true)
		before := loadSavedConfig(t, dir)
		run := startTerminal(t, env, "setup")
		run.Await("Access Key")
		run.Send("zz\r")
		run.Await("32 hexadecimal")
		run.Send("\x15" + fakes.AccessKey + "\r")
		run.Await("Secret Access Key")
		run.Send("short\r")
		run.Await("64 hexadecimal")
		run.Send("\x15" + strings.Repeat("c", 64) + "\r")
		run.Await("credentials rejected")
		run.Await("Replace the rejected credentials")
		kept := loadSavedConfig(t, dir)
		if kept.AccountID != before.AccountID || kept.Bucket != before.Bucket || kept.S3Secret != "" {
			t.Fatalf("rejection changed config: %+v", kept)
		}
		s3.Status = 503
		run.Await("Access Key ID (2):")
		run.Send(fakes.AccessKey + "\r")
		run.Await("Secret Access Key (2):")
		run.Send(fakes.SecretKey + "\r")
		run.Await("Retry")
		s3.Status = 0
		run.Send("j")
		run.Await("> Retry")
		run.Send("\r")
		run.Await("Access Key ID (3):")
		run.Send(fakes.AccessKey + "\r")
		run.Await("Secret Access Key (3):")
		run.Send(fakes.SecretKey + "\r")
		run.Await("yz screenshot.png")
		res := run.Wait()
		if res.ExitCode != 0 {
			t.Fatalf("exit = %d, stderr %q", res.ExitCode, res.Stderr)
		}
		if strings.Contains(res.Stderr, fakes.AccessKey) || strings.Contains(res.Stderr, fakes.SecretKey) {
			t.Fatal("credentials rendered")
		}
		if strings.Contains(res.Stderr, "setupModel") {
			t.Fatal("model dump rendered")
		}
		saved := loadSavedConfig(t, dir)
		if saved.Bucket != "photos" || saved.S3AccessKeyID != fakes.AccessKey {
			t.Fatalf("saved %+v", saved)
		}
		if !strings.Contains(strings.Join(api.Calls(), "\n"), "GET ") {
			t.Fatal("domain discovery missing")
		}
	})
}

func TestTerminalCredentialPaste(t *testing.T) {
	scenario(t, "terminal credential paste is masked and bounded", func(t *testing.T) {
		dir, _, _, env := credentialSetup(t, true)
		run := startTerminal(t, env, "setup")
		run.Await("Access Key")
		leaked := "LEAKED-SECRET-VALUE"
		run.Send(bracketPaste(strings.Repeat("a", 5000) + leaked + "\nsecond-line"))
		run.Send("\r")
		rejected := run.Await("paste")
		if strings.Contains(rejected, leaked) || strings.Contains(rejected, "second-line") {
			t.Fatal("overlong paste leaked")
		}
		if !strings.Contains(rejected, "Access Key") {
			t.Fatal("overlong paste advanced the field")
		}
		run.Send(bracketPaste(fakes.AccessKey))
		run.Send("\r")
		secretView := run.Await("Secret Access Key")
		if strings.Contains(secretView, fakes.AccessKey) {
			t.Fatal("access key was not masked")
		}
		prefix := fakes.SecretKey[:16]
		run.Send(prefix)
		run.Resize(20, 6)
		tiny := run.Await("resize")
		if strings.Contains(tiny, prefix) {
			t.Fatal("resize revealed the secret")
		}
		run.Resize(80, 24)
		run.Send(fakes.SecretKey[16:] + "\r")
		run.Await("yz screenshot.png")
		res := run.Wait()
		if res.ExitCode != 0 {
			t.Fatalf("exit = %d, stderr %q", res.ExitCode, res.Stderr)
		}
		if strings.Contains(res.Stderr, fakes.SecretKey) || strings.Contains(res.Stderr, prefix) {
			t.Fatal("secret rendered")
		}
		if loadSavedConfig(t, dir).S3Secret != fakes.SecretKey {
			t.Fatal("pasted secret was not kept across resize")
		}
	})
}

func TestTerminalPrivateDefault(t *testing.T) {
	scenario(t, "enter on access choice keeps the bucket private", func(t *testing.T) {
		dir, api, s3, env := credentialSetup(t, false)
		run := startTerminal(t, env, "setup")
		submitCredentials(t, run)
		run.Await("Private")
		run.Send("\r")
		view := run.Await("yz screenshot.png")
		if !strings.Contains(strings.ToLower(view), "private") {
			t.Fatalf("summary missing private mode: %q", view)
		}
		res := run.Wait()
		if res.ExitCode != 0 {
			t.Fatalf("exit = %d, stderr %q", res.ExitCode, res.Stderr)
		}
		if loadSavedConfig(t, dir).URLBase != "" {
			t.Fatal("private default stored a public URL")
		}
		for _, call := range api.Calls() {
			if strings.HasPrefix(call, "PUT ") {
				t.Fatalf("private default performed %s", call)
			}
		}
		if len(s3.Calls()) == 0 || !strings.HasPrefix(s3.Calls()[0], "HEAD ") {
			t.Fatalf("S3 calls = %v", s3.Calls())
		}
	})
}

func TestTerminalPublicConfirmation(t *testing.T) {
	scenario(t, "only an explicit public choice enables r2.dev", func(t *testing.T) {
		dir, api, s3, env := credentialSetup(t, false)
		run := startTerminal(t, env, "setup")
		submitCredentials(t, run)
		run.Await("Private")
		run.Send("j")
		run.Await("> Public")
		run.Send("\r")
		run.Await("yz screenshot.png")
		res := run.Wait()
		if res.ExitCode != 0 {
			t.Fatalf("exit = %d, stderr %q", res.ExitCode, res.Stderr)
		}
		if loadSavedConfig(t, dir).URLBase != "https://pub-test.r2.dev" {
			t.Fatalf("public URL = %q", loadSavedConfig(t, dir).URLBase)
		}
		calls := api.Calls()
		putAt := -1
		for i, call := range calls {
			if strings.HasPrefix(call, "PUT ") {
				putAt = i
			}
		}
		if putAt < 0 {
			t.Fatalf("public selection performed no PUT: %v", calls)
		}
		if len(s3.Calls()) == 0 || !strings.HasPrefix(s3.Calls()[0], "HEAD ") {
			t.Fatal("public PUT happened without a credential HEAD")
		}
	})
}

func TestTerminalSetupSummary(t *testing.T) {
	scenario(t, "terminal setup summary omits secrets", func(t *testing.T) {
		dir, _, _, env := credentialSetup(t, true)
		run := startTerminal(t, env, "setup")
		submitCredentials(t, run)
		view := run.Await("yz screenshot.png")
		if !strings.Contains(view, "photos") || !strings.Contains(view, fakes.AccountID) {
			t.Fatalf("summary missing account or bucket: %q", view)
		}
		res := run.Wait()
		if res.ExitCode != 0 || res.Stdout != "" {
			t.Fatalf("result = %+v", res)
		}
		if strings.Contains(res.Stderr, fakes.AccessKey) || strings.Contains(res.Stderr, fakes.SecretKey) || strings.Contains(res.Stderr, "setupModel") {
			t.Fatal("summary leaked a secret or model dump")
		}
		saved := loadSavedConfig(t, dir)
		if saved.Bucket != "photos" || saved.URLBase != "https://pub-test.r2.dev" {
			t.Fatalf("saved %+v", saved)
		}
	})
}

func credentialSetup(t *testing.T, managed bool) (string, *fakes.CFAPI, *fakes.CredentialServer, map[string]string) {
	t.Helper()
	dir, api, s3, env := setupFixture(t)
	cfg := loadSavedConfig(t, dir)
	cfg.Bucket = "photos"
	cfg.S3AccessKeyID = ""
	cfg.S3Secret = ""
	if err := config.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	api.Configure(func(a *fakes.CFAPI) { a.ManagedEnabled = managed })
	return dir, api, s3, env
}

func submitCredentials(t *testing.T, run *terminalRun) {
	t.Helper()
	run.Await("Access Key")
	run.Send(fakes.AccessKey + "\r")
	run.Await("Secret Access Key")
	run.Send(fakes.SecretKey + "\r")
}

func bracketPaste(s string) string {
	return "\x1b[200~" + s + "\x1b[201~"
}
