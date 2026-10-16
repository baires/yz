package update

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLatestTagStopsAtRedirect(t *testing.T) {
	tagHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			http.Redirect(w, r, "/baires/yz/releases/tag/v1.4.0", http.StatusFound)
		case strings.Contains(r.URL.Path, "/releases/tag/"):
			tagHits++
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	tag, err := LatestTag(context.Background(), srv.URL, DefaultRepo, "yz/test")
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v1.4.0" {
		t.Fatalf("tag = %q", tag)
	}
	if tagHits != 0 {
		t.Fatalf("release page was downloaded %d times", tagHits)
	}
}

func TestLatestTagRefusesMetadataRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	if _, err := LatestTag(context.Background(), srv.URL, DefaultRepo, "yz/test"); err == nil {
		t.Fatal("expected redirect to be refused")
	}
}

func TestReleaseURLRejectsNonLocalHTTP(t *testing.T) {
	if _, err := releaseURL("http://example.com", DefaultRepo, "latest"); err == nil {
		t.Fatal("expected non-local http to be rejected")
	}
	if _, err := releaseURL("https://github.com", "not a repo", "latest"); err == nil {
		t.Fatal("expected invalid repo to be rejected")
	}
	if _, err := LatestTag(context.Background(), "file:///tmp", DefaultRepo, "yz/test"); err == nil {
		t.Fatal("expected file URL to be rejected")
	}
}

func TestChecksumsAndApply(t *testing.T) {
	const tag = "v1.2.3"
	asset := AssetName(tag, runtime.GOOS, runtime.GOARCH)
	body := []byte("new-binary")
	other := []byte("other")
	files := map[string][]byte{
		asset:         body,
		"yz_other":    other,
		ChecksumsFile: checksumFile(t, asset, body, "yz_other", other),
		"ignored.txt": []byte("nope"),
	}
	files[ChecksumsFile] = append(files[ChecksumsFile], []byte(fmt.Sprintf("%x *%s\n", sha256.Sum256(body), "not-selected"))...)
	srv := releaseServer(t, tag, files)

	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	if err := os.WriteFile(target, []byte("old"), 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "yz")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	got, err := Apply(context.Background(), Request{
		BaseURL: srv.URL, Repo: DefaultRepo, Tag: tag,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Exe: link, UserAgent: "yz/test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != tag {
		t.Fatalf("applied %q", got)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(body) {
		t.Fatalf("target = %q", data)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was replaced with a regular file")
	}
	info, err = os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Errorf("mode = %o, want 750", info.Mode().Perm())
	}

	bad := map[string][]byte{
		asset:         body,
		ChecksumsFile: []byte("0000000000000000000000000000000000000000000000000000000000000000  " + asset + "\n"),
	}
	badSrv := releaseServer(t, tag, bad)
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = Apply(context.Background(), Request{
		BaseURL: badSrv.URL, Repo: DefaultRepo, Tag: tag,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Exe: target, UserAgent: "yz/test",
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
	data, err = os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old" {
		t.Fatalf("mismatch replaced the binary with %q", data)
	}
}

func TestExpectedDigestFormats(t *testing.T) {
	body := []byte("yz")
	sum := fmt.Sprintf("%x", sha256.Sum256(body))
	asset := "yz_v1.2.3_linux_amd64"
	text := sum + "  " + asset + "\n" +
		"abcdef  other\n" +
		sum + " *" + asset + "\r\n"
	// The second matching line wins; both are the same digest.
	got, err := ExpectedDigest([]byte(text), asset)
	if err != nil {
		t.Fatal(err)
	}
	if got != sum {
		t.Fatalf("digest = %s", got)
	}
	if _, err := ExpectedDigest([]byte(sum+"  other\n"), asset); err == nil {
		t.Fatal("expected a missing entry")
	}
	if err := Verify(asset, []byte("nope"), []byte(sum+"  "+asset+"\n")); err == nil {
		t.Fatal("expected a mismatch")
	}
}

func releaseServer(t *testing.T, tag string, files map[string][]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			http.Redirect(w, r, "/baires/yz/releases/tag/"+tag, http.StatusFound)
		case strings.Contains(r.URL.Path, "/releases/download/"):
			name := path.Base(r.URL.Path)
			data, ok := files[name]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func checksumFile(t *testing.T, name string, data []byte, name2 string, data2 []byte) []byte {
	t.Helper()
	return []byte(fmt.Sprintf("%x  %s\n%x  %s\n", sha256.Sum256(data), name, sha256.Sum256(data2), name2))
}
