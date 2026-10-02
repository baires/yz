package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/baires/yz/internal/config"
	"github.com/baires/yz/internal/desktop"
	"github.com/baires/yz/internal/history"
	"github.com/baires/yz/internal/share"
	"github.com/baires/yz/internal/ui"
)

const shareUsage = "usage: yz [--signed] [--expires 24h] [--domain HOST] <file>"

const shareHelp = shareUsage + `

Share a file and get a link you can paste anywhere.

  yz setup                        Connect Cloudflare and choose an R2 bucket
  yz screenshot.png               Upload a file and print its link
  yz --expires=1h report.pdf       Create a signed link that expires in an hour
  yz --domain=files.example.com photo.jpg
                                  Use a custom domain for this share
  yz list                         Show previous shares
  yz version                      Show the installed version

Links are copied to your clipboard automatically when available.
After an interactive upload, press o to open the link or enter to finish.
`

// runShare is the default command: upload a file and print its share URL.
func runShare(args []string, env envConfig, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("yz", flag.ContinueOnError)
	fs.SetOutput(stderr)
	signed := fs.Bool("signed", false, "print a presigned URL instead of a public one")
	expires := fs.String("expires", "", "expire the share after a duration (e.g. 24h); implies --signed")
	domain := fs.String("domain", "", "override the configured URL base host")
	fs.Usage = func() {
		if ui.Animated(stderr) {
			ui.WriteShareHelp(stderr, shareHelp)
			return
		}
		_, _ = fmt.Fprint(stderr, shareHelp)
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	var expiry time.Duration
	hasExpires := false
	if *expires != "" {
		d, err := time.ParseDuration(*expires)
		if err != nil {
			writeDiag(stderr, fmt.Sprintf("invalid --expires duration: %v", err))
			return 2
		}
		if d <= 0 || d > 7*24*time.Hour {
			writeDiag(stderr, fmt.Sprintf("invalid --expires duration %v (must be between 1s and 7d)", d))
			return 2
		}
		expiry = d
		hasExpires = true
	}

	opts := share.Options{
		Path:       fs.Arg(0),
		Signed:     *signed,
		Expires:    expiry,
		HasExpires: hasExpires,
		Domain:     *domain,
		R2Endpoint: env.R2Endpoint,
	}
	opts.PartSize, _ = strconv.ParseInt(os.Getenv("YZ_MULTIPART_PART_SIZE"), 10, 64)
	opts.Concurrency, _ = strconv.Atoi(os.Getenv("YZ_MULTIPART_CONCURRENCY"))

	cfg, err := config.Load(env.ConfigDir)
	if err != nil {
		writeDiag(stderr, err.Error())
		return 1
	}
	if cfg.Bucket == "" || cfg.S3AccessKeyID == "" || cfg.S3Secret == "" {
		writeDiag(stderr, "yz: not configured — run yz setup")
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGHUP)
	defer stop()
	var url bytes.Buffer
	if ui.Interactive(os.Stdin, stderr) {
		hold := ui.Interactive(os.Stdin, stdout)
		err := ui.RunUpload(ctx, os.Stdin, stderr, opts.Path, cfg.Bucket, hold, func(ctx context.Context, progress func(int64, int64)) (string, error) {
			opts.Progress = progress
			expires, err := share.Share(ctx, cfg, opts, &url)
			if err != nil {
				return "", err
			}
			target := strings.TrimSpace(url.String())
			recordShare(env.ConfigDir, opts.Path, target, expires, stderr)
			if !hold {
				if _, err := io.Copy(stdout, &url); err != nil {
					return "", err
				}
			}
			return target, nil
		})
		if errors.Is(err, context.Canceled) {
			_, _ = fmt.Fprintln(stderr, "yz: interrupted")
			return 130
		}
		if err != nil {
			writeDiag(stderr, err.Error())
			return 1
		}
		if hold {
			if _, err := io.Copy(stdout, &url); err != nil {
				writeDiag(stderr, err.Error())
				return 1
			}
		}
		return 0
	}
	expiresAt, err := share.Share(ctx, cfg, opts, &url)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			_, _ = fmt.Fprintln(stderr, "yz: interrupted")
			return 130
		}
		writeDiag(stderr, err.Error())
		return 1
	}
	target := strings.TrimSpace(url.String())
	recordShare(env.ConfigDir, opts.Path, target, expiresAt, stderr)
	if _, err := io.Copy(stdout, &url); err != nil {
		writeDiag(stderr, err.Error())
		return 1
	}
	_, _ = fmt.Fprintln(stderr, desktop.ClipboardNotice(desktop.CopyURL(ctx, target)))
	return 0
}

// recordShare appends the completed share to the local history. It is
// best-effort: the share itself already succeeded, so a history write
// failure only prints a warning.
func recordShare(configDir, path, url string, expires *time.Time, stderr io.Writer) {
	entry := history.Entry{
		URL:       url,
		File:      filepath.Base(path),
		CreatedAt: time.Now(),
		ExpiresAt: expires,
	}
	if info, err := os.Stat(path); err == nil {
		entry.Size = info.Size()
	}
	if err := history.Add(configDir, entry); err != nil {
		_, _ = fmt.Fprintln(stderr, "yz: warning: could not record share history:", err)
	}
}

func writeDiag(stderr io.Writer, msg string) {
	if ui.Animated(stderr) {
		ui.WriteShareError(stderr, msg)
		return
	}
	_, _ = fmt.Fprintln(stderr, ui.SafeText(msg))
}
