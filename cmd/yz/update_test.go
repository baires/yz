package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baires/yz/internal/config"
	"github.com/baires/yz/internal/update"
)

func TestExecuteDoesNotBlockWithoutATTY(t *testing.T) {
	setVersion(t, "v1.0.0")
	t.Setenv("CI", "")
	t.Setenv("YZ_NO_UPDATE_CHECK", "")
	t.Setenv("YZ_AUTO_UPDATE", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	t.Setenv("YZ_GITHUB_BASE", srv.URL)
	t.Setenv("YZ_CONFIG_DIR", t.TempDir())

	start := time.Now()
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("version blocked for %s", time.Since(start))
	}
	if !strings.Contains(stdout.String(), "v1.0.0") || stderr.Len() != 0 {
		t.Fatalf("stdout %q stderr %q", stdout.String(), stderr.String())
	}
}

func TestBackgroundCheckUsesCacheAndTimesOut(t *testing.T) {
	setVersion(t, "v1.0.0")
	t.Setenv("YZ_AUTO_UPDATE", "")
	t.Setenv("YZ_REPO", "baires/yz")

	var hits atomic.Int32
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-r.Context().Done()
	}))
	t.Cleanup(hang.Close)
	t.Setenv("YZ_GITHUB_BASE", hang.URL)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte("{\"schema_version\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := update.WriteCache(dir, update.Cache{CheckedAt: time.Now(), Latest: "v9.9.9"}); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	launchUpdateCheck(update.NoticePolicy{Current: version, StdoutTTY: true, StderrTTY: true}, envConfig{ConfigDir: dir}, "", time.Second, time.Second).finish(&stderr)
	if hits.Load() != 0 {
		t.Fatalf("fresh cache contacted the server %d times", hits.Load())
	}
	if stderr.String() != "yz 9.9.9 is available, run yz update\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}

	staleDir := t.TempDir()
	if err := update.WriteCache(staleDir, update.Cache{CheckedAt: time.Now().Add(-48 * time.Hour), Latest: "v8.8.8"}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	stderr.Reset()
	launchUpdateCheck(update.NoticePolicy{Current: version, StdoutTTY: true, StderrTTY: true}, envConfig{ConfigDir: staleDir}, "", 200*time.Millisecond, time.Second).finish(&stderr)
	if time.Since(start) > time.Second {
		t.Fatalf("timed-out check took %s", time.Since(start))
	}
	if stderr.String() != "yz 8.8.8 is available, run yz update\n" {
		t.Fatalf("stale stderr = %q", stderr.String())
	}
	cached, err := update.ReadCache(staleDir)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Latest != "v8.8.8" || cached.CheckedAt.IsZero() {
		t.Fatalf("cache after timeout = %+v", cached)
	}
}

func TestBackgroundCheckFindsReleaseAndAutoUpdates(t *testing.T) {
	setVersion(t, "v1.0.0")
	t.Setenv("YZ_AUTO_UPDATE", "")
	t.Setenv("YZ_REPO", "baires/yz")
	const tag = "v1.2.3"
	asset := update.AssetName(tag, runtime.GOOS, runtime.GOARCH)
	body := []byte("updated-bytes")
	var downloads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			http.Redirect(w, r, "/baires/yz/releases/tag/"+tag, http.StatusFound)
		case strings.Contains(r.URL.Path, "/releases/download/"):
			downloads.Add(1)
			name := path.Base(r.URL.Path)
			switch name {
			case update.ChecksumsFile:
				sum := sha256.Sum256(body)
				_, _ = fmt.Fprintf(w, "%x  %s\n", sum, asset)
			case asset:
				_, _ = w.Write(body)
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("YZ_GITHUB_BASE", srv.URL)

	dir := t.TempDir()
	var stderr bytes.Buffer
	launchUpdateCheck(update.NoticePolicy{Current: version, StdoutTTY: true, StderrTTY: true}, envConfig{ConfigDir: dir}, "", time.Second, time.Second).finish(&stderr)
	if stderr.String() != "yz 1.2.3 is available, run yz update\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if downloads.Load() != 0 {
		t.Fatalf("auto-update off downloaded %d files", downloads.Load())
	}
	info, err := os.Stat(filepath.Join(dir, update.CacheFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("cache mode = %o", info.Mode().Perm())
	}

	t.Setenv("YZ_AUTO_UPDATE", "1")
	exe := filepath.Join(t.TempDir(), "yz")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	downloads.Store(0)
	launchUpdateCheck(update.NoticePolicy{Current: version, StdoutTTY: true, StderrTTY: true}, envConfig{ConfigDir: t.TempDir()}, exe, time.Second, 5*time.Second).finish(&stderr)
	if stderr.String() != "yz updated to 1.2.3\n" {
		t.Fatalf("auto stderr = %q", stderr.String())
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("binary = %q", got)
	}

	brew := filepath.Join(t.TempDir(), "opt", "homebrew", "Cellar", "yz", "1.0.0", "bin", "yz")
	if err := os.MkdirAll(filepath.Dir(brew), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brew, []byte("brew-old"), 0o755); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	downloads.Store(0)
	launchUpdateCheck(update.NoticePolicy{Current: version, StdoutTTY: true, StderrTTY: true}, envConfig{ConfigDir: t.TempDir()}, brew, time.Second, time.Second).finish(&stderr)
	if stderr.String() != "yz 1.2.3 is available, run yz update\n" {
		t.Fatalf("brew stderr = %q", stderr.String())
	}
	if downloads.Load() != 0 {
		t.Fatalf("homebrew install was downloaded")
	}
	got, err = os.ReadFile(brew)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "brew-old" {
		t.Fatalf("homebrew binary changed to %q", got)
	}
}

func TestUpdateCommand(t *testing.T) {
	setVersion(t, "v1.0.0")
	t.Setenv("YZ_REPO", "baires/yz")
	const tag = "v1.2.3"
	asset := update.AssetName(tag, runtime.GOOS, runtime.GOARCH)
	body := []byte("release-bytes")
	sum := sha256.Sum256(body)
	manifest := fmt.Sprintf("%x  %s\n", sum, asset)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			http.Redirect(w, r, "/baires/yz/releases/tag/"+tag, http.StatusFound)
		case strings.HasSuffix(r.URL.Path, "/"+update.ChecksumsFile):
			_, _ = ioWrite(w, []byte(manifest))
		case strings.HasSuffix(r.URL.Path, "/"+asset):
			_, _ = ioWrite(w, body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("YZ_GITHUB_BASE", srv.URL)
	env := envConfig{ConfigDir: t.TempDir()}

	var stdout, stderr bytes.Buffer
	if code := updateCommand([]string{"--check"}, env, "", &stdout, &stderr); code != 0 {
		t.Fatalf("check exit %d stderr %q", code, stderr.String())
	}
	if stdout.String() != "yz 1.2.3 is available, run yz update\n" {
		t.Fatalf("check stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := updateCommand([]string{"--help"}, env, "", &stdout, &stderr); code != 0 || !strings.Contains(stderr.String(), "--check") {
		t.Fatalf("help %d %q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := updateCommand([]string{"extra"}, env, "", &stdout, &stderr); code != 2 {
		t.Fatalf("usage exit %d", code)
	}

	exe := filepath.Join(t.TempDir(), "yz")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := updateCommand(nil, env, exe, &stdout, &stderr); code != 0 {
		t.Fatalf("update exit %d stderr %q", code, stderr.String())
	}
	if stderr.String() != "yz updated to 1.2.3\n" || stdout.Len() != 0 {
		t.Fatalf("stdout %q stderr %q", stdout.String(), stderr.String())
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("updated binary = %q", got)
	}

	setVersion(t, "v1.2.3")
	stdout.Reset()
	stderr.Reset()
	if code := updateCommand(nil, env, exe, &stdout, &stderr); code != 0 || stdout.String() != "yz 1.2.3 is up to date\n" {
		t.Fatalf("up to date: %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestUpdateCommandManagedAndChecksum(t *testing.T) {
	setVersion(t, "v1.0.0")
	t.Setenv("YZ_REPO", "baires/yz")
	const tag = "v1.2.3"
	asset := update.AssetName(tag, runtime.GOOS, runtime.GOARCH)
	body := []byte("release-bytes")
	sum := sha256.Sum256(body)
	bad := "0000000000000000000000000000000000000000000000000000000000000000  " + asset + "\n"
	good := fmt.Sprintf("%x  %s\n", sum, asset)
	var manifest atomic.Value
	manifest.Store(good)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			http.Redirect(w, r, "/baires/yz/releases/tag/"+tag, http.StatusFound)
		case strings.HasSuffix(r.URL.Path, "/"+update.ChecksumsFile):
			_, _ = ioWrite(w, []byte(manifest.Load().(string)))
		case strings.HasSuffix(r.URL.Path, "/"+asset):
			_, _ = ioWrite(w, body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("YZ_GITHUB_BASE", srv.URL)

	gopath := t.TempDir()
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", gopath)
	exe := filepath.Join(gopath, "bin", "yz")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("go-old"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	env := envConfig{ConfigDir: t.TempDir()}
	if code := updateCommand(nil, env, exe, &stdout, &stderr); code != 0 {
		t.Fatalf("go install exit %d %q", code, stderr.String())
	}
	if stderr.String() != "yz is installed with go install. Run: go install github.com/baires/yz/cmd/yz@latest\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "go-old" {
		t.Fatal("go install binary was replaced")
	}

	t.Setenv("GOPATH", "")
	t.Setenv("GOBIN", "")
	brew := filepath.Join(t.TempDir(), "homebrew", "Cellar", "yz", "1.0.0", "bin", "yz")
	if err := os.MkdirAll(filepath.Dir(brew), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brew, []byte("brew-old"), 0o755); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := updateCommand(nil, env, brew, &stdout, &stderr); code != 0 {
		t.Fatalf("brew exit %d %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "brew upgrade yz") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	manifest.Store(bad)
	plain := filepath.Join(t.TempDir(), "yz")
	if err := os.WriteFile(plain, []byte("keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := updateCommand(nil, env, plain, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "checksum mismatch") {
		t.Fatalf("mismatch exit %d stderr %q", code, stderr.String())
	}
	got, err = os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep" {
		t.Fatalf("mismatch replaced binary with %q", got)
	}
}

func TestConfigCommand(t *testing.T) {
	dir := t.TempDir()
	env := envConfig{ConfigDir: dir}
	var stdout, stderr bytes.Buffer
	if code := runConfig(nil, env, &stdout, &stderr); code != 0 || stdout.String() != "auto-update: off\n" {
		t.Fatalf("show %d %q %q", code, stdout.String(), stderr.String())
	}
	if err := config.Save(dir, &config.Config{S3Secret: "super-secret", Bucket: "photos"}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := runConfig([]string{"auto-update", "on"}, env, &stdout, &stderr); code != 0 {
		t.Fatalf("set %d %q", code, stderr.String())
	}
	if stdout.String() != "auto-update: on\n" || strings.Contains(stdout.String()+stderr.String(), "super-secret") {
		t.Fatalf("stdout %q stderr %q", stdout.String(), stderr.String())
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AutoUpdate || cfg.S3Secret != "super-secret" || cfg.Bucket != "photos" {
		t.Fatalf("config = %+v", cfg)
	}
	stdout.Reset()
	if code := runConfig([]string{"auto-update", "off"}, env, &stdout, &stderr); code != 0 || stdout.String() != "auto-update: off\n" {
		t.Fatalf("off %d %q", code, stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runConfig([]string{"nope"}, env, &stdout, &stderr); code != 2 {
		t.Fatalf("usage exit %d", code)
	}
}

func setVersion(t *testing.T, v string) {
	t.Helper()
	prev := version
	version = v
	t.Cleanup(func() { version = prev })
}

func ioWrite(w http.ResponseWriter, data []byte) (int, error) {
	return w.Write(data)
}
