package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/baires/yz/internal/update"
)

const updateHelp = `usage: yz update [--check]

Download the latest yz release, verify its SHA-256, and replace this binary.

  yz update           Install the latest release
  yz update --check   Report whether a newer release exists

Homebrew installs are updated with: brew upgrade yz
go install copies are updated with: go install github.com/baires/yz/cmd/yz@latest
`

func runUpdate(args []string, env envConfig, stdout, stderr io.Writer) int {
	exe, err := os.Executable()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "yz:", err)
		return 1
	}
	return updateCommand(args, env, exe, stdout, stderr)
}

func updateCommand(args []string, env envConfig, exe string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("yz update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.Bool("check", false, "report whether a newer release exists")
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, updateHelp) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGHUP)
	defer stop()
	timeout := 60 * time.Second
	if *check {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tag, err := update.LatestTag(ctx, releaseBase(), releaseRepo(), userAgent())
	if err != nil {
		return updateErr(ctx, stderr, err)
	}
	_ = update.WriteCache(env.ConfigDir, update.Cache{CheckedAt: time.Now(), Latest: tag})
	if *check {
		_, _ = fmt.Fprintln(stdout, updateStatusLine(version, tag))
		return 0
	}
	if !update.IsNewer(tag, version) {
		_, _ = fmt.Fprintln(stdout, updateStatusLine(version, tag))
		return 0
	}
	if msg := update.ManagedMessage(update.InstallMethod(exe)); msg != "" {
		_, _ = fmt.Fprintln(stderr, msg)
		return 0
	}
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
		return updateErr(ctx, stderr, err)
	}
	_, _ = fmt.Fprintln(stderr, update.UpdatedLine(applied))
	return 0
}

func updateStatusLine(current, latest string) string {
	if line := update.AvailableLine(current, latest); line != "" {
		return line
	}
	shown := update.Display(current)
	if shown == "" {
		shown = update.Display(latest)
	}
	if shown == "" {
		return "yz is up to date"
	}
	return "yz " + shown + " is up to date"
}

func updateErr(ctx context.Context, stderr io.Writer, err error) int {
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		_, _ = fmt.Fprintln(stderr, "yz: interrupted")
		return 130
	}
	_, _ = fmt.Fprintln(stderr, "yz:", err)
	return 1
}

func releaseBase() string {
	if v := os.Getenv("YZ_GITHUB_BASE"); v != "" {
		return v
	}
	return update.DefaultBase
}

func releaseRepo() string {
	if v := os.Getenv("YZ_REPO"); v != "" {
		return v
	}
	return update.DefaultRepo
}

func userAgent() string {
	v := version
	if v == "" {
		v = "dev"
	}
	return "yz/" + v
}
