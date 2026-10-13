package setup

import (
	"context"
	"errors"

	"github.com/baires/yz/internal/config"
	"github.com/baires/yz/internal/sites"
)

// provisionSites deploys the yz-sites Worker when the bucket has a custom
// domain. It is best effort: setup still succeeds for file sharing, and
// yz site retries the deployment on first use.
func provisionSites(ctx context.Context, cfg *config.Config, opts Options, ui Interaction) {
	host, err := sites.Host(cfg.URLBase)
	if err != nil {
		if cfg.URLBase != "" {
			ui.Notice("static sites: skipped (needs a custom domain; r2.dev cannot run Workers)")
		}
		return
	}
	version := sites.Version(cfg.Bucket, host)
	if cfg.SitesWorker == version {
		return
	}
	ui.Status(StepSave, "Deploying the yz-sites Worker")
	d := sites.Deployer{BaseURL: opts.APIBaseURL, AccessToken: cfg.AccessToken}
	err = d.Deploy(ctx, cfg.AccountID, cfg.Bucket, host)
	if errors.Is(err, sites.ErrPermission) {
		// Logins saved before static sites lack the Workers scopes: upgrade
		// once, the same way account discovery upgrades memberships.read.
		ui.Notice("Static sites need Workers access; starting a new login")
		tokens, loginErr := loginTokens(ctx, opts, ui)
		if loginErr != nil {
			ui.Notice("static sites: login not upgraded (" + loginErr.Error() + "); yz site will retry")
			return
		}
		cfg.AccessToken, cfg.RefreshToken, cfg.TokenExpiry = tokens.AccessToken, tokens.RefreshToken, tokens.Expiry
		d.AccessToken = cfg.AccessToken
		ui.Status(StepSave, "Deploying the yz-sites Worker")
		err = d.Deploy(ctx, cfg.AccountID, cfg.Bucket, host)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		ui.Notice("static sites: Worker not deployed (" + err.Error() + "); yz site will retry")
		return
	}
	cfg.SitesWorker = version
	ui.Notice("static sites " + host + "/s/*")
}
