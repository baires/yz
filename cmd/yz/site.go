package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/baires/yz/internal/auth"
	"github.com/baires/yz/internal/config"
	"github.com/baires/yz/internal/desktop"
	"github.com/baires/yz/internal/history"
	"github.com/baires/yz/internal/r2"
	"github.com/baires/yz/internal/sites"
	"github.com/baires/yz/internal/ui"
)

const siteHelp = `usage: yz site <dir | site.zip>

Publish a static site (HTML, JS, CSS, assets) and get a link.

  yz site ./dist                  Upload a folder with index.html at its root
  yz site site.zip                Upload a zip (a single top folder is unwrapped)

Files go to R2 under sites/<id>/ and are served at <your domain>/s/<id>/ by
the yz-sites Worker, which yz deploys to your account on first use. Missing
paths without an extension fall back to index.html (SPA routing).
Requires a custom domain on the bucket; r2.dev cannot run Workers.
`

func runSite(args []string, env envConfig, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("yz site", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, siteHelp) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	src := fs.Arg(0)

	cfg, err := config.Load(env.ConfigDir)
	if err != nil {
		writeDiag(stderr, err.Error())
		return 1
	}
	if cfg.Bucket == "" || cfg.S3AccessKeyID == "" || cfg.S3Secret == "" {
		writeDiag(stderr, "yz: not configured — run yz setup")
		return 1
	}
	host, err := sites.Host(cfg.URLBase)
	if err != nil {
		writeDiag(stderr, "yz: "+err.Error())
		return 1
	}
	root, cleanup, err := sites.Prepare(src)
	if err != nil {
		writeDiag(stderr, err.Error())
		return 1
	}
	defer cleanup()
	files, err := sites.Collect(root)
	if err != nil {
		writeDiag(stderr, err.Error())
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGHUP)
	defer stop()
	if err := ensureSitesWorker(ctx, env, cfg, host, stderr); err != nil {
		if errors.Is(err, context.Canceled) {
			_, _ = fmt.Fprintln(stderr, "yz: interrupted")
			return 130
		}
		writeDiag(stderr, "yz: "+err.Error())
		return 1
	}

	id := sites.NewID()
	s3 := r2.S3{
		AccountID:   cfg.AccountID,
		Endpoint:    env.R2Endpoint,
		Credentials: r2.Credentials{AccessKeyID: cfg.S3AccessKeyID, Secret: cfg.S3Secret},
	}
	target := sites.URL(cfg.URLBase, id)
	var total int64
	for _, f := range files {
		total += f.Size
	}
	publish := func(ctx context.Context, progress func(int64, int64)) (string, error) {
		if err := sites.Upload(ctx, s3, cfg.Bucket, id, files, 0, progress); err != nil {
			return "", err
		}
		recordSite(env.ConfigDir, src, target, total, stderr)
		return target, nil
	}
	interactive := ui.Interactive(os.Stdin, stderr)
	if interactive {
		hold := ui.Interactive(os.Stdin, stdout)
		err = ui.RunUpload(ctx, os.Stdin, stderr, src, cfg.Bucket, hold, publish)
	} else {
		_, err = publish(ctx, nil)
	}
	if errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintln(stderr, "yz: interrupted")
		return 130
	}
	if err != nil {
		writeDiag(stderr, err.Error())
		return 1
	}
	_, _ = fmt.Fprintln(stdout, target)
	if !interactive {
		_, _ = fmt.Fprintf(stderr, "%d files published\n", len(files))
		_, _ = fmt.Fprintln(stderr, desktop.ClipboardNotice(desktop.CopyURL(ctx, target)))
	}
	return 0
}

// ensureSitesWorker lazily deploys the Worker when setup skipped it or the
// embedded script, bucket, or host changed since the last deployment.
func ensureSitesWorker(ctx context.Context, env envConfig, cfg *config.Config, host string, stderr io.Writer) error {
	version := sites.Version(cfg.Bucket, host)
	if cfg.SitesWorker == version {
		return nil
	}
	if cfg.AccessToken == "" && cfg.RefreshToken == "" {
		return errors.New("no Cloudflare login saved; run yz setup")
	}
	if time.Until(cfg.TokenExpiry) <= 30*time.Second {
		if cfg.RefreshToken == "" {
			return errors.New("saved Cloudflare login expired; run yz setup")
		}
		tokens, err := (auth.Client{BaseURL: env.OAuthBaseURL, ClientID: env.OAuthClientID}).Refresh(ctx, cfg.RefreshToken)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidGrant) {
				return errors.New("saved Cloudflare login is no longer valid; run yz setup")
			}
			return err
		}
		cfg.AccessToken, cfg.RefreshToken, cfg.TokenExpiry = tokens.AccessToken, tokens.RefreshToken, tokens.Expiry
		if err := config.Save(env.ConfigDir, cfg); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintln(stderr, "Deploying the yz-sites Worker to "+sites.RoutePattern(host))
	d := sites.Deployer{BaseURL: env.APIBaseURL, AccessToken: cfg.AccessToken}
	if err := d.Deploy(ctx, cfg.AccountID, cfg.Bucket, host); err != nil {
		return err
	}
	cfg.SitesWorker = version
	return config.Save(env.ConfigDir, cfg)
}

func recordSite(configDir, src, url string, size int64, stderr io.Writer) {
	name := strings.TrimSuffix(filepath.Base(filepath.Clean(src)), filepath.Ext(src))
	if !strings.EqualFold(filepath.Ext(src), ".zip") {
		name = filepath.Base(filepath.Clean(src))
	}
	entry := history.Entry{Kind: history.KindSite, URL: url, File: name, Size: size, CreatedAt: time.Now()}
	if err := history.Add(configDir, entry); err != nil {
		_, _ = fmt.Fprintln(stderr, "yz: warning: could not record share history:", err)
	}
}
