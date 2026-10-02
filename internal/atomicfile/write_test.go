package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReplacesPrivately(t *testing.T) {
	dir := t.TempDir()
	name := "secret.json"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := Write(dir, name, []byte("new\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "new\n" {
		t.Fatalf("contents = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".write-*.tmp"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("left temp files: %v", matches)
	}
}
