package e2e

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTerminalUploadProgress(t *testing.T) {
	scenario(t, "terminal upload shows increasing transfer bytes", func(t *testing.T) {
		_, s3, _, env := shareFixture(t, "https://files.example")
		s3.ReadChunk = 32 * 1024
		s3.ReadDelay = 40 * time.Millisecond
		size := 4 * 1024 * 1024
		path := writeSizedFile(t, "payload.bin", size)
		run := startTerminal(t, env, path)
		view := run.AwaitFor("Confirming upload", 20000)
		sents := sentCounts(view, size)
		if len(sents) < 2 || sents[len(sents)-1] <= sents[0] || sents[len(sents)-1] <= 0 {
			t.Fatalf("progress did not increase within %d: %v", size, sents)
		}
		for _, sent := range sents {
			if sent > int64(size) {
				t.Fatalf("sent %d exceeds file size %d", sent, size)
			}
		}
		run.Interrupt()
		run.Wait()

		empty := writeSizedFile(t, "empty.png", 0)
		emptyRun := startTerminal(t, env, empty)
		emptyView := emptyRun.Await("Uploaded")
		if !strings.Contains(emptyView, "0/0") {
			t.Fatalf("zero-byte view = %q", emptyView)
		}
		if strings.Contains(emptyView, "B/s") {
			t.Fatalf("zero-byte view invented a speed: %q", emptyView)
		}
		emptyRes := emptyRun.Wait()
		if emptyRes.ExitCode != 0 || strings.TrimSpace(emptyRes.Stdout) == "" {
			t.Fatalf("zero-byte result = %+v", emptyRes)
		}

		name := "naïve-\x1b]0;pwned\x07file.png"
		unsafePath := writeSizedFile(t, name, 32)
		unsafeRun := startTerminal(t, env, unsafePath)
		unsafeView := unsafeRun.Await("Uploaded")
		requireNoControls(t, "upload filename", unsafeView)
		if !strings.Contains(unsafeView, "naïve") || !strings.Contains(unsafeView, "file.png") {
			t.Fatalf("readable filename missing: %q", unsafeView)
		}
		unsafeRes := unsafeRun.Wait()
		if unsafeRes.ExitCode != 0 {
			t.Fatalf("unsafe name exit = %d, stderr %q", unsafeRes.ExitCode, unsafeRes.Stderr)
		}
		if strings.TrimSpace(unsafeRes.Stdout) == "" {
			t.Fatal("redirected stdout missing URL")
		}
		if strings.Contains(unsafeRes.Stderr, strings.TrimSpace(unsafeRes.Stdout)) {
			t.Fatal("URL leaked onto stderr")
		}
	})
}

func TestTerminalUploadAcknowledgement(t *testing.T) {
	scenario(t, "terminal upload waits for server acknowledgement", func(t *testing.T) {
		_, s3, _, env := shareFixture(t, "https://files.example")
		s3.HoldAck()
		s3.ReadChunk = 64 * 1024
		s3.ReadDelay = 40 * time.Millisecond
		path := writeSizedFile(t, "gated.bin", 256*1024)
		sum := fileSHA(t, path)
		run := startTerminal(t, env, path)
		run.AwaitFor("Confirming upload", 20000)
		run.Await("█▄▄▄▄█")
		if _, err := os.Stat(env["YZ_TEST_CLIPBOARD"]); !os.IsNotExist(err) {
			t.Fatalf("clipboard touched before acknowledgement: %v", err)
		}
		stdout, _ := run.Peek()
		if strings.TrimSpace(stdout) != "" {
			t.Fatalf("URL written before acknowledgement: %q", stdout)
		}
		s3.ReleaseAck()
		res := run.Wait()
		if res.ExitCode != 0 {
			t.Fatalf("exit = %d, stderr %q", res.ExitCode, res.Stderr)
		}
		if !strings.Contains(res.Stderr, "Uploaded") {
			t.Fatalf("acknowledged upload missing success feedback: %q", res.Stderr)
		}
		url := strings.TrimSpace(res.Stdout)
		copied, err := os.ReadFile(env["YZ_TEST_CLIPBOARD"])
		if err != nil || string(copied) != url {
			t.Fatalf("acknowledged clipboard = %q, want %q, err %v", copied, url, err)
		}
		if url == "" || strings.Contains(url, "\n") {
			t.Fatalf("stdout = %q, want one URL", res.Stdout)
		}
		if strings.Contains(res.Stderr, url) {
			t.Fatal("URL leaked onto stderr")
		}
		key := strings.TrimPrefix(strings.TrimPrefix(url, "https://files.example/"), "/")
		obj, ok := s3.GetObject(key)
		if !ok {
			t.Fatalf("object %q not stored", key)
		}
		if fmt.Sprintf("%x", sha256.Sum256(obj.Data)) != sum {
			t.Fatal("stored payload differs from the file")
		}
	})
}

func TestTerminalUploadCancel(t *testing.T) {
	scenario(t, "ctrl-c during acknowledgement prints no URL", func(t *testing.T) {
		_, s3, _, env := shareFixture(t, "https://files.example")
		s3.HoldAck()
		path := writeSizedFile(t, "cancel.bin", 128*1024)
		run := startTerminal(t, env, path)
		run.AwaitFor("Confirming upload", 20000)
		run.Interrupt()
		res := run.Wait()
		if res.ExitCode != 130 {
			t.Fatalf("exit = %d, want 130; stderr %q", res.ExitCode, res.Stderr)
		}
		if strings.TrimSpace(res.Stdout) != "" {
			t.Fatalf("stdout = %q, want empty", res.Stdout)
		}
		if strings.Contains(res.Stderr, "Uploaded") {
			t.Fatal("cancelled upload displayed success")
		}
		run.AssertTerminalRestored()
	})
}

func TestTerminalUploadFailure(t *testing.T) {
	scenario(t, "failed upload prints no URL", func(t *testing.T) {
		_, s3, _, env := shareFixture(t, "https://files.example")
		s3.StatusOverride = 500
		path := writeSizedFile(t, "fail.bin", 128)
		run := startTerminal(t, env, path)
		res := run.Wait()
		if res.ExitCode != 1 {
			t.Fatalf("exit = %d, want 1; stderr %q", res.ExitCode, res.Stderr)
		}
		if strings.TrimSpace(res.Stdout) != "" {
			t.Fatalf("stdout = %q, want empty", res.Stdout)
		}
		if strings.Contains(res.Stderr, "https://") {
			t.Fatalf("failure stderr leaked a URL: %q", res.Stderr)
		}
	})
}

func writeSizedFile(t *testing.T, name string, n int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = "abcdefghijklmnopqrstuvwxyz012345"[i%32]
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

var sentPattern = regexp.MustCompile(`(\d+)/(\d+)`)

func sentCounts(view string, total int) []int64 {
	var out []int64
	seen := map[int64]bool{}
	for _, match := range sentPattern.FindAllStringSubmatch(view, -1) {
		den, _ := strconv.ParseInt(match[2], 10, 64)
		if den != int64(total) {
			continue
		}
		num, _ := strconv.ParseInt(match[1], 10, 64)
		if seen[num] {
			continue
		}
		seen[num] = true
		out = append(out, num)
	}
	return out
}
