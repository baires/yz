// Package history records past shares in a plain JSON file (shares.json)
// inside the config directory so yz list can show them later. The file is
// mode 0600: stored URLs may carry presigned query credentials.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/baires/yz/internal/atomicfile"
)

// FileName is the history file name inside the config directory.
const FileName = "shares.json"

// MaxEntries caps the stored history; oldest entries are dropped.
const MaxEntries = 200

// KindSite marks a static site; file shares leave Kind empty.
const KindSite = "site"

// Entry is one recorded share.
type Entry struct {
	Kind      string     `json:"kind,omitempty"`
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
	return save(dir, entries)
}

func save(dir string, entries []Entry) error {
	if entries == nil {
		entries = []Entry{}
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

// IsSite reports whether the entry is a static site.
func (e Entry) IsSite() bool { return e.Kind == KindSite }

// Label is the name shown by yz list; sites end in a slash.
func (e Entry) Label() string {
	if e.IsSite() {
		return e.File + "/"
	}
	return e.File
}

var (
	fileKeyPattern = regexp.MustCompile(`^[0-9A-Za-z]{16}/[^/]+$`)
	siteIDPattern  = regexp.MustCompile(`^[0-9A-Za-z]{16}$`)
)

// Target returns the R2 object key (file share) or key prefix (site) this
// entry points at. Presigned URLs carry the bucket as the first path
// segment; public URLs put the key at the root.
func (e Entry) Target(bucket string) (key string, isPrefix bool, err error) {
	u, err := url.Parse(e.URL)
	if err != nil {
		return "", false, fmt.Errorf("yz: cannot parse share URL")
	}
	p := strings.TrimPrefix(u.Path, "/")
	if u.Query().Get("X-Amz-Signature") != "" {
		p = strings.TrimPrefix(p, bucket+"/")
	}
	if e.IsSite() {
		id, ok := strings.CutPrefix(p, "s/")
		id = strings.TrimSuffix(id, "/")
		if !ok || !siteIDPattern.MatchString(id) {
			return "", false, fmt.Errorf("yz: cannot find the site id in %q", e.URL)
		}
		return "sites/" + id + "/", true, nil
	}
	if !fileKeyPattern.MatchString(p) {
		return "", false, fmt.Errorf("yz: cannot find the object key in %q", e.URL)
	}
	return p, false, nil
}

// Remove drops entries matching url and created time from the history.
func Remove(dir, url string, created time.Time) error {
	entries, err := Load(dir)
	if err != nil {
		return err
	}
	kept := entries[:0]
	for _, e := range entries {
		if e.URL == url && e.CreatedAt.Equal(created) {
			continue
		}
		kept = append(kept, e)
	}
	return save(dir, kept)
}
