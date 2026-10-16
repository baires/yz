package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDue(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	if !Due(time.Time{}, now) {
		t.Fatal("zero time should be due")
	}
	if Due(now.Add(time.Minute), now) {
		t.Fatal("future timestamp should not be due")
	}
	if Due(now.Add(-Interval+time.Second), now) {
		t.Fatal("just under 24h should not be due")
	}
	if !Due(now.Add(-Interval), now) {
		t.Fatal("exactly 24h should be due")
	}
}

func TestCacheRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "yz")
	checked := time.Now().UTC().Truncate(time.Second)
	want := Cache{CheckedAt: checked, Latest: "v1.4.0"}
	if err := WriteCache(dir, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o, want 700", info.Mode().Perm())
	}
	path := filepath.Join(dir, CacheFile)
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("cache mode = %o, want 600", info.Mode().Perm())
	}
	got, err := ReadCache(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CheckedAt.Equal(want.CheckedAt) || got.Latest != want.Latest {
		t.Fatalf("cache = %+v", got)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".write-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("left temp files: %v", matches)
	}
}

func TestReadCacheIgnoresUnsafeOrCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, CacheFile)
	if err := os.WriteFile(path, []byte("{\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCache(dir)
	if err != nil || got.Latest != "" {
		t.Fatalf("corrupt cache = %+v, %v", got, err)
	}
	if err := os.WriteFile(path, []byte(`{"latest":"v9.9.9","checked_at":"2026-10-16T00:00:00Z"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = ReadCache(dir)
	if err != nil || got.Latest != "" {
		t.Fatalf("world-readable cache = %+v, %v", got, err)
	}
	if _, err := ReadCache(filepath.Join(dir, "missing")); err != nil {
		t.Fatal(err)
	}
}
