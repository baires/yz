package fakes

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"strings"
	"sync"
	"testing"
)

// ReleaseServer fakes the GitHub release URLs install.sh and yz update use:
// /releases/latest redirects to /releases/tag/<tag>, and /releases/download
// serves the asset bytes.
type ReleaseServer struct {
	URL string

	srv   *httptest.Server
	mu    sync.Mutex
	tag   string
	files map[string][]byte
	hits  map[string]int
}

// NewReleaseServer starts a fake release host. files maps asset names to
// bodies. checksums.txt is added when missing.
func NewReleaseServer(t *testing.T, tag string, files map[string][]byte) *ReleaseServer {
	t.Helper()
	copied := maps.Clone(files)
	if copied == nil {
		copied = map[string][]byte{}
	}
	if _, ok := copied["checksums.txt"]; !ok {
		copied["checksums.txt"] = checksumManifest(copied)
	}
	r := &ReleaseServer{tag: tag, files: copied, hits: map[string]int{}}
	r.srv = httptest.NewServer(r)
	r.URL = r.srv.URL
	t.Cleanup(r.srv.Close)
	return r
}

// Hits reports how many times name was requested. "latest" counts the
// redirect endpoint.
func (r *ReleaseServer) Hits(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hits[name]
}

func (r *ReleaseServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch {
	case strings.HasSuffix(req.URL.Path, "/releases/latest"):
		r.bump("latest")
		http.Redirect(w, req, "/baires/yz/releases/tag/"+r.tag, http.StatusFound)
	case strings.Contains(req.URL.Path, "/releases/download/"):
		name := path.Base(req.URL.Path)
		r.bump(name)
		r.mu.Lock()
		data, ok := r.files[name]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(data)
	default:
		http.NotFound(w, req)
	}
}

func (r *ReleaseServer) bump(name string) {
	r.mu.Lock()
	r.hits[name]++
	r.mu.Unlock()
}

func checksumManifest(files map[string][]byte) []byte {
	names := make([]string, 0, len(files))
	for name := range files {
		if name == "checksums.txt" {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, name := range names {
		sum := sha256.Sum256(files[name])
		fmt.Fprintf(&b, "%x  %s\n", sum, name)
	}
	return []byte(b.String())
}
