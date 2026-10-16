package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/baires/yz/internal/config"
	"github.com/baires/yz/internal/update"
	"github.com/charmbracelet/x/term"
)

const (
	defaultCheckTimeout    = 2 * time.Second
	defaultDownloadTimeout = 60 * time.Second
)

// updateCheck prints a cached update notice after the command. A release
// refresh, at most once every 24 hours, runs in a goroutine and is waited
// on only after the command has finished. Startup itself only reads the
// cache file.
type updateCheck struct {
	current    string
	notice     string
	exe        string
	configDir  string
	checkTO    time.Duration
	downloadTO time.Duration
	done       chan struct{}
	result     checkResult
}

type checkResult struct {
	latest  string
	updated bool
}

func beginUpdateCheck(args []string, env envConfig, stdout, stderr io.Writer) *updateCheck {
	if len(args) > 0 && args[0] == "update" {
		return &updateCheck{}
	}
	return launchUpdateCheck(noticePolicy(stdout, stderr), env, "", defaultCheckTimeout, defaultDownloadTimeout)
}

func launchUpdateCheck(p update.NoticePolicy, env envConfig, exe string, checkTO, downloadTO time.Duration) *updateCheck {
	uc := &updateCheck{
		current:    p.Current,
		exe:        exe,
		configDir:  env.ConfigDir,
		checkTO:    checkTO,
		downloadTO: downloadTO,
	}
	if p.Skip() || env.ConfigDir == "" {
		return uc
	}
	cached, err := update.ReadCache(env.ConfigDir)
	if err != nil {
		return uc
	}
	if line := update.AvailableLine(p.Current, cached.Latest); line != "" {
		uc.notice = line
	}
	if !update.Due(cached.CheckedAt, time.Now()) {
		if uc.notice == "" {
			return uc
		}
		uc.done = make(chan struct{})
		go uc.cachedAuto(cached)
		return uc
	}
	uc.done = make(chan struct{})
	go uc.refresh(cached)
	return uc
}

func (uc *updateCheck) finish(stderr io.Writer) {
	if uc == nil {
		return
	}
	if uc.done != nil {
		<-uc.done
	}
	if uc.result.updated {
		if line := update.UpdatedLine(uc.result.latest); line != "" {
			_, _ = fmt.Fprintln(stderr, line)
		}
		return
	}
	if uc.result.latest != "" {
		if line := update.AvailableLine(uc.current, uc.result.latest); line != "" {
			_, _ = fmt.Fprintln(stderr, line)
		}
		return
	}
	if uc.notice != "" {
		_, _ = fmt.Fprintln(stderr, uc.notice)
	}
}

func (uc *updateCheck) refresh(prev update.Cache) {
	defer close(uc.done)
	ctx, cancel := context.WithTimeout(context.Background(), uc.checkTO)
	tag, err := update.LatestTag(ctx, releaseBase(), releaseRepo(), userAgent())
	cancel()
	now := time.Now()
	if err != nil {
		prev.CheckedAt = now
		_ = update.WriteCache(uc.configDir, prev)
		return
	}
	prev.CheckedAt = now
	prev.Latest = tag
	prev.ApplyAttemptedAt = time.Time{}
	_ = update.WriteCache(uc.configDir, prev)
	uc.result.latest = tag
	uc.maybeAuto(prev, tag)
}

func (uc *updateCheck) cachedAuto(cached update.Cache) {
	defer close(uc.done)
	uc.maybeAuto(cached, cached.Latest)
}

func (uc *updateCheck) maybeAuto(cached update.Cache, tag string) {
	if !update.IsNewer(tag, uc.current) || !autoEnabled(uc.configDir) {
		return
	}
	if !cached.ApplyAttemptedAt.IsZero() && !cached.ApplyAttemptedAt.Before(cached.CheckedAt) {
		return
	}
	exe, err := uc.executable()
	if err != nil {
		return
	}
	if update.InstallMethod(exe) != update.MethodDirect {
		return
	}
	cached.Latest = tag
	if cached.CheckedAt.IsZero() {
		cached.CheckedAt = time.Now()
	}
	cached.ApplyAttemptedAt = time.Now()
	_ = update.WriteCache(uc.configDir, cached)
	ctx, cancel := context.WithTimeout(context.Background(), uc.downloadTO)
	defer cancel()
	applied, err := update.Apply(ctx, update.Request{
		BaseURL:   releaseBase(),
		Repo:      releaseRepo(),
		Tag:       tag,
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		Exe:       exe,
		UserAgent: userAgent(),
	})
	if err != nil {
		return
	}
	uc.result.updated = true
	uc.result.latest = applied
}

func (uc *updateCheck) executable() (string, error) {
	if uc.exe != "" {
		return uc.exe, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(filepath.Base(exe), ".test") {
		return "", errors.New("refusing to update a test binary")
	}
	return exe, nil
}

func noticePolicy(stdout, stderr io.Writer) update.NoticePolicy {
	return update.NoticePolicy{
		Current:   version,
		StdoutTTY: isTerminalWriter(stdout),
		StderrTTY: isTerminalWriter(stderr),
		CI:        inCI(),
		Disabled:  updateCheckDisabled(),
	}
}

func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || f == nil {
		return false
	}
	return term.IsTerminal(f.Fd())
}

func inCI() bool {
	v, ok := os.LookupEnv("CI")
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func updateCheckDisabled() bool {
	v, ok := os.LookupEnv("YZ_NO_UPDATE_CHECK")
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func autoEnabled(dir string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("YZ_AUTO_UPDATE"))) {
	case "1", "true", "yes", "on":
		return true
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return false
	}
	return cfg.AutoUpdate
}
