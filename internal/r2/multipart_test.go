package r2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMultipartAbortAfterCompletionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	aborted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			aborted <- struct{}{}
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Query().Has("uploads"):
			_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>test</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == http.MethodPut:
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", `"test"`)
		default:
			_, _ = io.Copy(io.Discard, r.Body)
			cancel()
			<-r.Context().Done()
		}
	}))
	defer server.Close()
	client := S3{
		AccountID: strings.Repeat("1", 32), Endpoint: server.URL,
		Credentials: Credentials{AccessKeyID: strings.Repeat("a", 32), Secret: strings.Repeat("b", 64)},
	}
	err := client.MultipartUpload(ctx, "bench", "key", bytes.NewReader([]byte("payload")), 7, "", MultipartOptions{Concurrency: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
	select {
	case <-aborted:
	case <-time.After(time.Second):
		t.Fatal("canceled completion did not abort")
	}
}
