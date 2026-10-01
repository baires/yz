package e2e

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baires/yz/e2e/fakes"
	"github.com/baires/yz/internal/config"
)

func seedS3Config(t *testing.T, dir, bucket, key, secret string) {
	t.Helper()
	if err := config.Save(dir, &config.Config{
		AccountID:     fakes.AccountID,
		Bucket:        bucket,
		S3AccessKeyID: key,
		S3Secret:      secret,
	}); err != nil {
		t.Fatalf("seeding config: %v", err)
	}
}

func s3Fixture(t *testing.T) (string, *fakes.S3Server, map[string]string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "yz")
	seedS3Config(t, dir, "photos", fakes.AccessKey, fakes.SecretKey)
	s3 := fakes.NewS3Server(t)
	env := map[string]string{
		"YZ_CONFIG_DIR":  dir,
		"YZ_R2_ENDPOINT": s3.URL(),
	}
	return dir, s3, env
}

func writeTestFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for test file: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}
	return path
}

func TestS3UploadRoundTrip(t *testing.T) {
	scenario(t, "s3 upload then GET via presigned URL roundtrip", func(t *testing.T) {
		_, _, env := s3Fixture(t)
		fileDir := t.TempDir()
		pngPayload := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("image-data-chunk"), 64)...)
		filePath := writeTestFile(t, fileDir, "sample.png", pngPayload)

		res := runYz(t, env, filePath)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)
		if rawURL == "" {
			t.Fatalf("stdout = %q, want presigned URL", res.Stdout)
		}

		resp, err := http.Get(rawURL)
		if err != nil {
			t.Fatalf("GET presigned URL: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("reading response body: %v", err)
		}
		if !bytes.Equal(body, pngPayload) {
			t.Fatalf("downloaded payload mismatch: got %d bytes, want %d bytes", len(body), len(pngPayload))
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
			t.Errorf("Content-Type = %q, want image/png", ct)
		}

		recordManifest("sample.png", fmt.Sprintf("%x", sha256.Sum256(body)))
	})
}

func TestS3EmptyUpload(t *testing.T) {
	scenario(t, "s3 empty file upload and GET", func(t *testing.T) {
		_, _, env := s3Fixture(t)
		fileDir := t.TempDir()
		filePath := writeTestFile(t, fileDir, "empty.txt", []byte{})

		res := runYz(t, env, filePath)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)
		if rawURL == "" {
			t.Fatalf("stdout = %q, want presigned URL", res.Stdout)
		}

		resp, err := http.Get(rawURL)
		if err != nil {
			t.Fatalf("GET presigned URL: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("reading body: %v", err)
		}
		if len(body) != 0 {
			t.Fatalf("body length = %d, want 0", len(body))
		}
		recordManifest("empty.txt", fmt.Sprintf("%x", sha256.Sum256(body)))
	})
}

func TestS3LargeUploadStreaming(t *testing.T) {
	scenario(t, "s3 streamed large upload", func(t *testing.T) {
		_, _, env := s3Fixture(t)
		fileDir := t.TempDir()
		// 5 MB of structured data
		chunk := bytes.Repeat([]byte("0123456789abcdef"), 64) // 1024 bytes
		largePayload := bytes.Repeat(chunk, 5*1024)           // 5 MB
		filePath := writeTestFile(t, fileDir, "large.bin", largePayload)

		res := runYz(t, env, filePath)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)

		resp, err := http.Get(rawURL)
		if err != nil {
			t.Fatalf("GET presigned URL: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("reading body: %v", err)
		}
		if len(body) != len(largePayload) {
			t.Fatalf("body length = %d, want %d", len(body), len(largePayload))
		}
		expectedDigest := sha256.Sum256(largePayload)
		actualDigest := sha256.Sum256(body)
		if expectedDigest != actualDigest {
			t.Fatalf("digest mismatch: got %x, want %x", actualDigest, expectedDigest)
		}
		recordManifest("large.bin", fmt.Sprintf("%x", actualDigest))
	})
}

func TestS3UnicodeSpacesKey(t *testing.T) {
	scenario(t, "s3 upload with unicode and spaces in filename", func(t *testing.T) {
		_, _, env := s3Fixture(t)
		fileDir := t.TempDir()
		payload := []byte("unicode and spaces content 🚀")
		fileName := "hello 🚀 world.txt"
		filePath := writeTestFile(t, fileDir, fileName, payload)

		res := runYz(t, env, filePath)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)

		resp, err := http.Get(rawURL)
		if err != nil {
			t.Fatalf("GET presigned URL: %v", err)
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
			t.Fatalf("payload mismatch")
		}
		recordManifest("unicode-spaces.txt", fmt.Sprintf("%x", sha256.Sum256(body)))
	})
}

func TestS3WrongSecret(t *testing.T) {
	scenario(t, "s3 wrong secret rejected", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "yz")
		wrongSecret := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		seedS3Config(t, dir, "photos", fakes.AccessKey, wrongSecret)
		s3 := fakes.NewS3Server(t)
		env := map[string]string{
			"YZ_CONFIG_DIR":  dir,
			"YZ_R2_ENDPOINT": s3.URL(),
		}
		fileDir := t.TempDir()
		filePath := writeTestFile(t, fileDir, "test.txt", []byte("hello"))

		res := runYz(t, env, filePath)
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1", res.ExitCode)
		}
		if res.Stdout != "" {
			t.Errorf("stdout = %q, want empty", res.Stdout)
		}
		if !strings.Contains(res.Stderr, "R2 credentials rejected") {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "R2 credentials rejected")
		}
	})
}

func TestS3InvalidAccessKey(t *testing.T) {
	scenario(t, "s3 unknown access key rejected", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "yz")
		badKey := "ffffffffffffffffffffffffffffffff"
		seedS3Config(t, dir, "photos", badKey, fakes.SecretKey)
		s3 := fakes.NewS3Server(t)
		env := map[string]string{
			"YZ_CONFIG_DIR":  dir,
			"YZ_R2_ENDPOINT": s3.URL(),
		}
		fileDir := t.TempDir()
		filePath := writeTestFile(t, fileDir, "test.txt", []byte("hello"))

		res := runYz(t, env, filePath)
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1", res.ExitCode)
		}
		if res.Stdout != "" {
			t.Errorf("stdout = %q, want empty", res.Stdout)
		}
		if !strings.Contains(res.Stderr, "R2 credentials rejected") {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "R2 credentials rejected")
		}
	})
}

func TestS3ClockSkew(t *testing.T) {
	scenario(t, "s3 clock skew rejected", func(t *testing.T) {
		_, s3, env := s3Fixture(t)
		// Advance server clock 20 minutes into the future
		s3.Advance(20 * time.Minute)
		fileDir := t.TempDir()
		filePath := writeTestFile(t, fileDir, "test.txt", []byte("hello"))

		res := runYz(t, env, filePath)
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1", res.ExitCode)
		}
		if res.Stdout != "" {
			t.Errorf("stdout = %q, want empty", res.Stdout)
		}
		if !strings.Contains(res.Stderr, "clock is out of sync") {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "clock is out of sync")
		}
	})
}

func TestS3PresignExpiredBoundary(t *testing.T) {
	scenario(t, "s3 presign expired by 1s rejected", func(t *testing.T) {
		_, s3, env := s3Fixture(t)
		fileDir := t.TempDir()
		filePath := writeTestFile(t, fileDir, "test.txt", []byte("expire-test"))

		res := runYz(t, env, "--expires=1m", filePath)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)

		// Advance clock by 61 seconds (expired by 1 second)
		s3.Advance(61 * time.Second)

		resp, err := http.Get(rawURL)
		if err != nil {
			t.Fatalf("GET presigned URL: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 Forbidden", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "Request has expired") {
			t.Errorf("response body = %q, want it to contain %q", string(body), "Request has expired")
		}
	})
}

func TestS3PresignValidBoundary(t *testing.T) {
	scenario(t, "s3 presign valid by 1s accepted", func(t *testing.T) {
		_, s3, env := s3Fixture(t)
		fileDir := t.TempDir()
		payload := []byte("valid-boundary-payload")
		filePath := writeTestFile(t, fileDir, "test.txt", payload)

		res := runYz(t, env, "--expires=1m", filePath)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)

		// Advance clock by 59 seconds (1 second before 60s expiration)
		s3.Advance(59 * time.Second)

		resp, err := http.Get(rawURL)
		if err != nil {
			t.Fatalf("GET presigned URL: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 OK", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !bytes.Equal(body, payload) {
			t.Fatalf("payload mismatch")
		}
	})
}

func TestS3TamperedSignature(t *testing.T) {
	scenario(t, "s3 tampered signature rejected", func(t *testing.T) {
		_, _, env := s3Fixture(t)
		fileDir := t.TempDir()
		filePath := writeTestFile(t, fileDir, "test.txt", []byte("tamper-test"))

		res := runYz(t, env, filePath)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)

		u, err := url.Parse(rawURL)
		if err != nil {
			t.Fatalf("parsing URL: %v", err)
		}
		q := u.Query()
		sig := q.Get("X-Amz-Signature")
		if len(sig) < 2 {
			t.Fatalf("signature too short: %q", sig)
		}
		// Flip the last hex char
		lastChar := sig[len(sig)-1]
		if lastChar == '0' {
			lastChar = '1'
		} else {
			lastChar = '0'
		}
		q.Set("X-Amz-Signature", sig[:len(sig)-1]+string(lastChar))
		u.RawQuery = q.Encode()

		resp, err := http.Get(u.String())
		if err != nil {
			t.Fatalf("GET tampered URL: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 Forbidden", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "SignatureDoesNotMatch") {
			t.Errorf("response body = %q, want SignatureDoesNotMatch", string(body))
		}
	})
}

func TestS3TamperedQueryParam(t *testing.T) {
	scenario(t, "s3 tampered query parameter rejected", func(t *testing.T) {
		_, _, env := s3Fixture(t)
		fileDir := t.TempDir()
		filePath := writeTestFile(t, fileDir, "test.txt", []byte("tamper-param"))

		res := runYz(t, env, "--expires=1m", filePath)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)

		u, err := url.Parse(rawURL)
		if err != nil {
			t.Fatalf("parsing URL: %v", err)
		}
		q := u.Query()
		// Tamper with expires: change from 60 to 7200
		q.Set("X-Amz-Expires", "7200")
		u.RawQuery = q.Encode()

		resp, err := http.Get(u.String())
		if err != nil {
			t.Fatalf("GET tampered URL: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 Forbidden", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "SignatureDoesNotMatch") {
			t.Errorf("response body = %q, want SignatureDoesNotMatch", string(body))
		}
	})
}

func TestS3ContentTypeDetection(t *testing.T) {
	scenario(t, "s3 content type detection", func(t *testing.T) {
		_, _, env := s3Fixture(t)
		fileDir := t.TempDir()

		cases := []struct {
			name       string
			payload    []byte
			wantPrefix string
		}{
			{"image.png", append([]byte("\x89PNG\r\n\x1a\n"), []byte("some-png-bytes")...), "image/png"},
			{"doc.txt", []byte("plain text content here"), "text/plain"},
			{"binary.dat", []byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0xfd}, "application/octet-stream"},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				p := writeTestFile(t, fileDir, tc.name, tc.payload)
				res := runYz(t, env, p)
				if res.ExitCode != 0 {
					t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
				}
				rawURL := strings.TrimSpace(res.Stdout)
				resp, err := http.Get(rawURL)
				if err != nil {
					t.Fatalf("GET presigned URL: %v", err)
				}
				defer func() { _ = resp.Body.Close() }()

				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, want 200", resp.StatusCode)
				}
				ct := resp.Header.Get("Content-Type")
				if !strings.HasPrefix(ct, tc.wantPrefix) {
					t.Errorf("Content-Type = %q, want prefix %q", ct, tc.wantPrefix)
				}
			})
		}
	})
}

func TestS3EndpointError(t *testing.T) {
	scenario(t, "s3 endpoint error 500", func(t *testing.T) {
		_, s3, env := s3Fixture(t)
		s3.StatusOverride = 500
		fileDir := t.TempDir()
		filePath := writeTestFile(t, fileDir, "test.txt", []byte("hello"))

		res := runYz(t, env, filePath)
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1", res.ExitCode)
		}
		if res.Stdout != "" {
			t.Errorf("stdout = %q, want empty", res.Stdout)
		}
		if !strings.Contains(res.Stderr, "R2 service error (HTTP 500)") {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "R2 service error (HTTP 500)")
		}
	})
}
