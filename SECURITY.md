# Security Policy

## Reporting a Vulnerability

Please do **not** open a public GitHub issue for security vulnerabilities.

Report vulnerabilities privately through
[GitHub's private vulnerability reporting](https://github.com/baires/yz/security/advisories/new)
on this repository. If that is unavailable, email alexis@sgarbossa.com.ar.

You can expect an acknowledgement within a few days. Please include the version
(`yz version`), your OS and architecture, and steps to reproduce the issue.

## Scope Notes

- `yz` stores your Cloudflare R2 credentials in `~/.config/yz/config.json`
  with `0600` permissions and refuses to load world-readable configs.
- The R2 API token you provide needs only **Object Read & Write** on your
  bucket. Use a scoped token, never your Cloudflare global API key.
- `yz` sends no telemetry. It talks only to Cloudflare endpoints (OAuth, the
  Client v4 API, and your R2 S3 endpoint).
- Public shares are public: anyone with the link can download the file until
  you delete it. Use `--expires` or `--signed` for sensitive files.
