package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallMethod(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")
	if got := InstallMethod(filepath.Join(home, "go", "bin", "yz")); got != MethodGo {
		t.Fatalf("default GOPATH = %v", got)
	}

	gopath := t.TempDir()
	t.Setenv("GOPATH", gopath+string(os.PathListSeparator)+filepath.Join(t.TempDir(), "other"))
	if got := InstallMethod(filepath.Join(gopath, "bin", "yz")); got != MethodGo {
		t.Fatalf("GOPATH/bin = %v", got)
	}
	gobin := filepath.Join(t.TempDir(), "gobin")
	t.Setenv("GOBIN", gobin)
	if got := InstallMethod(filepath.Join(gobin, "yz")); got != MethodGo {
		t.Fatalf("GOBIN = %v", got)
	}

	for _, path := range []string{
		"/opt/homebrew/Cellar/yz/1.2.3/bin/yz",
		"/opt/homebrew/bin/yz",
		"/usr/local/Cellar/yz/1.2.3/bin/yz",
		"/home/linuxbrew/.linuxbrew/Cellar/yz/1.2.3/bin/yz",
	} {
		if got := InstallMethod(path); got != MethodBrew {
			t.Errorf("InstallMethod(%s) = %v, want brew", path, got)
		}
	}
	if got := InstallMethod(filepath.Join(t.TempDir(), "yz")); got != MethodDirect {
		t.Fatalf("direct = %v", got)
	}
	if msg := ManagedMessage(MethodBrew); msg != "yz is installed with Homebrew. Run: brew upgrade yz" {
		t.Fatalf("brew message = %q", msg)
	}
	if msg := ManagedMessage(MethodGo); msg != "yz is installed with go install. Run: go install github.com/baires/yz/cmd/yz@latest" {
		t.Fatalf("go message = %q", msg)
	}
}

func TestReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "yz")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Replace(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("contents = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %o, want 755", info.Mode().Perm())
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".yz-update-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("left temp files: %v", matches)
	}
	if err := Replace(path, nil); err == nil {
		t.Fatal("empty payload should be refused")
	}
}
