package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeDesktop(t *testing.T, env map[string]string) {
	t.Helper()
	dir := t.TempDir()
	copyScript := "#!/bin/sh\nif [ \"$YZ_TEST_COPY_FAIL\" = 1 ]; then exit 1; fi\n/bin/cat > \"$YZ_TEST_CLIPBOARD\"\n"
	openScript := "#!/bin/sh\nif [ \"$YZ_TEST_OPEN_FAIL\" = 1 ]; then exit 1; fi\nprintf '%s' \"$1\" > \"$YZ_TEST_OPENED\"\n"
	for _, name := range []string{"pbcopy", "wl-copy", "xclip", "xsel", "clip.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(copyScript), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"open", "xdg-open", "explorer.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(openScript), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env["PATH"] = dir + string(os.PathListSeparator) + os.Getenv("PATH")
	env["YZ_TEST_CLIPBOARD"] = filepath.Join(dir, "clipboard")
	env["YZ_TEST_OPENED"] = filepath.Join(dir, "opened")
}

func TestShareClipboard(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "copied", true: "unavailable"}[fail], func(t *testing.T) {
			_, _, _, env := shareFixture(t, "https://files.example")
			if fail {
				env["YZ_TEST_COPY_FAIL"] = "1"
			}
			res := runYz(t, env, writeSizedFile(t, "copy.png", 32))
			if res.ExitCode != 0 || !strings.HasPrefix(res.Stdout, "https://files.example/") || strings.Count(res.Stdout, "\n") != 1 {
				t.Fatalf("upload result: %+v", res)
			}
			if fail {
				if !strings.Contains(res.Stderr, "Couldn't copy URL to clipboard") || strings.Contains(res.Stderr, "URL copied") {
					t.Fatalf("clipboard failure: %q", res.Stderr)
				}
				return
			}
			copied, err := os.ReadFile(env["YZ_TEST_CLIPBOARD"])
			if err != nil || string(copied) != strings.TrimSpace(res.Stdout) || !strings.Contains(res.Stderr, "URL copied to clipboard") {
				t.Fatalf("clipboard = %q, err %v, result %+v", copied, err, res)
			}
		})
	}
}

func TestTerminalShareOpen(t *testing.T) {
	_, _, _, env := shareFixture(t, "https://files.example")
	env["YZ_TEST_STDOUT_TTY"] = "1"
	run := startTerminal(t, env, writeSizedFile(t, "open.png", 32))
	run.Await("URL copied to clipboard")
	run.Await("o open in browser")
	copied, err := os.ReadFile(env["YZ_TEST_CLIPBOARD"])
	if err != nil {
		t.Fatal(err)
	}
	run.Send("o")
	run.Await("Opened in browser")
	opened, err := os.ReadFile(env["YZ_TEST_OPENED"])
	if err != nil || string(opened) != string(copied) {
		t.Fatalf("opened %q, clipboard %q, err %v", opened, copied, err)
	}
	run.Send("\r")
	if res := run.Wait(); res.ExitCode != 0 {
		t.Fatalf("completion: %+v", res)
	}
	run.AssertTerminalRestored()
}

func TestTerminalShareBrowserFailure(t *testing.T) {
	_, _, _, env := shareFixture(t, "https://files.example")
	env["YZ_TEST_STDOUT_TTY"] = "1"
	env["YZ_TEST_OPEN_FAIL"] = "1"
	env["NO_COLOR"] = "1"
	run := startTerminal(t, env, writeSizedFile(t, "open.png", 32))
	run.Await("o open in browser")
	run.Send("o")
	view := run.Await("Couldn't open browser")
	if colorSequence(view) {
		t.Fatal("NO_COLOR completion emitted color")
	}
	run.Send("\r")
	if res := run.Wait(); res.ExitCode != 0 {
		t.Fatalf("browser failure changed upload exit: %+v", res)
	}
	run.AssertTerminalRestored()
}

func TestShareSignedClipboard(t *testing.T) {
	_, _, _, env := shareFixture(t, "https://files.example")
	res := runYz(t, env, "--expires=1h", writeSizedFile(t, "signed.png", 32))
	copied, err := os.ReadFile(env["YZ_TEST_CLIPBOARD"])
	if res.ExitCode != 0 || err != nil || string(copied) != strings.TrimSpace(res.Stdout) ||
		!strings.Contains(string(copied), "X-Amz-Signature=") {
		t.Fatalf("signed clipboard = %q, err %v, result %+v", copied, err, res)
	}
}

func TestShareFailedUploadDoesNotCopy(t *testing.T) {
	_, _, _, env := shareFixture(t, "https://files.example")
	res := runYz(t, env, filepath.Join(t.TempDir(), "missing.png"))
	_, err := os.Stat(env["YZ_TEST_CLIPBOARD"])
	if res.ExitCode != 1 || res.Stdout != "" || !os.IsNotExist(err) {
		t.Fatalf("failed upload touched clipboard: %+v, stat %v", res, err)
	}
}
