package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/baires/yz/e2e/fakes"
)

func TestTerminalSetupIntroduction(t *testing.T) {
	scenario(t, "first setup explains the tool and prerequisites", func(t *testing.T) {
		oauth := fakes.NewOAuthServer(t)
		run := startTerminal(t, map[string]string{
			"YZ_CONFIG_DIR":     filepath.Join(t.TempDir(), "yz"),
			"YZ_OAUTH_BASE_URL": oauth.URL(),
		}, "setup")
		run.Await("Share files as links")
		run.Await("Cloudflare account")
		run.Await("Object Read & Write")
		run.Await("Open this URL")
		run.Await("█▄▄▄▄█")
		run.Await("↑ ↑")
		run.Interrupt()
		if res := run.Wait(); res.ExitCode != 130 || res.Stdout != "" {
			t.Fatalf("cancelled intro: %+v", res)
		}
		run.AssertTerminalRestored()
	})
}

func TestHelpGettingStarted(t *testing.T) {
	scenario(t, "help offers setup and sharing examples", func(t *testing.T) {
		res := runYz(t, nil, "--help")
		for _, text := range []string{"yz setup", "yz screenshot.png", "--expires=1h"} {
			if !strings.Contains(res.Stderr, text) {
				t.Fatalf("help missing %q: %q", text, res.Stderr)
			}
		}
		if res.ExitCode != 2 || res.Stdout != "" {
			t.Fatalf("usage result: %+v", res)
		}
	})
}

func TestTerminalCredentialFeedback(t *testing.T) {
	scenario(t, "credential feedback preserves input and stays masked", func(t *testing.T) {
		dir, _, _, env := credentialSetup(t, true)
		run := startTerminal(t, env, "setup")
		run.Await("your Access Key ID")
		run.Send("ab")
		run.Await("2/32 characters")
		run.Send("\r")
		run.Await("32 hexadecimal")
		run.Send("c")
		run.Await("3/32 characters")
		run.Send("\x1b[D")
		run.Send("\x15" + fakes.AccessKey)
		run.Await("Ready")
		run.Send("\r")
		run.Await("your Secret Access Key")
		run.Send(fakes.SecretKey[:16])
		run.Await("16/64 characters")
		run.Send(fakes.SecretKey[16:] + "\r")
		run.Await("yz screenshot.png")
		run.Await("★ ★")
		res := run.Wait()
		if res.ExitCode != 0 || res.Stdout != "" {
			t.Fatalf("setup exit = %d, stdout %q, stderr %q", res.ExitCode, res.Stdout, res.Stderr)
		}
		if strings.Contains(res.Stderr, fakes.AccessKey) || strings.Contains(res.Stderr, fakes.SecretKey[:16]) {
			t.Fatal("credential feedback exposed a credential")
		}
		if loadSavedConfig(t, dir).S3Secret != fakes.SecretKey {
			t.Fatal("editing the preserved input did not save the credentials")
		}
		run.AssertTerminalRestored()
	})
}

func TestTerminalEmptySearchRecovery(t *testing.T) {
	scenario(t, "empty bucket search explains how to recover", func(t *testing.T) {
		_, api, env := discoveryFixture(t)
		api.Configure(func(a *fakes.CFAPI) {
			a.BucketNames = []string{"photos", "blog-exports"}
		})
		run := startTerminal(t, env, "setup")
		run.Await("blog-exports")
		run.Send("/nothingmatches")
		run.Await("No matches")
		run.Await("esc clear")
		run.Send("\x1b[27u")
		run.Send("j")
		run.Await("> blog-exports")
		run.Send("\r")
		run.Await("Access Key")
		run.Interrupt()
		if res := run.Wait(); res.ExitCode != 130 {
			t.Fatalf("exit = %d, stderr %q", res.ExitCode, res.Stderr)
		}
		run.AssertTerminalRestored()
	})
}
