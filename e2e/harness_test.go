// Package e2e drives the compiled yz binary end to end. It is the project's
// only test suite: every scenario runs the real binary via os/exec, and each
// run emits e2e/artifacts/report.json.
package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/baires/yz/e2e/fakes"
)

var binPath string

type scenarioResult struct {
	Name       string `json:"name"`
	Pass       bool   `json:"pass"`
	DurationMS int64  `json:"duration_ms"`
}

// report records scenario results and the digest of the binary under test.
type report struct {
	Timestamp time.Time         `json:"timestamp"`
	Binary    string            `json:"binary_version"`
	Scenarios []scenarioResult  `json:"scenarios"`
	Manifest  map[string]string `json:"manifest,omitempty"`
}

var (
	results         []scenarioResult
	manifestMu      sync.Mutex
	manifestEntries = map[string]string{}
)

func recordManifest(name, digest string) {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	manifestEntries[name] = digest
}

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "yz-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: creating temp dir:", err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	binPath = filepath.Join(tmp, "yz")
	buildArgs := []string{"build"}
	if os.Getenv("YZ_E2E_RACE") == "1" {
		buildArgs = append(buildArgs, "-race")
	}
	buildArgs = append(buildArgs,
		"-ldflags", "-X main.version=e2e-test",
		"-o", binPath, "./cmd/yz")
	build := exec.Command("go", buildArgs...)
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: building binary: %v\n%s", err, out)
		os.Exit(1)
	}

	code := m.Run()
	if err := writeReport(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: writing report:", err)
		code = 1
	}
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}

// writeReport records scenario outcomes plus the binary's self-reported
// version into e2e/artifacts/report.json.
func writeReport() error {
	version := "unknown"
	if out, err := exec.Command(binPath, "version").Output(); err == nil {
		version = string(bytes.TrimSpace(out))
	}
	binary, err := os.ReadFile(binPath)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(binary)
	manifestMap := map[string]string{"yz": fmt.Sprintf("%x", digest)}
	manifestMu.Lock()
	maps.Copy(manifestMap, manifestEntries)
	manifestMu.Unlock()

	rep := report{
		Timestamp: time.Now().UTC(),
		Binary:    version,
		Scenarios: results,
		Manifest:  manifestMap,
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	dir := "artifacts"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(dir, "report.json"), data, 0o644); err != nil {
		return err
	}
	reportDigest := sha256.Sum256(data)
	manifest := fmt.Sprintf("%x  report.json\n", reportDigest)
	return os.WriteFile(filepath.Join(dir, "manifest.sha256"), []byte(manifest), 0o644)
}

// scenario times fn and records its outcome for the run report.
func scenario(t *testing.T, name string, fn func(t *testing.T)) {
	t.Helper()
	start := time.Now()
	defer func() {
		results = append(results, scenarioResult{
			Name: name, Pass: !t.Failed(), DurationMS: time.Since(start).Milliseconds(),
		})
	}()
	fn(t)
}

type runResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// runYz executes the binary with args. YZ_CONFIG_DIR defaults to a fresh
// per-test temp dir; entries in env override it.
func runYz(t *testing.T, env map[string]string, args ...string) runResult {
	input := ""
	if len(args) > 0 && args[0] == "setup" && env["YZ_API_BASE_URL"] != "" {
		input = fakes.SetupInput
	}
	return runYzInput(t, env, input, args...)
}

func runYzInput(t *testing.T, env map[string]string, input string, args ...string) runResult {
	t.Helper()
	cmdEnv := os.Environ()
	if _, ok := env["YZ_CONFIG_DIR"]; !ok {
		cmdEnv = append(cmdEnv, "YZ_CONFIG_DIR="+t.TempDir())
	}
	for k, v := range env {
		cmdEnv = append(cmdEnv, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(binPath, args...)
	cmd.Env = cmdEnv
	cmd.Stdin = bytes.NewBufferString(input)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := runResult{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		res.ExitCode = 0
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		t.Fatalf("running yz %v: %v", args, err)
	}
	return res
}

func TestUnconfiguredRun(t *testing.T) {
	scenario(t, "no config", func(t *testing.T) {
		res := runYz(t, nil, "somefile.png")
		if res.ExitCode != 1 {
			t.Errorf("exit code = %d, want 1", res.ExitCode)
		}
		if res.Stdout != "" {
			t.Errorf("stdout = %q, want empty", res.Stdout)
		}
		if !bytes.Contains([]byte(res.Stderr), []byte("run yz setup")) {
			t.Errorf("stderr = %q, want it to contain %q", res.Stderr, "run yz setup")
		}
	})
}

func TestUsageError(t *testing.T) {
	scenario(t, "usage error", func(t *testing.T) {
		res := runYz(t, nil, "--bogus")
		if res.ExitCode != 2 {
			t.Errorf("exit code = %d, want 2", res.ExitCode)
		}
	})
}

func TestVersion(t *testing.T) {
	scenario(t, "version", func(t *testing.T) {
		res := runYz(t, nil, "version")
		if res.ExitCode != 0 {
			t.Errorf("exit code = %d, want 0 (stderr: %q)", res.ExitCode, res.Stderr)
		}
		if !bytes.Contains([]byte(res.Stdout), []byte("e2e-test")) {
			t.Errorf("stdout = %q, want it to contain %q", res.Stdout, "e2e-test")
		}
	})
}
