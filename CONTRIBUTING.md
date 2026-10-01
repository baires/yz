# Contributing

Thanks for your interest in contributing to `yz`!

## Development Setup

Prerequisites:

- **Go 1.26.8+** (Go's automatic toolchain selection downloads it when your
  installed version is older)
- **Python 3** for the terminal (PTY) end-to-end tests
- For `make race`: CGO and a C compiler

Clone and build:

```bash
git clone https://github.com/baires/yz.git
cd yz
go build -o yz ./cmd/yz
```

## Make Targets

Run `make` to list all commands. Local development and CI use the same checks:

```bash
make tools       # Download and verify pinned tools (optional; lint installs on demand)
make fmt         # gofumpt formatting and goimports import organization
make lint        # Formatting checks, vet, staticcheck, modernize, and other linters
make check       # Lint, module tidiness, all tests, build, and govulncheck
make race        # Race-check the test harness and the binary exercised by E2E tests
```

The tools are pinned to golangci-lint 2.14.0 and govulncheck 1.8.0 and are
cached under `.tools/`; neither adds development-tool dependencies to `go.mod`.
Tool bootstrap requires `curl`, `tar`, and `sha256sum` or `shasum`.

`make lint` checks the whole codebase, including tests, and fails on formatting
drift. `make tidy-check` uses `go mod tidy -diff` to check module metadata
without rewriting it. `.editorconfig` supplies indentation and newline settings
for editors.

## Testing

`go test ./e2e/` drives the compiled binary against in-process fakes, so tests
are hermetic — no Cloudflare account needed. Terminal scenarios use the PTY
driver in `e2e/pty_driver.py` and fail (rather than skip) if Python 3 is
missing.

Before opening a PR, run `make check` and make sure it is green.

## Pull Requests and Releases

Merged pull requests release themselves. Label the PR, merge it to `main`, and
[release-train](https://github.com/marketplace/actions/release-train) bumps the
version, writes the release notes, and publishes the binaries `install.sh`
downloads.

| Label | Bump |
| --- | --- |
| `semver:breaking` | major |
| `semver:minor` | minor |
| `semver:patch` | patch |
| `semver:none` | no release |

`feat`, `fix`, `perf`, `refactor`, `docs`, `chore`, `style`, and
`breaking change` are accepted aliases. An unlabeled PR fails the Release
check. Do not tag releases by hand.

## Real-Account Verification Checklist

If your change touches setup, auth, or the upload path, verify it against a
real Cloudflare account before merging:

- [ ] Run `yz setup` against a real Cloudflare account.
- [ ] Complete browser OAuth login and select a real R2 bucket.
- [ ] Verify the pasted R2 API token is validated and saved to
      `~/.config/yz/config.json` with `0600` permissions.
- [ ] Run `yz <image.png>` and fetch the printed URL in a fresh private
      browser window. Verify HTTP 200 and the image displays correctly.
- [ ] Run `yz --expires=1m <file.txt>`. Verify the link works immediately,
      then verify it returns HTTP 403 / AccessDenied after 1 minute.
- [ ] Run `yz --signed <file.txt>` and verify the presigned URL fetches the
      exact file.

## Forking: Your Own OAuth Client

`yz` ships with a registered **public** OAuth client ID
(`internal/auth/oauth.go`, `DefaultClientID`). Public clients have no secret —
PKCE protects the flow — so embedding it is safe and standard practice (same
approach as `wrangler` and `gh`).

If you fork `yz`, your users' consent screens will show the upstream app's
name and the registered loopback redirect ports (8974–8976). Register your own
OAuth client in the Cloudflare dashboard with redirect URIs
`http://127.0.0.1:8974/callback` through `8976` and the scopes
`memberships.read`, `workers-r2.read`, `workers-r2.write`, `offline_access`,
then point the binary at it — either set `YZ_OAUTH_CLIENT_ID` or change
`DefaultClientID`.

## Code of Conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md). Please
read it before participating.
