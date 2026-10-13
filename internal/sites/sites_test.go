package sites

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHost(t *testing.T) {
	if h, err := Host("https://Static.Example.com"); err != nil || h != "static.example.com" {
		t.Fatalf("Host = %q %v", h, err)
	}
	for _, bad := range []string{"", "https://pub-x.r2.dev"} {
		if _, err := Host(bad); err == nil {
			t.Errorf("Host(%q) accepted", bad)
		}
	}
}

func TestVersionChangesWithTarget(t *testing.T) {
	if Version("a", "h") == Version("b", "h") || Version("a", "h") == Version("a", "i") || len(Version("a", "h")) != 16 {
		t.Fatal("version must be stable and depend on bucket and host")
	}
}

func TestContentType(t *testing.T) {
	for name, want := range map[string]string{
		"index.html": "text/html", "a.JS": "text/javascript", "s.css": "text/css",
		"x.wasm": "application/wasm", "i.svg": "image/svg+xml", "noext": "application/octet-stream",
	} {
		if got := ContentType(name); !strings.HasPrefix(got, want) {
			t.Errorf("%s = %s", name, got)
		}
	}
}

func TestPrepareRejectsZipTraversal(t *testing.T) {
	p := filepath.Join(t.TempDir(), "evil.zip")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("../escape.html")
	_, _ = w.Write([]byte("x"))
	_ = zw.Close()
	_ = f.Close()
	if _, cleanup, err := Prepare(p); err == nil {
		cleanup()
		t.Fatal("traversal zip accepted")
	}
}

func TestPrepareRejectsOtherFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "page.html")
	_ = os.WriteFile(p, []byte("x"), 0o600)
	if _, _, err := Prepare(p); err == nil {
		t.Fatal("single file accepted as site")
	}
}

func TestCollectSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "index.html"), []byte("x"), 0o600)
	secret := filepath.Join(t.TempDir(), "secret")
	_ = os.WriteFile(secret, []byte("s"), 0o600)
	if err := os.Symlink(secret, filepath.Join(root, "leak.txt")); err != nil {
		t.Skip(err)
	}
	files, err := Collect(root)
	if err != nil || len(files) != 1 || files[0].Rel != "index.html" {
		t.Fatalf("files = %+v %v", files, err)
	}
}

func TestURL(t *testing.T) {
	if got := URL("https://cdn.example.com/", "abc"); got != "https://cdn.example.com/s/abc/" {
		t.Fatal(got)
	}
}
