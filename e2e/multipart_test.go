package e2e

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/baires/yz/internal/r2"
)

// multipartPayload builds a deterministic payload of size bytes with a real
// PNG header so MIME detection and the .png extension agree on image/png.
func multipartPayload(size int) []byte {
	payload := make([]byte, size)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))
	for i := 8; i < size; i++ {
		payload[i] = byte(i*31 ^ i>>8)
	}
	return payload
}

type s3Call struct {
	method string
	query  url.Values
}

// parseS3Calls splits recorded "METHOD /path?query" call lines into
// method and query components.
func parseS3Calls(t *testing.T, calls []string) []s3Call {
	t.Helper()
	var out []s3Call
	for _, c := range calls {
		method, uri, _ := strings.Cut(c, " ")
		u, err := url.Parse(uri)
		if err != nil {
			t.Fatalf("parsing recorded call %q: %v", c, err)
		}
		out = append(out, s3Call{method: method, query: u.Query()})
	}
	return out
}

// multipartCallCounts classifies recorded calls into multipart phases:
// initiate count, per-part-number PUT counts, complete count, abort count.
func multipartCallCounts(t *testing.T, calls []string) (int, map[int]int, int, int) {
	t.Helper()
	initiates, completes, aborts := 0, 0, 0
	partCalls := map[int]int{}
	for _, c := range parseS3Calls(t, calls) {
		_, initiate := c.query["uploads"]
		uploadID := c.query.Get("uploadId")
		switch {
		case c.method == http.MethodPost && initiate:
			initiates++
		case c.method == http.MethodPost && uploadID != "":
			completes++
		case c.method == http.MethodDelete && uploadID != "":
			aborts++
		case c.method == http.MethodPut && c.query.Get("partNumber") != "":
			n, err := strconv.Atoi(c.query.Get("partNumber"))
			if err != nil {
				t.Fatalf("parsing partNumber from %v: %v", c.query, err)
			}
			partCalls[n]++
		}
	}
	return initiates, partCalls, completes, aborts
}

func TestMultipartUpload(t *testing.T) {
	scenario(t, "multipart upload of file above threshold round-trips", func(t *testing.T) {
		_, s3, _, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		fileDir := t.TempDir()
		payload := multipartPayload(65 << 20)
		filePath := writeTestFile(t, fileDir, "big.png", payload)

		res := runYz(t, env, filePath)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)
		u, err := url.Parse(rawURL)
		if err != nil {
			t.Fatalf("parsing URL %q: %v", rawURL, err)
		}
		key := strings.TrimPrefix(u.Path, "/")

		initiates, partCalls, completes, aborts := multipartCallCounts(t, s3.Calls())
		if initiates != 1 {
			t.Errorf("initiate calls = %d, want 1", initiates)
		}
		if completes != 1 {
			t.Errorf("complete calls = %d, want 1", completes)
		}
		if aborts != 0 {
			t.Errorf("abort calls = %d, want 0", aborts)
		}
		// 65 MiB at 8 MiB parts = 9 parts.
		if len(partCalls) != 9 {
			t.Fatalf("distinct part numbers = %d, want 9 (parts: %v)", len(partCalls), partCalls)
		}
		for n := 1; n <= 9; n++ {
			if partCalls[n] != 1 {
				t.Errorf("part %d PUT calls = %d, want 1", n, partCalls[n])
			}
		}

		obj, ok := s3.GetObject(key)
		if !ok {
			t.Fatalf("object with key %q not found in S3", key)
		}
		if !bytes.Equal(obj.Data, payload) {
			t.Errorf("assembled object mismatch: got %d bytes, want %d", len(obj.Data), len(payload))
		}
		if obj.ContentType != "image/png" {
			t.Errorf("Content-Type = %q, want image/png", obj.ContentType)
		}

		// The assembled object must also be served by the public host.
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
			t.Errorf("downloaded payload mismatch")
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
			t.Errorf("served Content-Type = %q, want image/png", ct)
		}
	})
}

func TestMultipartThresholdBoundary(t *testing.T) {
	scenario(t, "file just under threshold uses single PUT", func(t *testing.T) {
		_, s3, _, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		fileDir := t.TempDir()
		payload := multipartPayload(r2.DefaultMultipartThreshold - 1)
		filePath := writeTestFile(t, fileDir, "under.png", payload)

		res := runYz(t, env, filePath)
		if res.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %q", res.ExitCode, res.Stderr)
		}
		rawURL := strings.TrimSpace(res.Stdout)
		u, err := url.Parse(rawURL)
		if err != nil {
			t.Fatalf("parsing URL %q: %v", rawURL, err)
		}
		key := strings.TrimPrefix(u.Path, "/")

		var puts int
		for _, c := range parseS3Calls(t, s3.Calls()) {
			if _, initiate := c.query["uploads"]; initiate {
				t.Errorf("unexpected multipart initiate call: %v", c.query)
			}
			if c.query.Get("uploadId") != "" || c.query.Get("partNumber") != "" {
				t.Errorf("unexpected multipart query in call: %v", c.query)
			}
			if c.method == http.MethodPut {
				puts++
			}
		}
		if puts != 1 {
			t.Errorf("PUT calls = %d, want 1", puts)
		}

		obj, ok := s3.GetObject(key)
		if !ok {
			t.Fatalf("object with key %q not found in S3", key)
		}
		if !bytes.Equal(obj.Data, payload) {
			t.Errorf("stored object mismatch: got %d bytes, want %d", len(obj.Data), len(payload))
		}
	})
}

func TestMultipartAbortOnPartFailure(t *testing.T) {
	scenario(t, "permanently failing part triggers retries then abort", func(t *testing.T) {
		_, s3, _, env := shareFixture(t, "MOCK_PUBLIC_HOST")
		s3.FailPartNumber = 5
		fileDir := t.TempDir()
		payload := multipartPayload(65 << 20)
		filePath := writeTestFile(t, fileDir, "failing.png", payload)

		res := runYz(t, env, filePath)
		if res.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1; stdout: %q", res.ExitCode, res.Stdout)
		}
		if !strings.Contains(res.Stderr, "HTTP 500") {
			t.Errorf("stderr = %q, want it to mention the HTTP 500 part failure", res.Stderr)
		}

		initiates, partCalls, completes, aborts := multipartCallCounts(t, s3.Calls())
		if initiates != 1 {
			t.Errorf("initiate calls = %d, want 1", initiates)
		}
		if completes != 0 {
			t.Errorf("complete calls = %d, want 0", completes)
		}
		if aborts != 1 {
			t.Errorf("abort calls = %d, want 1", aborts)
		}
		// The failed part is attempted 3 times before the upload aborts.
		if partCalls[5] != 3 {
			t.Errorf("part 5 PUT calls = %d, want 3 (retries)", partCalls[5])
		}
		if got := s3.ObjectCount(); got != 0 {
			t.Errorf("stored objects = %d, want 0", got)
		}
	})
}

func TestMultipartCompletionFailure(t *testing.T) {
	for _, status := range []int{200, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			_, s3, _, env := shareFixture(t, "MOCK_PUBLIC_HOST")
			if status == 200 {
				s3.CompleteError = "InternalError"
			} else {
				s3.CompleteStatus = status
			}
			path := writeTestFile(t, t.TempDir(), "failure.png", multipartPayload(64<<20))
			res := runYz(t, env, path)
			if res.ExitCode != 1 || strings.TrimSpace(res.Stdout) != "" {
				t.Errorf("expected failure without URL, got exit=%d stdout=%q", res.ExitCode, res.Stdout)
			}
			_, _, completes, aborts := multipartCallCounts(t, s3.Calls())
			if completes != 1 || aborts != 1 {
				t.Errorf("complete=%d abort=%d, want 1 each", completes, aborts)
			}
			if s3.ObjectCount() != 0 {
				t.Error("failed completion stored an object")
			}
		})
	}
}
