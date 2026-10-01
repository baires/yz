package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// writeConfigFile writes content as <dir>/config.json with exactly the given
// mode (Chmod after write so umask cannot interfere).
func writeConfigFile(t *testing.T, dir, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod config: %v", err)
	}
}

// runYzWithConfig runs `yz file.png` against a config dir prepared by the
// scenario and asserts the shared contract: exit 1, stdout empty.
func runYzWithConfig(t *testing.T, dir string) runResult {
	t.Helper()
	res := runYz(t, map[string]string{"YZ_CONFIG_DIR": dir}, "file.png")
	if res.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", res.ExitCode)
	}
	if res.Stdout != "" {
		t.Errorf("stdout = %q, want empty", res.Stdout)
	}
	return res
}

func TestCorruptConfig(t *testing.T) {
	scenario(t, "corrupt config", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, "{not json", 0o600)
		res := runYzWithConfig(t, dir)
		if !bytes.Contains([]byte(res.Stderr), []byte("corrupt")) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "corrupt")
		}
		if !bytes.Contains([]byte(res.Stderr), []byte("run yz setup")) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "run yz setup")
		}
	})
}

func TestWorldReadableConfig(t *testing.T) {
	scenario(t, "world-readable config", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, `{"schema_version":1}`, 0o644)
		res := runYzWithConfig(t, dir)
		if !bytes.Contains([]byte(res.Stderr), []byte("chmod 600")) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "chmod 600")
		}
	})
}

func TestNewerSchemaVersion(t *testing.T) {
	scenario(t, "newer schema version", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, `{"schema_version":99}`, 0o600)
		res := runYzWithConfig(t, dir)
		if !bytes.Contains([]byte(res.Stderr), []byte("schema version")) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "schema version")
		}
	})
}

func TestConfigPathIsDirectory(t *testing.T) {
	scenario(t, "config path is a directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "config.json"), 0o700); err != nil {
			t.Fatalf("mkdir config.json: %v", err)
		}
		res := runYzWithConfig(t, dir)
		if !bytes.Contains([]byte(res.Stderr), []byte("directory")) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "directory")
		}
		if !bytes.Contains([]byte(res.Stderr), []byte("run yz setup")) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "run yz setup")
		}
	})
}

// TestValidConfig proves a well-formed config (including unknown fields from
// a newer minor release) loads: the binary gets past config loading and
// reports incomplete configuration rather than a corrupt JSON error.
func TestValidConfig(t *testing.T) {
	scenario(t, "valid config loads", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, `{"schema_version":1,"account_id":"abc","future_field":"x"}`, 0o600)
		res := runYzWithConfig(t, dir)
		if !bytes.Contains([]byte(res.Stderr), []byte("run yz setup")) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "run yz setup")
		}
	})
}
