package e2e

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestShareStdin(t *testing.T) {
	scenario(t, "yz - uploads stdin and names the object from the payload", func(t *testing.T) {
		_, _, pubHost, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		payload := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 1, 2, 3}
		res := runYzInput(t, env, string(payload), "-")
		if res.ExitCode != 0 {
			t.Fatalf("exit %d stderr %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)
		if !strings.HasPrefix(rawURL, pubHost.URL+"/") || !strings.HasSuffix(strings.Split(rawURL, "?")[0], "/paste.png") {
			t.Fatalf("url = %q", rawURL)
		}
		resp, err := http.Get(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 || !bytes.Equal(body, payload) {
			t.Fatalf("status %d body %q", resp.StatusCode, body)
		}
	})
}

func TestShareStdinEmpty(t *testing.T) {
	scenario(t, "yz - rejects empty stdin", func(t *testing.T) {
		_, _, _, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		res := runYzInput(t, env, "", "-")
		if res.ExitCode != 1 || !strings.Contains(res.Stderr, "stdin is empty") {
			t.Fatalf("result %+v", res)
		}
	})
}

func TestShareClipboardUpload(t *testing.T) {
	scenario(t, "yz --clipboard uploads a clipboard file on macOS and refuses elsewhere", func(t *testing.T) {
		_, _, pubHost, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		if runtime.GOOS != "darwin" {
			res := runYz(t, env, "--clipboard")
			if res.ExitCode != 1 || !strings.Contains(res.Stderr, "only available on macOS") {
				t.Fatalf("result %+v", res)
			}
			return
		}
		dir := t.TempDir()
		clip := filepath.Join(dir, "notes.txt")
		script := "#!/bin/sh\nprintf '%s' 'clip-bytes' > \"$YZ_CLIP_FILE\"\nprintf 'file:%s\\n' \"$YZ_CLIP_FILE\"\n"
		if err := os.WriteFile(filepath.Join(dir, "osascript"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		env["PATH"] = dir + string(os.PathListSeparator) + env["PATH"]
		env["YZ_CLIP_FILE"] = clip
		res := runYz(t, env, "--clipboard")
		if res.ExitCode != 0 {
			t.Fatalf("exit %d stderr %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)
		if !strings.HasPrefix(rawURL, pubHost.URL+"/") || !strings.HasSuffix(strings.Split(rawURL, "?")[0], "/notes.txt") {
			t.Fatalf("url = %q", rawURL)
		}
		resp, err := http.Get(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "clip-bytes" {
			t.Fatalf("body %q", body)
		}
		if _, err := os.Stat(clip); err != nil {
			t.Fatalf("clipboard source file should be left in place: %v", err)
		}
	})
}

func TestShareBareClipboardImage(t *testing.T) {
	scenario(t, "yz with no args uploads the clipboard image", func(t *testing.T) {
		_, _, pubHost, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		dir := t.TempDir()
		switch runtime.GOOS {
		case "darwin":
			script := "#!/bin/sh\ndir=\nfor a in \"$@\"; do dir=\"$a\"; done\nprintf '\\211PNG\\r\\n\\001\\002' > \"$dir/paste.png\"\nprintf 'wrote:%s\\n' \"$dir/paste.png\"\n"
			if err := os.WriteFile(filepath.Join(dir, "osascript"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
		case "linux":
			script := "#!/bin/sh\nprintf '\\211PNG\\r\\n\\001\\002'\n"
			if err := os.WriteFile(filepath.Join(dir, "wl-paste"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
		default:
			res := runYz(t, env)
			if res.ExitCode != 1 || !strings.Contains(res.Stderr, "clipboard images") {
				t.Fatalf("result %+v", res)
			}
			return
		}
		env["PATH"] = dir + string(os.PathListSeparator) + env["PATH"]
		res := runYz(t, env)
		if res.ExitCode != 0 {
			t.Fatalf("exit %d stderr %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)
		if !strings.HasPrefix(rawURL, pubHost.URL+"/") || !strings.HasSuffix(strings.Split(rawURL, "?")[0], "/paste.png") {
			t.Fatalf("url = %q", rawURL)
		}
	})
}
