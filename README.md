# `yz` — Instant File Sharing via Cloudflare R2

`yz` is a single-binary CLI for instant file sharing backed by Cloudflare R2: type `yz myfile.png`, get a ready-to-paste public URL. Recipients just click the link; no authentication needed on their side.

- **Single static binary**: no AWS SDK, no runtime dependencies; under 10 MB for macOS and Linux (amd64 and arm64).
- **Fast**: terminal-to-link in under ~5 seconds.
- **Unguessable keys**: files are uploaded under `<16-char random base62>/<filename>` prefixes.
- **Custom domains & r2.dev**: automatic detection and configuration of managed `r2.dev` or custom domains.
- **Time-bounded & signed shares**: `--expires` and `--signed` for presigned URLs.
- **Local history**: `yz list` shows previous shares, newest first.

```bash
$ yz screenshot.png
https://pub-yourbucket.r2.dev/3kK9Xw0Lp2QmZb8N/screenshot.png
```

## Installation

### Homebrew (macOS)

```bash
brew install baires/tap/yz
```

### Install script (macOS & Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/baires/yz/master/install.sh | sh
```

The script downloads the latest [release](https://github.com/baires/yz/releases) for your OS and architecture, verifies the published SHA-256, and installs `yz` onto your `PATH`. Pin a version or install directory with `--version` / `--bin-dir`; see `install.sh --help`.

### Go

```bash
go install github.com/baires/yz/cmd/yz@latest
```

### Manual download

Download a static binary from [Releases](https://github.com/baires/yz/releases) — names are `yz_<tag>_<os>_<arch>` — then:

```bash
chmod +x yz_*
mv yz_* /usr/local/bin/yz
```

## Quickstart

### 1. One-time setup

```bash
yz setup
```

Setup walks you through: browser OAuth login to Cloudflare → picking your account and R2 bucket → choosing a public URL base (existing custom domain, managed `r2.dev`, or private) → pasting an R2 API token with Object Read & Write. Credentials are validated and saved to `~/.config/yz/config.json` with `0600` permissions.

![Interactive setup demo](docs/demos/setup.gif)

[Watch setup as MP4](docs/demos/setup.mp4).

### 2. Share files

```bash
$ yz presentation.pdf
https://pub-yourbucket.r2.dev/8xV1K2m9Qp4Lw0Zb/presentation.pdf
```

Only the URL goes to stdout; progress, clipboard notices, and errors go to stderr. On an interactive terminal the link is copied to your clipboard automatically, and the completion screen lets you press `o` to open it in a browser.

![File upload demo](docs/demos/upload.gif)

[Watch upload as MP4](docs/demos/upload.mp4).

## Usage & Options

```
usage: yz [--signed] [--expires 24h] [--domain HOST] <file>
```

| Flag | Description |
| --- | --- |
| *(default)* | Public URL via your bucket's custom domain or `r2.dev` |
| `--expires 2h` | Presigned URL that R2 rejects after the given duration (`1s`–`7d`) |
| `--signed` | Force a presigned URL even when a public base is configured |
| `--domain HOST` | Override the configured URL base for this share |
| `yz list` | Show previous shares, newest first |
| `yz version` | Print the version |

```bash
yz --expires=2h secret_report.pdf      # expires automatically
yz --signed internal-doc.docx          # presigned even with a public domain
yz --domain=cdn.example.com banner.webp
yz list                                # previous shares, newest first
```

`yz list` prints each share's time, size, filename, expiry, and URL. On a terminal it is an interactive table: enter copies the selected link, `q` quits. Piped output stays plain text. History is stored locally in `~/.config/yz/shares.json` (`0600`). Presigned links, including the private-bucket fallback, are marked with their expiry.

![CLI help demo](docs/demos/help.gif)

[Watch help as MP4](docs/demos/help.mp4). See [recording instructions](docs/demos/README.md) to regenerate the demos with VHS.

## Security

- **Hand-rolled SigV4**: streaming `PUT` and presigned `GET` signing with no AWS SDK dependency.
- **Streaming uploads**: files stream from disk to R2 without buffering whole payloads in memory.
- **Path confinement**: `os.OpenRoot` prevents symlink escapes outside the target directory.
- **Strict permissions**: config is saved `0600` and world-readable configs are refused.
- **No telemetry**: `yz` talks only to Cloudflare endpoints.

See [SECURITY.md](SECURITY.md) for how to report vulnerabilities.

## Environment Overrides (testing & advanced)

- `YZ_CONFIG_DIR`: configuration directory (default `~/.config/yz`)
- `YZ_OAUTH_BASE_URL`: override Cloudflare OAuth endpoints
- `YZ_OAUTH_CLIENT_ID`: override the built-in OAuth client ID (for forks that register their own Cloudflare OAuth client)
- `YZ_API_BASE_URL`: override Cloudflare Client v4 API endpoint
- `YZ_R2_ENDPOINT`: override the R2 S3 endpoint

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup, test workflow, and how pull requests turn into releases. Please follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

[Apache 2.0](LICENSE)
