<p align="left"><strong>English</strong> · <a href="README_ZH.md">中文</a></p>

<p align="center">
  <img src="resources/icon.png" alt="SSHYard logo" width="128" height="128">
</p>

<h1 align="center">SSHYard</h1>

SSHYard is a cross-platform desktop SSH / SFTP client for macOS, Windows and Linux. It is built on [mygo](https://github.com/egoist/mygo) with a pure native UI: no webview and no HTML. The terminal is a VT/ANSI engine written in Go and drawn by the app itself.

## Features

- **Terminal**: tabs and split panes, true colour, wide characters, input methods, mouse reporting (tmux, vim, htop), search.
- **Files**: SFTP panes side by side with the terminal; a transfer queue with pause, resume, retry and folder transfers; drag and drop; a built-in editor; the files pane can follow the terminal's working directory.
- **AI assistant**: a per-host assistant pane that can run commands and edit files on that host, with permission modes (Ask, Review, Full access). Choose the built-in backend (OpenAI Chat Completions, OpenAI Responses or Anthropic Messages providers) or a custom ACP executable in Settings → AI Assistant. In Review mode, a configured chat model, OpenAI Decisions or TypeSafe Jev reviews every operation, including read-only, unclassified and high-risk actions; an allow decision permits execution, while an ask decision or review failure requires your confirmation. Decision models offer loose, standard or strict thresholds. Without a configured review model, the existing permission rules still apply. For ACP, configure its executable and one argument per line, then choose External ACP in the pane header. Authenticate in the external program before use; SSHYard does not install agents or guess their command-line options. ACP file and terminal requests use the selected host and the same permission gate, and Stop or a security-setting change closes that pane's agent process without closing other SSH panes.
- **Agent access: open API, MCP and skill** (off by default): lets AI agents on this computer list groups and hosts, connect with the saved credentials (never revealed; hosts that need a typed password are refused) and run commands. The same tools are served as an HTTP API at `http://127.0.0.1:47320/api/v1` and over MCP at `/mcp`, with a bearer token from the local encrypted store; a skill at `/skill` teaches an agent the API, and can be injected into Claude Code, Codex and `~/.agents/skills` where they are found (the injected file holds the token). Turn it on in Settings → MCP / Skill after reading its risks.
- **Shared connections**: every pane on a host shares one authenticated SSH connection, and they all reconnect together after a drop.
- **Hosts and credentials**: passwords and private keys (OpenSSH, PEM, PuTTY .ppk) stored in the local encrypted `secrets.enc` file; only its random 32-byte master key is stored in the system keychain; SSH agent; key generation and public-key install.
- **Backup**: one password-encrypted file (Argon2id, XChaCha20-Poly1305) holding hosts, credentials with their secrets, proxies, port forwards, preferences and trusted host keys, to restore on another device under its own master key; the local master key and device MCP token are excluded.
- **Networking**: multi-hop jump hosts, HTTP / SOCKS5 proxies, local / remote / dynamic port forwards.
- **Convenience**: import from `~/.ssh/config`, quick connect (`user@host:port`), a server metrics bar, light and dark themes, English and Simplified Chinese.

At startup SSHYard automatically requests the master key and unlocks its local store. If access is denied or fails, Retry requests access again and Cancel leaves the app locked; credentials never fall back to memory-only storage. The former per-secret keychain layout is not migrated or read.

## Development

Requires Go 1.27.3 (`GOTOOLCHAIN` can download it). No cgo is needed.

```sh
# run
go run .

# run the development app; on macOS prefer an "Apple Development" identity
# stable signing can reduce keychain prompts; self-signing does not guarantee
# access across rebuilds, and macOS may still request authorization
scripts/run-dev-app.sh

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
