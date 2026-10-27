# SSHYard

English · [中文](README_ZH.md)

SSHYard is a cross-platform desktop SSH / SFTP client for macOS, Windows and Linux. It is built on [mygo](https://github.com/egoist/mygo) with a pure native UI: no webview and no HTML. The terminal is a VT/ANSI engine written in Go and drawn by the app itself.

## Features

- **Terminal**: tabs and split panes, true colour, wide characters, input methods, mouse reporting (tmux, vim, htop), search.
- **Files**: SFTP panes side by side with the terminal; a transfer queue with pause, resume, retry and folder transfers; drag and drop; a built-in editor; the files pane can follow the terminal's working directory.
- **AI assistant**: a per-host assistant pane that can run commands and edit files on that host, with permission modes (Ask, Review, Full access).
- **Shared connections**: every pane on a host shares one authenticated SSH connection, and they all reconnect together after a drop.
- **Hosts and credentials**: passwords and private keys (OpenSSH, PEM, PuTTY .ppk) stored only in the system keychain; SSH agent; key generation and public-key install.
- **Networking**: multi-hop jump hosts, HTTP / SOCKS5 proxies, local / remote / dynamic port forwards.
- **Convenience**: import from `~/.ssh/config`, quick connect (`user@host:port`), a server metrics bar, light and dark themes, English and Simplified Chinese.

## Development

Requires Go 1.27.3 (`GOTOOLCHAIN` can download it). No cgo is needed.

```sh
# run
go run .

# keep data in a separate directory
SSHYARD_DATA_DIR=/tmp/demo go run .

# a local SSH server for testing (macOS / Linux)
go run ./cmd/devsshd -addr 127.0.0.1:2222 -user demo -password demo -root /tmp/remote

# a local HTTP / SOCKS proxy for testing
go run ./cmd/devproxy -addr 127.0.0.1:3128

# tests and checks
go test -race -count=1 ./...
go vet ./...
gofmt -l .

# regenerate the locale catalog after editing locales/*.json
GOFLAGS=-mod=mod go run github.com/egoist/mygo/cmd/mygo generate

# package into dist/
go tool mygo build -platform linux/amd64,windows/amd64,darwin/arm64,darwin/amd64 -o dist
```

To try it against devsshd, add a host `127.0.0.1`, port `2222`, user `demo`, password `demo`.

Contributor rules are in [AGENTS.md](AGENTS.md).
