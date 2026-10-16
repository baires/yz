package update

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

// CacheFile is the update cache inside the config directory.
const CacheFile = "update.json"

// Interval is the longest the cache may be reused before another release
// check. Checks happen at most once per interval.
const Interval = 24 * time.Hour

// Cache is the on-disk record of the last release check. It holds no
// credentials. The file is mode 0600.
type Cache struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest,omitempty"`
	// ApplyAttemptedAt records the last background install attempt for the
	// current check so a failed download is not retried on every command.
	ApplyAttemptedAt time.Time `json:"apply_attempted_at,omitzero"`
}

// Due reports whether checked is old enough that another release check
// should run. A zero time is due. A timestamp in the future is not.
func Due(checked, now time.Time) bool {
	if checked.IsZero() {
		return true
	}
	if now.Before(checked) {
		return false
	}
	return now.Sub(checked) >= Interval
}

// ReadCache loads the cache. A missing, unreadable-by-others, corrupt, or
// world-accessible file is an empty cache: the next check rewrites it.
// Other IO errors are returned so the caller can skip the check.
func ReadCache(dir string) (Cache, error) {
	if dir == "" {
		return Cache{}, errors.New("empty config dir")
	}
	path := filepath.Join(dir, CacheFile)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Cache{}, nil
		}
		return Cache{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return Cache{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Cache{}, err
	}
	c, ok := decodeCache(data)
	if !ok {
		return Cache{}, nil
	}
	return c, nil
}

// decodeCache reports ok false when data is not a cache. Callers treat that
// as a missing file so the next check can rewrite it.
func decodeCache(data []byte) (Cache, bool) {
	var c Cache
	if err := json.Unmarshal(data, &c); err != nil {
		return Cache{}, false
	}
	return c, true
}

// WriteCache replaces the cache atomically and privately.
func WriteCache(dir string, c Cache) error {
	if dir == "" {
		return errors.New("empty config dir")
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding update cache: %w", err)
	}
	if err := atomicfile.Write(dir, CacheFile, append(data, '\n')); err != nil {
		return fmt.Errorf("writing update cache: %w", err)
	}
	return nil
}
