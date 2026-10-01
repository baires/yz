package fakes

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// CredentialServer independently checks signed HEAD requests, including the
// canonical request and key derivation. It never calls the production signer.
type CredentialServer struct {
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []string
	Bucket string
	Status int
}

func NewCredentialServer(t *testing.T) *CredentialServer {
	t.Helper()
	f := &CredentialServer{Bucket: "photos", calls: []string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}
func (f *CredentialServer) URL() string { return f.srv.URL }
func (f *CredentialServer) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}

func (f *CredentialServer) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
	if f.Status != 0 {
		w.WriteHeader(f.Status)
		return
	}
	stamp := r.Header.Get("X-Amz-Date")
	date, err := time.Parse("20060102T150405Z", stamp)
	if err != nil || time.Since(date).Abs() > 15*time.Minute {
		w.WriteHeader(403)
		return
	}
	empty := fmt.Sprintf("%x", sha256.Sum256(nil))
	if r.Method != "HEAD" || r.URL.Path != "/"+f.Bucket || r.URL.RawQuery != "" || r.Header.Get("X-Amz-Content-Sha256") != empty {
		w.WriteHeader(403)
		return
	}
	scope := date.Format("20060102") + "/auto/s3/aws4_request"
	signed := "host;x-amz-content-sha256;x-amz-date"
	canonical := strings.Join([]string{
		"HEAD", "/" + f.Bucket, "",
		"host:" + r.Host + "\nx-amz-content-sha256:" + empty + "\nx-amz-date:" + stamp + "\n",
		signed, empty,
	}, "\n")
	stringToSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	key := []byte("AWS4" + SecretKey)
	for _, part := range []string{date.Format("20060102"), "auto", "s3", "aws4_request", stringToSign} {
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(part))
		key = mac.Sum(nil)
	}
	expected := "AWS4-HMAC-SHA256 Credential=" + AccessKey + "/" + scope + ", SignedHeaders=" + signed + ", Signature=" + fmt.Sprintf("%x", key)
	if !hmac.Equal([]byte(r.Header.Get("Authorization")), []byte(expected)) {
		w.WriteHeader(403)
		return
	}
	w.WriteHeader(200)
}
