package e2e

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baires/yz/e2e/fakes"
	"github.com/baires/yz/internal/config"
	"github.com/baires/yz/internal/update"
)

func TestUpdateCheckCommand(t *testing.T) {
	scenario(t, "yz update --check reports the fake release", func(t *testing.T) {
		const tag = "v1.2.3"
		rel := fakes.NewReleaseServer(t, tag, nil)
		res := runBin(t, binPath, releaseEnv(t, map[string]string{"YZ_GITHUB_BASE": rel.URL}), "update", "--check")
		if res.ExitCode != 0 {
			t.Fatalf("exit %d stderr %q", res.ExitCode, res.Stderr)
		}
		if res.Stdout != "yz 1.2.3 is available, run yz update\n" {
			t.Fatalf("stdout = %q", res.Stdout)
		}
		if rel.Hits("latest") != 1 || rel.Hits(assetName(tag)) != 0 {
			t.Fatalf("latest hits %d asset hits %d", rel.Hits("latest"), rel.Hits(assetName(tag)))
		}
	})
}

func TestUpdateReplacesBinary(t *testing.T) {
	scenario(t, "yz update verifies the checksum and replaces the binary", func(t *testing.T) {
		const tag = "v1.2.3"
		body := markerBytes(t, tag)
		asset := assetName(tag)
		rel := fakes.NewReleaseServer(t, tag, map[string][]byte{asset: body})
		exe := copyExecutable(t, binPath, filepath.Join(t.TempDir(), "yz"))
		before := sha256.Sum256(mustRead(t, exe))
		res := runBin(t, exe, releaseEnv(t, map[string]string{"YZ_GITHUB_BASE": rel.URL}), "update")
		if res.ExitCode != 0 || res.Stderr != "yz updated to 1.2.3\n" || res.Stdout != "" {
			t.Fatalf("result %+v", res)
		}
		if rel.Hits(asset) != 1 || rel.Hits("checksums.txt") != 1 {
			t.Fatalf("asset %d checksums %d", rel.Hits(asset), rel.Hits("checksums.txt"))
		}
		after := runBin(t, exe, map[string]string{"YZ_CONFIG_DIR": t.TempDir(), "CI": "true"}, "version")
		if after.ExitCode != 0 || after.Stdout != "yz "+tag+"\n" {
			t.Fatalf("replaced version %+v", after)
		}
		if sha256.Sum256(mustRead(t, exe)) == before {
			t.Fatal("binary was not replaced")
		}
	})
}

func TestUpdateRejectsBadChecksum(t *testing.T) {
	scenario(t, "yz update refuses a checksum mismatch", func(t *testing.T) {
		const tag = "v1.2.3"
		asset := assetName(tag)
		body := []byte("not a real match")
		rel := fakes.NewReleaseServer(t, tag, map[string][]byte{
			asset:           body,
			"checksums.txt": []byte("0000000000000000000000000000000000000000000000000000000000000000  " + asset + "\n"),
		})
		exe := copyExecutable(t, binPath, filepath.Join(t.TempDir(), "yz"))
		before := mustRead(t, exe)
		res := runBin(t, exe, releaseEnv(t, map[string]string{"YZ_GITHUB_BASE": rel.URL}), "update")
		if res.ExitCode != 1 || !strings.Contains(res.Stderr, "checksum mismatch") {
			t.Fatalf("result %+v", res)
		}
		if !bytes.Equal(mustRead(t, exe), before) {
			t.Fatal("checksum mismatch replaced the binary")
		}
	})
}

func TestUpdateManagedInstalls(t *testing.T) {
	scenario(t, "homebrew and go install print update commands", func(t *testing.T) {
		const tag = "v1.2.3"
		asset := assetName(tag)
		rel := fakes.NewReleaseServer(t, tag, map[string][]byte{asset: markerBytes(t, tag)})

		brewDir := filepath.Join(t.TempDir(), "opt", "homebrew", "Cellar", "yz", "1.0.0", "bin")
		brew := copyExecutable(t, binPath, filepath.Join(brewDir, "yz"))
		res := runBin(t, brew, releaseEnv(t, map[string]string{"YZ_GITHUB_BASE": rel.URL}), "update")
		if res.ExitCode != 0 || !strings.Contains(res.Stderr, "brew upgrade yz") {
			t.Fatalf("brew result %+v", res)
		}
		if rel.Hits(asset) != 0 {
			t.Fatalf("homebrew download hits %d", rel.Hits(asset))
		}

		gopath := t.TempDir()
		goBin := copyExecutable(t, binPath, filepath.Join(gopath, "bin", "yz"))
		res = runBin(t, goBin, releaseEnv(t, map[string]string{
			"YZ_GITHUB_BASE": rel.URL,
			"GOPATH":         gopath,
			"GOBIN":          "",
		}), "update")
		if res.ExitCode != 0 || !strings.Contains(res.Stderr, "go install github.com/baires/yz/cmd/yz@latest") {
			t.Fatalf("go result %+v", res)
		}
	})
}

func TestUpdateNotice(t *testing.T) {
	scenario(t, "a terminal release build prints an update notice", func(t *testing.T) {
		const tag = "v1.2.3"
		rel := fakes.NewReleaseServer(t, tag, nil)
		bin := copyExecutable(t, releaseBinary(t), filepath.Join(t.TempDir(), "yz"))
		env := releaseEnv(t, map[string]string{
			"YZ_GITHUB_BASE":     rel.URL,
			"YZ_TEST_STDOUT_TTY": "1",
		})
		run := startTerminalBin(t, bin, env, "version")
		res := run.Wait()
		if res.ExitCode != 0 {
			t.Fatalf("exit %d stderr %q", res.ExitCode, res.Stderr)
		}
		notice := "yz 1.2.3 is available, run yz update"
		versionAt := strings.Index(res.Stderr, "yz v1.0.0")
		noticeAt := strings.Index(res.Stderr, notice)
		if versionAt < 0 || noticeAt < 0 || versionAt > noticeAt {
			t.Fatalf("stderr = %q", res.Stderr)
		}
		if rel.Hits("latest") != 1 {
			t.Fatalf("latest hits %d", rel.Hits("latest"))
		}
		info, err := os.Stat(filepath.Join(env["YZ_CONFIG_DIR"], update.CacheFile))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("cache mode = %o, want 600", info.Mode().Perm())
		}
		run.AssertTerminalRestored()
	})
}

func TestUpdateNoticeCache(t *testing.T) {
	scenario(t, "a fresh cache skips the network and a stale cache refreshes", func(t *testing.T) {
		rel := fakes.NewReleaseServer(t, "v1.2.3", nil)
		bin := releaseBinary(t)

		freshDir := t.TempDir()
		if err := update.WriteCache(freshDir, update.Cache{CheckedAt: time.Now(), Latest: "v9.9.9"}); err != nil {
			t.Fatal(err)
		}
		fresh := runRelease(t, bin, freshDir, rel.URL, "version")
		if fresh.ExitCode != 0 || !strings.Contains(fresh.Stderr, "yz 9.9.9 is available, run yz update") {
			t.Fatalf("fresh %+v", fresh)
		}
		if rel.Hits("latest") != 0 {
			t.Fatalf("fresh cache hit the server %d times", rel.Hits("latest"))
		}

		staleDir := t.TempDir()
		if err := update.WriteCache(staleDir, update.Cache{CheckedAt: time.Now().Add(-48 * time.Hour), Latest: "v0.0.1"}); err != nil {
			t.Fatal(err)
		}
		stale := runRelease(t, bin, staleDir, rel.URL, "version")
		if stale.ExitCode != 0 || !strings.Contains(stale.Stderr, "yz 1.2.3 is available, run yz update") || strings.Contains(stale.Stderr, "0.0.1") {
			t.Fatalf("stale %+v", stale)
		}
		if rel.Hits("latest") != 1 {
			t.Fatalf("stale hits %d", rel.Hits("latest"))
		}
	})
}

func TestUpdateCheckSkipped(t *testing.T) {
	scenario(t, "update checks stay quiet in CI, when disabled, when piped, and for dev builds", func(t *testing.T) {
		rel := fakes.NewReleaseServer(t, "v9.9.9", nil)
		bin := releaseBinary(t)
		for _, extra := range []map[string]string{
			{"CI": "true"},
			{"YZ_NO_UPDATE_CHECK": "1"},
		} {
			extra["YZ_GITHUB_BASE"] = rel.URL
			extra["YZ_TEST_STDOUT_TTY"] = "1"
			run := startTerminalBin(t, bin, releaseEnv(t, extra), "version")
			got := run.Wait()
			if got.ExitCode != 0 || strings.Contains(got.Stderr, "is available") {
				t.Fatalf("skip %+v result %+v", extra, got)
			}
		}
		if rel.Hits("latest") != 0 {
			t.Fatalf("skipped checks hit latest %d times", rel.Hits("latest"))
		}

		piped := runBin(t, bin, releaseEnv(t, map[string]string{"YZ_GITHUB_BASE": rel.URL}), "version")
		if piped.ExitCode != 0 || piped.Stderr != "" || !strings.Contains(piped.Stdout, "yz v1.0.0") {
			t.Fatalf("piped %+v", piped)
		}
		dev := startTerminalBin(t, binPath, releaseEnv(t, map[string]string{
			"YZ_GITHUB_BASE":     rel.URL,
			"YZ_TEST_STDOUT_TTY": "1",
		}), "version")
		got := dev.Wait()
		if got.ExitCode != 0 || strings.Contains(got.Stderr, "is available") {
			t.Fatalf("dev %+v", got)
		}
		if rel.Hits("latest") != 0 {
			t.Fatalf("hits %d", rel.Hits("latest"))
		}
	})
}

func TestAutoUpdate(t *testing.T) {
	scenario(t, "opt-in auto-update swaps the binary and skips package managers", func(t *testing.T) {
		const tag = "v1.2.3"
		asset := assetName(tag)
		body := markerBytes(t, tag)
		rel := fakes.NewReleaseServer(t, tag, map[string][]byte{asset: body})
		exe := copyExecutable(t, releaseBinary(t), filepath.Join(t.TempDir(), "yz"))
		run := startTerminalBin(t, exe, releaseEnv(t, map[string]string{
			"YZ_GITHUB_BASE":     rel.URL,
			"YZ_AUTO_UPDATE":     "1",
			"YZ_TEST_STDOUT_TTY": "1",
		}), "version")
		got := run.Wait()
		if got.ExitCode != 0 || !strings.Contains(got.Stderr, "yz updated to 1.2.3") || strings.Contains(got.Stderr, "is available") {
			t.Fatalf("auto %+v", got)
		}
		next := runBin(t, exe, map[string]string{"YZ_CONFIG_DIR": t.TempDir(), "CI": "true"}, "version")
		if next.Stdout != "yz "+tag+"\n" {
			t.Fatalf("next version %+v", next)
		}

		brew := copyExecutable(t, releaseBinary(t), filepath.Join(t.TempDir(), "opt", "homebrew", "Cellar", "yz", "1.0.0", "bin", "yz"))
		before := mustRead(t, brew)
		brewRun := startTerminalBin(t, brew, releaseEnv(t, map[string]string{
			"YZ_GITHUB_BASE":     rel.URL,
			"YZ_AUTO_UPDATE":     "1",
			"YZ_TEST_STDOUT_TTY": "1",
		}), "version")
		brewRes := brewRun.Wait()
		if brewRes.ExitCode != 0 || !strings.Contains(brewRes.Stderr, "yz 1.2.3 is available, run yz update") || strings.Contains(brewRes.Stderr, "yz updated to") {
			t.Fatalf("brew auto %+v", brewRes)
		}
		if !bytes.Equal(mustRead(t, brew), before) {
			t.Fatal("homebrew binary was replaced")
		}
	})
}

func TestConfigEnablesAutoUpdate(t *testing.T) {
	scenario(t, "yz config auto-update on enables a background install", func(t *testing.T) {
		dir := t.TempDir()
		set := runYz(t, map[string]string{"YZ_CONFIG_DIR": dir}, "config", "auto-update", "on")
		if set.ExitCode != 0 || set.Stdout != "auto-update: on\n" {
			t.Fatalf("set %+v", set)
		}
		info, err := os.Stat(filepath.Join(dir, "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("config mode = %o", info.Mode().Perm())
		}
		cfg, err := config.Load(dir)
		if err != nil || !cfg.AutoUpdate {
			t.Fatalf("config %+v %v", cfg, err)
		}

		const tag = "v1.2.3"
		asset := assetName(tag)
		rel := fakes.NewReleaseServer(t, tag, map[string][]byte{asset: markerBytes(t, tag)})
		exe := copyExecutable(t, releaseBinary(t), filepath.Join(t.TempDir(), "yz"))
		run := startTerminalBin(t, exe, releaseEnv(t, map[string]string{
			"YZ_CONFIG_DIR":      dir,
			"YZ_GITHUB_BASE":     rel.URL,
			"YZ_TEST_STDOUT_TTY": "1",
		}), "version")
		got := run.Wait()
		if got.ExitCode != 0 || !strings.Contains(got.Stderr, "yz updated to 1.2.3") {
			t.Fatalf("auto from config %+v", got)
		}
	})
}

func TestUpdateHelp(t *testing.T) {
	scenario(t, "yz update --help explains --check", func(t *testing.T) {
		res := runYz(t, nil, "update", "--help")
		if res.ExitCode != 0 || !strings.Contains(res.Stderr, "--check") || !strings.Contains(res.Stderr, "brew upgrade yz") {
			t.Fatalf("help %+v", res)
		}
	})
}

func runRelease(t *testing.T, bin, dir, base, arg string) runResult {
	t.Helper()
	run := startTerminalBin(t, bin, releaseEnv(t, map[string]string{
		"YZ_CONFIG_DIR":      dir,
		"YZ_GITHUB_BASE":     base,
		"YZ_TEST_STDOUT_TTY": "1",
	}), arg)
	res := run.Wait()
	run.AssertTerminalRestored()
	return res
}

func releaseEnv(t *testing.T, extra map[string]string) map[string]string {
	t.Helper()
	env := map[string]string{
		"CI":                 "",
		"YZ_NO_UPDATE_CHECK": "",
		"YZ_AUTO_UPDATE":     "",
		"YZ_CONFIG_DIR":      t.TempDir(),
	}
	maps.Copy(env, extra)
	return env
}

func runBin(t *testing.T, bin string, env map[string]string, args ...string) runResult {
	t.Helper()
	cmdEnv := os.Environ()
	if _, ok := env["YZ_CONFIG_DIR"]; !ok {
		cmdEnv = append(cmdEnv, "YZ_CONFIG_DIR="+t.TempDir())
	}
	for k, v := range env {
		cmdEnv = append(cmdEnv, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Env = cmdEnv
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
		t.Fatalf("running %s %v: %v", bin, args, err)
	}
	return res
}

func copyExecutable(t *testing.T, src, dst string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, mustRead(t, src), 0o755); err != nil {
		t.Fatal(err)
	}
	return dst
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assetName(tag string) string {
	return update.AssetName(tag, runtime.GOOS, runtime.GOARCH)
}

func markerBytes(t *testing.T, version string) []byte {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	prog := "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"yz " + version + "\") }\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "marker")
	cmd := exec.Command("go", "build", "-o", out, src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("marker build: %v\n%s", err, b)
	}
	return mustRead(t, out)
}

var (
	releaseOnce sync.Once
	releaseBin  string
	releaseErr  error
)

func releaseBinary(t *testing.T) string {
	t.Helper()
	releaseOnce.Do(func() {
		dir, err := os.MkdirTemp("", "yz-release-bin")
		if err != nil {
			releaseErr = err
			return
		}
		releaseBin = filepath.Join(dir, "yz")
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-X main.version=v1.0.0", "-o", releaseBin, "./cmd/yz")
		cmd.Dir = ".."
		if out, err := cmd.CombinedOutput(); err != nil {
			releaseErr = fmt.Errorf("build v1.0.0: %w\n%s", err, out)
		}
	})
	if releaseErr != nil {
		t.Fatal(releaseErr)
	}
	return releaseBin
}
