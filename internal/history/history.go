// Package history records past shares in a plain JSON file (shares.json)
// inside the config directory so yz list can show them later. The file is
// mode 0600: stored URLs may carry presigned query credentials.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/baires/yz/internal/atomicfile"
)

// FileName is the history file name inside the config directory.
const FileName = "shares.json"

// MaxEntries caps the stored history; oldest entries are dropped.
const MaxEntries = 200

// Entry is one recorded share.
type Entry struct {
	URL       string     `json:"url"`
	File      string     `json:"file"`
	Size      int64      `json:"size"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Load reads the history in dir, newest first. A missing file is not an
// error: it yields nil, nil.
func Load(dir string) ([]Entry, error) {
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("yz: reading history at %q: %w", path, err)
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("yz: history at %q is corrupt — delete it to start fresh", path)
	}
	return entries, nil
}

// TimeLayout is the local timestamp format used by yz list.
const TimeLayout = "2006-01-02 15:04"

// FormatTime renders t in the local zone for list output.
func FormatTime(t time.Time) string {
	return t.Local().Format(TimeLayout)
}

// Expired reports whether the share link is past its expiry. A link with no
// expiry never expires.
func (e Entry) Expired(now time.Time) bool {
	return e.ExpiresAt != nil && e.ExpiresAt.Before(now)
}

// Add prepends e to the history in dir, caps the list at MaxEntries, and
// saves atomically — temp file in the same directory, fsync, rename — so a
// crash leaves the old or the new history, never a truncated one.
func Add(dir string, e Entry) error {
	entries, err := Load(dir)
	if err != nil {
		return err
	}
	entries = append([]Entry{e}, entries...)
	if len(entries) > MaxEntries {
		entries = entries[:MaxEntries]
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("yz: encoding history: %w", err)
	}
	if err := atomicfile.Write(dir, FileName, append(data, '\n')); err != nil {
		var op *atomicfile.OpError
		if errors.As(err, &op) && op.Op == "dir" {
			return fmt.Errorf("yz: creating config dir %q: %w", dir, err)
		}
		return fmt.Errorf("yz: writing history in %q: %w", dir, err)
	}
	return nil
}
