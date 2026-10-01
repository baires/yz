package setup

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/baires/yz/internal/auth"
	"github.com/baires/yz/internal/config"
)

func login(ctx context.Context, opts Options, ui Interaction) error {
	ui.Status(StepLogin, "Checking login")
	client := auth.Client{BaseURL: opts.OAuthBaseURL, ClientID: opts.OAuthClientID}
	cfg, err := config.Load(opts.ConfigDir)
	if err != nil {
		cfg = &config.Config{}
	}
	if cfg.AccessToken != "" && time.Until(cfg.TokenExpiry) > 30*time.Second {
		ui.Status(StepLogin, "already logged in")
		ui.Notice("already logged in")
		return nil
	}
	if cfg.RefreshToken != "" {
		ui.Status(StepLogin, "Refreshing login")
		tokens, refreshErr := client.Refresh(ctx, cfg.RefreshToken)
		if refreshErr != nil && !errors.Is(refreshErr, auth.ErrInvalidGrant) {
			return refreshErr
		}
		if refreshErr == nil {
			cfg.AccessToken, cfg.RefreshToken, cfg.TokenExpiry = tokens.AccessToken, tokens.RefreshToken, tokens.Expiry
			if err := config.Save(opts.ConfigDir, cfg); err != nil {
				return err
			}
			ui.Status(StepLogin, "login refreshed")
			ui.Notice("login refreshed")
			return nil
		}
		ui.Notice("saved login is no longer valid; starting a new login")
	}
	tokens, err := loginTokens(ctx, opts, ui)
	if err != nil {
		return err
	}
	cfg.AccessToken, cfg.RefreshToken, cfg.TokenExpiry = tokens.AccessToken, tokens.RefreshToken, tokens.Expiry
	if err := config.Save(opts.ConfigDir, cfg); err != nil {
		return err
	}
	ui.Status(StepLogin, "logged in")
	ui.Notice("logged in")
	return nil
}

func loginTokens(ctx context.Context, opts Options, ui Interaction) (auth.Tokens, error) {
	ui.Status(StepLogin, "Waiting for browser authorization")
	timeout := 5 * time.Minute
	if raw := os.Getenv("YZ_LOGIN_TIMEOUT"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			return auth.Tokens{}, errors.New("YZ_LOGIN_TIMEOUT must be a positive duration")
		}
		timeout = parsed
	}
	return (auth.Client{BaseURL: opts.OAuthBaseURL, ClientID: opts.OAuthClientID}).Login(ctx, ui, timeout)
}
