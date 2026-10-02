package history

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAddAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	expires := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	e := Entry{
		URL:       "https://files.example.com/abc123/photo.png",
		File:      "photo.png",
		Size:      1234,
		CreatedAt: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC),
		ExpiresAt: &expires,
	}
	if err := Add(dir, e); err != nil {
		t.Fatalf("Add: %v", err)
	}

	entries, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	got := entries[0]
	if got.URL != e.URL || got.File != e.File || got.Size != e.Size {
		t.Errorf("entry mismatch: got %+v, want %+v", got, e)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(expires) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, expires)
	}
	if !got.CreatedAt.Equal(e.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, e.CreatedAt)
	}

	info, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("stat history: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("history mode = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadMissingFile(t *testing.T) {
	entries, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if entries != nil {
		t.Errorf("entries = %v, want nil", entries)
	}
}

func TestLoadCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("writing corrupt history: %v", err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load succeeded on corrupt file, want error")
	}
}

func TestAddPrependsAndCaps(t *testing.T) {
	dir := t.TempDir()
	for i := range MaxEntries + 10 {
		if err := Add(dir, Entry{URL: fmt.Sprintf("https://example.com/%d", i)}); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}
	entries, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != MaxEntries {
		t.Fatalf("len(entries) = %d, want %d", len(entries), MaxEntries)
	}
	want := fmt.Sprintf("https://example.com/%d", MaxEntries+9)
	if entries[0].URL != want {
		t.Errorf("newest entry = %q, want %q", entries[0].URL, want)
	}
}
