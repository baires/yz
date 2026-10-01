package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/baires/yz/internal/setup"
	"github.com/baires/yz/internal/ui"
)

func runSetup(env envConfig, input io.Reader, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGHUP)
	defer stop()
	if err := setupFlow(ctx, env, input, stderr); err != nil {
		if errors.Is(err, context.Canceled) {
			_, _ = fmt.Fprintln(stderr, "yz: interrupted")
			return 130
		}
		_, _ = fmt.Fprintln(stderr, "yz:", err)
		return 1
	}
	return 0
}

func setupFlow(ctx context.Context, env envConfig, input io.Reader, stderr io.Writer) error {
	opts := setup.Options{
		ConfigDir:     env.ConfigDir,
		OAuthBaseURL:  env.OAuthBaseURL,
		OAuthClientID: env.OAuthClientID,
		APIBaseURL:    env.APIBaseURL,
		R2Endpoint:    env.R2Endpoint,
	}
	if ui.Interactive(input, stderr) {
		_, err := ui.RunSetup(ctx, opts, input, stderr)
		return err
	}
	_, err := setup.Run(ctx, opts, setup.NewPlain(input, stderr))
	return err
}
