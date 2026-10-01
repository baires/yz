# README recordings

These GIF and MP4 demos run the real `yz` binary against local Cloudflare API and R2 fixtures, recorded with [VHS](https://github.com/charmbracelet/vhs). All accounts, buckets, credentials, and URLs are fictional. `files.example.com` is an illustrative URL, not a live share.

Setup starts with a fictional authenticated session and covers bucket selection, masked credential entry, validation, and completion. Browser OAuth is not recorded. No recording reads or changes your real `~/.config/yz` configuration or uploads to Cloudflare.

## Record again

Install VHS, ttyd, ffmpeg, and Python 3. From the repository root:

```sh
go build -o bin/yz ./cmd/yz
python3 docs/demos/server.py
```

Leave the fixture server running on loopback port 8766. In another terminal, from the repository root:

```sh
vhs docs/demos/setup.tape
vhs docs/demos/upload.tape
vhs docs/demos/help.tape
```

Stop the server with Ctrl+C afterward. Each tape initializes its own ignored fixture configuration. The upload fixture is `hello.txt`; objects stay in the server's memory. The fixtures are for presentation, not authentication or signing tests. The existing E2E fixtures test those behaviors.

The videos use Menlo and Catppuccin Mocha.
