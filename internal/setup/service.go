package setup

import (
	"context"

	"github.com/baires/yz/internal/config"
)

func Run(ctx context.Context, opts Options, ui Interaction) (*config.Config, error) {
	if err := login(ctx, opts, ui); err != nil {
		return nil, err
	}
	cfg, err := config.Load(opts.ConfigDir)
	if err != nil {
		return nil, err
	}
	if err := configureR2(ctx, cfg, opts, ui); err != nil {
		return nil, err
	}
	ui.Status(StepSave, "Saving")
	if err := config.Save(opts.ConfigDir, cfg); err != nil {
		ui.Notice(err.Error())
		return nil, err
	}
	access := "private"
	if cfg.URLBase != "" {
		access = cfg.URLBase
	}
	ui.Notice("account " + cfg.AccountID)
	ui.Notice("bucket " + cfg.Bucket)
	ui.Notice("access " + access)
	ui.Notice("example yz screenshot.png")
	ui.Notice("setup complete")
	return cfg, nil
}
