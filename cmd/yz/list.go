package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/baires/yz/internal/config"
	"github.com/baires/yz/internal/history"
	"github.com/baires/yz/internal/r2"
	"github.com/baires/yz/internal/ui"
)

// runList prints previously recorded shares, newest first. On a terminal it
// shows an interactive table; piped output stays plain text.
func runList(env envConfig, stdout, stderr io.Writer) int {
	entries, err := history.Load(env.ConfigDir)
	if err != nil {
		writeDiag(stderr, err.Error())
		return 1
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(stdout, "no shares yet — share a file with: yz <file>")
		return 0
	}
	if ui.Interactive(os.Stdin, stdout) {
		if err := ui.RunShareList(os.Stdin, stdout, entries, func(ctx context.Context, e history.Entry) error {
			return deleteShare(ctx, env, e)
		}); err != nil {
			writeDiag(stderr, err.Error())
			return 1
		}
		return 0
	}
	now := time.Now()
	for _, e := range entries {
		status := ""
		if e.ExpiresAt != nil {
			if e.Expired(now) {
				status = " (expired)"
			} else {
				status = " (expires " + history.FormatTime(*e.ExpiresAt) + ")"
			}
		}
		_, _ = fmt.Fprintf(stdout, "%s  %8s  %s%s\n    %s\n",
			history.FormatTime(e.CreatedAt), ui.HumanSize(e.Size), ui.SafeText(e.Label()), status, e.URL)
	}
	return 0
}

// deleteShare removes a share's R2 objects (one key, or every key under a
// site's sites/<id>/ prefix) and then its history entry. The Worker stays.
func deleteShare(ctx context.Context, env envConfig, e history.Entry) error {
	cfg, err := config.Load(env.ConfigDir)
	if err != nil {
		return err
	}
	if cfg.Bucket == "" || cfg.S3AccessKeyID == "" || cfg.S3Secret == "" {
		return errors.New("not configured — run yz setup")
	}
	key, isPrefix, err := e.Target(cfg.Bucket)
	if err != nil {
		return err
	}
	s3 := r2.S3{
		AccountID:   cfg.AccountID,
		Endpoint:    env.R2Endpoint,
		Credentials: r2.Credentials{AccessKeyID: cfg.S3AccessKeyID, Secret: cfg.S3Secret},
	}
	if isPrefix {
		_, err = s3.DeletePrefix(ctx, cfg.Bucket, key)
	} else {
		err = s3.DeleteObject(ctx, cfg.Bucket, key)
	}
	if err != nil {
		return err
	}
	return history.Remove(env.ConfigDir, e.URL, e.CreatedAt)
}
