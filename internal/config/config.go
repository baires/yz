// Package config loads and saves yz's on-disk configuration: a JSON file
// (config.json, schema v1) inside the config directory, which defaults to
// ~/.config/yz and is overridden by YZ_CONFIG_DIR (resolved by the caller).
package config

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

// FileName is the config file name inside the config directory.
const FileName = "config.json"

// SchemaVersion is the only config schema version this build understands.
const SchemaVersion = 1

// Config is the on-disk schema v1. The credential fields stay empty until
// yz setup (Tasks 4–5) populates them.
type Config struct {
	SchemaVersion int       `json:"schema_version"`
	AccountID     string    `json:"account_id,omitempty"`
	Bucket        string    `json:"bucket,omitempty"`
	URLBase       string    `json:"url_base,omitempty"`
	AccessToken   string    `json:"access_token,omitempty"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	TokenExpiry   time.Time `json:"token_expiry"`
	S3AccessKeyID string    `json:"s3_access_key_id,omitempty"`
	S3Secret      string    `json:"s3_secret,omitempty"`
}

// Load reads and validates the config in dir. A missing file is not an
// error the user can fix with chmod: it reports "not configured". Insecure
// permissions are rejected before parsing, so a world-readable file is
// flagged even when its contents are also invalid.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, FileName)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errors.New("yz: not configured — run yz setup")
		}
		return nil, fmt.Errorf("yz: reading config at %q: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("yz: config at %q is a directory — run yz setup", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("yz: config at %q is readable by others — run: chmod 600 %q", path, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("yz: reading config at %q: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("yz: config at %q is corrupt — run yz setup", path)
	}
	if cfg.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("yz: config at %q has unsupported schema version %d (this build supports %d) — run yz setup", path, cfg.SchemaVersion, SchemaVersion)
	}
	return &cfg, nil
}

// Save writes cfg as <dir>/config.json atomically — temp file in the same
// directory, fsync, rename — so a crash leaves the old or the new config,
// never a truncated one. The file is mode 0600 and a missing config dir is
// created with mode 0700.
func Save(dir string, cfg *Config) error {
	cfg.SchemaVersion = SchemaVersion
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("yz: encoding config: %w", err)
	}
	if err := atomicfile.Write(dir, FileName, append(data, '\n')); err != nil {
		var op *atomicfile.OpError
		if errors.As(err, &op) && op.Op == "dir" {
			return fmt.Errorf("yz: creating config dir %q: %w", dir, err)
		}
		return fmt.Errorf("yz: writing config in %q: %w", dir, err)
	}
	return nil
}
