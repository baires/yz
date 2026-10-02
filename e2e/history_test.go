package e2e

import (
	"strings"
	"testing"
)

func TestListEmpty(t *testing.T) {
	scenario(t, "list with no history reports no shares", func(t *testing.T) {
		_, _, _, env := shareFixture(t, "MOCK_PUBLIC_HOST")

		res := runYz(t, env, "list")
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		if !strings.Contains(res.Stdout, "no shares yet") {
			t.Errorf("stdout = %q, want it to contain %q", res.Stdout, "no shares yet")
		}
	})
}

func TestListShowsPreviousShares(t *testing.T) {
	scenario(t, "list shows previous shares newest first", func(t *testing.T) {
		_, _, _, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		fileDir := t.TempDir()
		first := writeTestFile(t, fileDir, "first.png", []byte("first payload"))
		second := writeTestFile(t, fileDir, "second.png", []byte("second payload"))

		res1 := runYz(t, env, first)
		if res1.ExitCode != 0 {
			t.Fatalf("first share exit code = %d, want 0; stderr: %q", res1.ExitCode, res1.Stderr)
		}
		res2 := runYz(t, env, second)
		if res2.ExitCode != 0 {
			t.Fatalf("second share exit code = %d, want 0; stderr: %q", res2.ExitCode, res2.Stderr)
		}

		res := runYz(t, env, "list")
		if res.ExitCode != 0 {
			t.Fatalf("list exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		firstURL := strings.TrimSpace(res1.Stdout)
		secondURL := strings.TrimSpace(res2.Stdout)
		for _, want := range []string{"first.png", "second.png", firstURL, secondURL} {
			if !strings.Contains(res.Stdout, want) {
				t.Errorf("stdout missing %q:\n%s", want, res.Stdout)
			}
		}
		if strings.Index(res.Stdout, "second.png") > strings.Index(res.Stdout, "first.png") {
			t.Errorf("stdout not newest first:\n%s", res.Stdout)
		}
		if strings.Contains(res.Stdout, "expires") {
			t.Errorf("public shares marked expiring:\n%s", res.Stdout)
		}
	})
}

func TestListShowsExpiry(t *testing.T) {
	scenario(t, "list marks expiring shares", func(t *testing.T) {
		_, _, _, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		fileDir := t.TempDir()
		filePath := writeTestFile(t, fileDir, "secret.txt", []byte("payload"))

		res := runYz(t, env, "--expires=1h", filePath)
		if res.ExitCode != 0 {
			t.Fatalf("share exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}

		res = runYz(t, env, "list")
		if res.ExitCode != 0 {
			t.Fatalf("list exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		if !strings.Contains(res.Stdout, "expires") {
			t.Errorf("stdout = %q, want it to contain %q", res.Stdout, "expires")
		}
	})
}

func TestListShowsFallbackExpiry(t *testing.T) {
	scenario(t, "list marks presigned fallback shares as expiring", func(t *testing.T) {
		_, _, _, env := shareFixture(t, "")
		filePath := writeTestFile(t, t.TempDir(), "private.txt", []byte("payload"))

		res := runYz(t, env, filePath)
		if res.ExitCode != 0 {
			t.Fatalf("share exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}

		res = runYz(t, env, "list")
		if res.ExitCode != 0 {
			t.Fatalf("list exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		if !strings.Contains(res.Stdout, "expires") {
			t.Errorf("stdout = %q, want it to contain %q", res.Stdout, "expires")
		}
	})
}
