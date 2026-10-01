package setup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/baires/yz/internal/config"
	"github.com/baires/yz/internal/r2"
)

func configureR2(ctx context.Context, cfg *config.Config, opts Options, ui Interaction) error {
	api := r2.API{BaseURL: opts.APIBaseURL, AccessToken: cfg.AccessToken}
	account := cfg.AccountID
	if account == "" {
		ui.Status(StepAccount, "Loading accounts")
		accounts, err := api.Accounts(ctx)
		if errors.Is(err, r2.ErrAccountReadRequired) {
			ui.Notice("Account discovery needs memberships.read; starting a new login")
			tokens, loginErr := loginTokens(ctx, opts, ui)
			if loginErr != nil {
				return loginErr
			}
			cfg.AccessToken, cfg.RefreshToken, cfg.TokenExpiry = tokens.AccessToken, tokens.RefreshToken, tokens.Expiry
			if err := config.Save(opts.ConfigDir, cfg); err != nil {
				return err
			}
			api.AccessToken = cfg.AccessToken
			ui.Status(StepAccount, "Loading accounts")
			accounts, err = api.Accounts(ctx)
			if errors.Is(err, r2.ErrAccountReadRequired) {
				return errors.New("account discovery denied; add memberships.read (Memberships Read) to the registered OAuth client scopes in Cloudflare, then run yz setup")
			}
		}
		if err != nil {
			return err
		}
		account, err = selectAccount(ctx, accounts, ui)
		if err != nil {
			return err
		}
	}
	if !r2.ValidHex(account, 32) {
		return errors.New("account ID must be 32 hexadecimal characters; run yz setup")
	}
	ui.Status(StepBucket, "Loading buckets")
	buckets, err := api.Buckets(ctx, account)
	if err != nil {
		return err
	}
	if len(buckets) == 0 {
		return errors.New("no buckets found; create an R2 bucket in the Cloudflare dashboard, then run yz setup")
	}
	bucket, err := selectBucket(ctx, buckets, cfg.Bucket, ui)
	if err != nil {
		return err
	}
	ui.Status(StepBucket, "Checking domains")
	custom, err := api.CustomDomains(ctx, account, bucket)
	if err != nil {
		return err
	}
	managed, enabled, err := api.ManagedDomain(ctx, account, bucket, false)
	if err != nil {
		return err
	}
	base := ""
	if len(custom) > 0 {
		base = custom[0]
	} else if enabled {
		base = managed
	}
	creds := r2.Credentials{AccessKeyID: cfg.S3AccessKeyID, Secret: cfg.S3Secret}
	reusingCredentials := cfg.AccountID == account && cfg.Bucket == bucket && creds.AccessKeyID != "" && creds.Secret != ""
	if !reusingCredentials {
		var err error
		creds, err = collectCredentials(ctx, ui, account, bucket, opts.R2Endpoint)
		if err != nil {
			return err
		}
	}
	s3 := r2.S3{AccountID: account, Endpoint: opts.R2Endpoint, Credentials: creds}
	if reusingCredentials {
		ui.Status(StepCredentials, "Checking your saved R2 credentials")
		if err := s3.CheckBucket(ctx, bucket); err != nil {
			return err
		}
	}
	keepPrivate := reusingCredentials && cfg.URLBase == ""
	if base == "" && !keepPrivate {
		ui.Status(StepAccess, "Choosing access")
		public, err := ui.ConfirmPublic(ctx)
		if err != nil {
			return err
		}
		if public {
			base, _, err = api.ManagedDomain(ctx, account, bucket, true)
			if err != nil {
				return err
			}
		} else {
			ui.Notice("Bucket remains private; shares use signed links that expire after 24h by default.")
		}
	}
	cfg.AccountID, cfg.Bucket, cfg.URLBase = account, bucket, base
	cfg.S3AccessKeyID, cfg.S3Secret = creds.AccessKeyID, creds.Secret
	return nil
}

func collectCredentials(ctx context.Context, ui Interaction, account, bucket, endpoint string) (r2.Credentials, error) {
	ui.Status(StepCredentials, "Waiting for credentials")
	ui.Notice("Create an R2 API token with Object Read & Write permission for this bucket:")
	ui.Notice("Dashboard: https://dash.cloudflare.com/" + account + "/r2/api-tokens")
	validateKey := func(value string) error {
		if !r2.ValidHex(value, 32) {
			return errors.New("access key ID must be 32 hexadecimal characters (0–9, a–f). Edit it below and press enter")
		}
		return nil
	}
	validateSecret := func(value string) error {
		if !r2.ValidHex(value, 64) {
			return errors.New("secret access key must be 64 hexadecimal characters (0–9, a–f). Edit it below and press enter")
		}
		return nil
	}
	for attempt := 1; ; attempt++ {
		keyLabel := "Access Key ID:"
		secretLabel := "Secret Access Key (hidden):"
		if attempt > 1 {
			keyLabel = fmt.Sprintf("Access Key ID (%d):", attempt)
			secretLabel = fmt.Sprintf("Secret Access Key (%d):", attempt)
		}
		key, err := ui.Text(ctx, TextRequest{
			Label: keyLabel, Secret: true, Validate: validateKey,
			Placeholder: "Paste your Access Key ID", Length: 32,
			Hint: "Use the Access Key ID from your R2 token. Your input stays hidden.",
		})
		if err != nil {
			return r2.Credentials{}, err
		}
		secret, err := ui.Text(ctx, TextRequest{
			Label: secretLabel, Secret: true, Validate: validateSecret,
			Placeholder: "Paste your Secret Access Key", Length: 64,
			Hint: "Use the Secret Access Key shown when you created the R2 token.",
		})
		if err != nil {
			return r2.Credentials{}, err
		}
		creds := r2.Credentials{AccessKeyID: key, Secret: secret}
		ui.Status(StepCredentials, "Checking credentials against your bucket")
		err = (r2.S3{AccountID: account, Endpoint: endpoint, Credentials: creds}).CheckBucket(ctx, bucket)
		if err == nil {
			return creds, nil
		}
		if errors.Is(err, r2.ErrCredentialsRejected) {
			if failFast(ui) {
				return r2.Credentials{}, err
			}
			ui.Notice(err.Error())
			ui.Status(StepCredentials, "Replace the rejected credentials")
			ui.Notice("Replace the rejected credentials")
			continue
		}
		if !transientFailure(err) || failFast(ui) {
			return r2.Credentials{}, err
		}
		again, retryErr := ui.Retry(ctx, err.Error())
		if retryErr != nil {
			return r2.Credentials{}, retryErr
		}
		if !again {
			return r2.Credentials{}, err
		}
		ui.Status(StepCredentials, "Enter replacement credentials")
	}
}

func failFast(ui Interaction) bool {
	_, ok := ui.(interface{ keepsPlainErrors() })
	return ok
}

func transientFailure(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "R2 connection failed") || strings.Contains(msg, "HTTP 5")
}

func selectBucket(ctx context.Context, buckets []string, saved string, ui Interaction) (string, error) {
	for _, bucket := range buckets {
		if bucket == saved {
			ui.Notice("Using R2 bucket: " + bucket)
			return bucket, nil
		}
	}
	ui.Status(StepBucket, "Choose an R2 bucket")
	ui.Notice("R2 buckets:")
	choices := make([]Choice, 0, len(buckets))
	for _, bucket := range buckets {
		choices = append(choices, Choice{Value: bucket, Title: bucket})
	}
	return ui.Choose(ctx, "Choose a bucket number:", choices)
}

func selectAccount(ctx context.Context, accounts []r2.Account, ui Interaction) (string, error) {
	if len(accounts) == 0 {
		return "", errors.New("no accessible Cloudflare accounts; check your Cloudflare login")
	}
	if len(accounts) == 1 {
		ui.Status(StepAccount, "Using Cloudflare account: "+accounts[0].Name)
		ui.Notice("Using Cloudflare account: " + accounts[0].Name)
		return accounts[0].ID, nil
	}
	ui.Status(StepAccount, "Choose an account")
	ui.Notice("Cloudflare accounts:")
	choices := make([]Choice, 0, len(accounts))
	for _, account := range accounts {
		choices = append(choices, Choice{Value: account.ID, Title: account.Name, Description: account.ID})
	}
	return ui.Choose(ctx, "Choose an account number:", choices)
}
