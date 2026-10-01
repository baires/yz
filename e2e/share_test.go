package e2e

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/baires/yz/e2e/fakes"
	"github.com/baires/yz/internal/config"
)

var keyPattern = regexp.MustCompile(`^[0-9a-zA-Z]{16}/[a-zA-Z0-9._-]+$`)

func shareFixture(t *testing.T, urlBase string) (string, *fakes.S3Server, *httptest.Server, map[string]string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "yz")
	if err := config.Save(dir, &config.Config{
		AccountID:     fakes.AccountID,
		Bucket:        "photos",
		URLBase:       urlBase,
		S3AccessKeyID: fakes.AccessKey,
		S3Secret:      fakes.SecretKey,
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	s3 := fakes.NewS3Server(t)
	pubHost := fakes.NewPublicHost(t, s3)

	finalBase := urlBase
	if finalBase == "MOCK_PUBLIC_HOST" {
		finalBase = pubHost.URL
		// Update config with the real public host URL
		if err := config.Save(dir, &config.Config{
			AccountID:     fakes.AccountID,
			Bucket:        "photos",
			URLBase:       finalBase,
			S3AccessKeyID: fakes.AccessKey,
			S3Secret:      fakes.SecretKey,
		}); err != nil {
			t.Fatalf("updating config with public host: %v", err)
		}
	}

	env := map[string]string{
		"YZ_CONFIG_DIR":  dir,
		"YZ_R2_ENDPOINT": s3.URL(),
	}
	fakeDesktop(t, env)
	return dir, s3, pubHost, env
}

func TestSharePublicURL(t *testing.T) {
	scenario(t, "share prints public URL and fetches byte-identical payload", func(t *testing.T) {
		_, _, pubHost, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		fileDir := t.TempDir()
		payload := []byte("public share payload bytes")
		filePath := writeTestFile(t, fileDir, "file.png", payload)

		res := runYz(t, env, filePath)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)
		if rawURL == "" {
			t.Fatal("stdout is empty, want public URL")
		}

		u, err := url.Parse(rawURL)
		if err != nil {
			t.Fatalf("parsing URL %q: %v", rawURL, err)
		}

		pubURL, _ := url.Parse(pubHost.URL)
		if u.Host != pubURL.Host {
			t.Fatalf("URL host = %q, want %q", u.Host, pubURL.Host)
		}

		key := strings.TrimPrefix(u.Path, "/")
		if !keyPattern.MatchString(key) {
			t.Fatalf("key = %q does not match expected format <16 base62>/<name>", key)
		}

		// Fetch from fake public host
		resp, err := http.Get(rawURL)
		if err != nil {
			t.Fatalf("GET public URL: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("reading body: %v", err)
		}
		if !bytes.Equal(body, payload) {
			t.Fatalf("downloaded payload mismatch")
		}

		recordManifest("public-share.png", fmt.Sprintf("%x", sha256.Sum256(body)))
	})
}

func TestShareSignedFlag(t *testing.T) {
	scenario(t, "share --signed forces presigned URL even when public URLBase exists", func(t *testing.T) {
		_, s3, _, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		fileDir := t.TempDir()
		payload := []byte("signed share payload")
		filePath := writeTestFile(t, fileDir, "doc.pdf", payload)

		res := runYz(t, env, "--signed", filePath)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)

		u, err := url.Parse(rawURL)
		if err != nil {
			t.Fatalf("parsing URL: %v", err)
		}

		// Must point to S3 endpoint with SigV4 query parameters
		s3URL, _ := url.Parse(s3.URL())
		if u.Host != s3URL.Host {
			t.Errorf("host = %q, want S3 host %q", u.Host, s3URL.Host)
		}
		if u.Query().Get("X-Amz-Signature") == "" {
			t.Errorf("URL missing X-Amz-Signature query param: %q", rawURL)
		}

		resp, err := http.Get(rawURL)
		if err != nil {
			t.Fatalf("GET signed URL: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !bytes.Equal(body, payload) {
			t.Fatalf("payload mismatch")
		}
	})
}

func TestShareExpiresFlag(t *testing.T) {
	scenario(t, "share --expires=1h implies presigning and enforces expiry", func(t *testing.T) {
		_, s3, _, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		fileDir := t.TempDir()
		payload := []byte("expires share payload")
		filePath := writeTestFile(t, fileDir, "secret.txt", payload)

		res := runYz(t, env, "--expires=1h", filePath)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)

		u, err := url.Parse(rawURL)
		if err != nil {
			t.Fatalf("parsing URL: %v", err)
		}

		if u.Query().Get("X-Amz-Expires") != "3600" {
			t.Errorf("X-Amz-Expires = %q, want 3600", u.Query().Get("X-Amz-Expires"))
		}

		// Advance clock 1 hour + 1 second
		s3.Advance(3601 * time.Second)

		resp, err := http.Get(rawURL)
		if err != nil {
			t.Fatalf("GET expired URL: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 Forbidden", resp.StatusCode)
		}
	})
}

func TestShareDomainOverride(t *testing.T) {
	scenario(t, "share --domain overrides configured URLBase", func(t *testing.T) {
		_, s3, _, env := shareFixture(t, "https://pub-default.r2.dev")
		fileDir := t.TempDir()
		payload := []byte("domain override payload")
		filePath := writeTestFile(t, fileDir, "photo.jpg", payload)

		res := runYz(t, env, "--domain=cdn.example.com", filePath)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)

		if !strings.HasPrefix(rawURL, "https://cdn.example.com/") {
			t.Fatalf("URL = %q, want prefix https://cdn.example.com/", rawURL)
		}

		u, err := url.Parse(rawURL)
		if err != nil {
			t.Fatalf("parsing URL: %v", err)
		}
		key := strings.TrimPrefix(u.Path, "/")
		if !keyPattern.MatchString(key) {
			t.Fatalf("key = %q does not match expected format", key)
		}

		// Verify object exists in fake S3
		if _, ok := s3.GetObject(key); !ok {
			t.Fatalf("object with key %q not found in S3", key)
		}
	})
}

func TestSharePrivateBucketFallback(t *testing.T) {
	scenario(t, "share with no URLBase falls back to presigned URL", func(t *testing.T) {
		_, s3, _, env := shareFixture(t, "") // empty URLBase
		fileDir := t.TempDir()
		payload := []byte("private bucket payload")
		filePath := writeTestFile(t, fileDir, "private.txt", payload)

		res := runYz(t, env, filePath)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)

		u, err := url.Parse(rawURL)
		if err != nil {
			t.Fatalf("parsing URL: %v", err)
		}

		s3URL, _ := url.Parse(s3.URL())
		if u.Host != s3URL.Host {
			t.Fatalf("host = %q, want S3 host %q", u.Host, s3URL.Host)
		}
		if u.Query().Get("X-Amz-Signature") == "" {
			t.Fatalf("expected presigned URL, got %q", rawURL)
		}
	})
}

func TestShareKeyFormatAndUniqueness(t *testing.T) {
	scenario(t, "consecutive shares produce distinct keys with 16 base62 prefix", func(t *testing.T) {
		_, _, _, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		fileDir := t.TempDir()
		filePath := writeTestFile(t, fileDir, "document.pdf", []byte("same content"))

		res1 := runYz(t, env, filePath)
		res2 := runYz(t, env, filePath)
		if res1.ExitCode != 0 || res2.ExitCode != 0 {
			t.Fatalf("share failed: res1=%+v, res2=%+v", res1, res2)
		}

		u1, _ := url.Parse(strings.TrimSpace(res1.Stdout))
		u2, _ := url.Parse(strings.TrimSpace(res2.Stdout))
		key1 := strings.TrimPrefix(u1.Path, "/")
		key2 := strings.TrimPrefix(u2.Path, "/")

		if !keyPattern.MatchString(key1) {
			t.Errorf("key1 = %q does not match pattern", key1)
		}
		if !keyPattern.MatchString(key2) {
			t.Errorf("key2 = %q does not match pattern", key2)
		}
		if key1 == key2 {
			t.Fatalf("keys should be distinct, but both are %q", key1)
		}
	})
}

func TestShareMissingFile(t *testing.T) {
	scenario(t, "share missing file exits 1 with actionable error", func(t *testing.T) {
		_, _, _, env := shareFixture(t, "MOCK_PUBLIC_HOST")

		res := runYz(t, env, "nonexistent-file.png")
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1", res.ExitCode)
		}
		if res.Stdout != "" {
			t.Errorf("stdout = %q, want empty", res.Stdout)
		}
		if !strings.Contains(res.Stderr, "nonexistent-file.png") {
			t.Errorf("stderr = %q, want it to contain filename", res.Stderr)
		}
	})
}

func TestShareUnreadableFile(t *testing.T) {
	scenario(t, "share unreadable file exits 1 with actionable error", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("skipping unreadable file test as root")
		}
		_, _, _, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		fileDir := t.TempDir()
		filePath := writeTestFile(t, fileDir, "unreadable.txt", []byte("locked"))
		if err := os.Chmod(filePath, 0o000); err != nil {
			t.Fatalf("chmod: %v", err)
		}

		res := runYz(t, env, filePath)
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1", res.ExitCode)
		}
		if res.Stdout != "" {
			t.Errorf("stdout = %q, want empty", res.Stdout)
		}
		if !strings.Contains(res.Stderr, "unreadable.txt") {
			t.Errorf("stderr = %q, want it to contain filename", res.Stderr)
		}
	})
}

func TestShareDirectory(t *testing.T) {
	scenario(t, "share directory exits 1 with error", func(t *testing.T) {
		_, _, _, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		dirPath := t.TempDir()

		res := runYz(t, env, dirPath)
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1", res.ExitCode)
		}
		if res.Stdout != "" {
			t.Errorf("stdout = %q, want empty", res.Stdout)
		}
		if !strings.Contains(res.Stderr, "directory") {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "directory")
		}
	})
}

func TestShareInvalidExpiresFormat(t *testing.T) {
	scenario(t, "share invalid --expires duration exits 2", func(t *testing.T) {
		_, _, _, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		fileDir := t.TempDir()
		filePath := writeTestFile(t, fileDir, "test.txt", []byte("hello"))

		res := runYz(t, env, "--expires=bogus", filePath)
		if res.ExitCode != 2 {
			t.Fatalf("exit code = %d, want 2 (usage error)", res.ExitCode)
		}
		if res.Stdout != "" {
			t.Errorf("stdout = %q, want empty", res.Stdout)
		}
		if !strings.Contains(res.Stderr, "invalid --expires") {
			t.Errorf("stderr = %q, want it to contain invalid --expires", res.Stderr)
		}
	})
}

func TestShareOutOfRangeExpires(t *testing.T) {
	scenario(t, "share out of range --expires duration exits 2", func(t *testing.T) {
		_, _, _, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		fileDir := t.TempDir()
		filePath := writeTestFile(t, fileDir, "test.txt", []byte("hello"))

		for _, exp := range []string{"0s", "-1h", "200h"} {
			res := runYz(t, env, "--expires="+exp, filePath)
			if res.ExitCode != 2 {
				t.Fatalf("--expires=%s exit code = %d, want 2", exp, res.ExitCode)
			}
		}
	})
}

func TestShareIncompleteConfig(t *testing.T) {
	scenario(t, "share with incomplete config exits 1 prompting setup", func(t *testing.T) {
		dir := t.TempDir()
		if err := config.Save(dir, &config.Config{
			AccountID: fakes.AccountID,
			// Missing Bucket, S3AccessKeyID, S3Secret
		}); err != nil {
			t.Fatalf("saving incomplete config: %v", err)
		}
		fileDir := t.TempDir()
		filePath := writeTestFile(t, fileDir, "test.txt", []byte("hello"))

		res := runYz(t, map[string]string{"YZ_CONFIG_DIR": dir}, filePath)
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1", res.ExitCode)
		}
		if !strings.Contains(res.Stderr, "run yz setup") {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "run yz setup")
		}
	})
}
