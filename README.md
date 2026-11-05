<p align="left"><strong>English</strong> · <a href="README_ZH.md">简体中文</a> · <a href="README_ZH_TW.md">繁體中文</a> · <a href="README_KO.md">한국어</a> · <a href="README_JA.md">日本語</a></p>

<p align="center">
  <img src="resources/icon.png" alt="SSHYard logo" width="128" height="128">
</p>

<h1 align="center">SSHYard</h1>

SSHYard is a native desktop SSH / SFTP client for macOS, Windows and Linux, bringing terminals, file management and AI assistance into one app.

## Screenshots

<table>
  <tr>
    <td width="50%" align="center">
      <img src="docs/terminal-ai-assistant.webp" alt="Terminal and AI assistant" width="100%"><br>
      Terminal and AI assistant
    </td>
    <td width="50%" align="center">
      <img src="docs/sftp-file-manager.webp" alt="SFTP file manager" width="100%"><br>
      SFTP file manager
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <img src="docs/system-monitor.webp" alt="System monitor" width="100%"><br>
      System monitor
    </td>
    <td width="50%" align="center">
      <img src="docs/ai-assistant-settings.webp" alt="AI assistant settings" width="100%"><br>
      AI assistant settings
    </td>
  </tr>
</table>

## Features

- **Terminal**
  - Tabs, split panes and terminal search
  - True colour, wide characters, input methods and mouse support for tmux, vim and htop
  - Shared connections across panes on the same host, with automatic reconnection

- **File management**
  - SFTP alongside the terminal, with directory following and a built-in editor
  - Drag-and-drop support and folder transfers
  - Transfer queue with pause, resume and retry

- **AI assistant**
  - Run commands and edit files through a dedicated assistant for each host
  - Connect AI providers or use an external ACP agent
  - Choose Ask, Review or Full access mode, with configurable AI review

- **Agent integrations**
  - Connect local AI agents through the open API, MCP and Skill
  - Browse hosts, establish connections and run commands without exposing saved credentials
  - One-click Skill setup for Claude Code and Codex; agent access is off by default

- **Hosts and credentials**
  - Import from SSH config or connect quickly with `user@host:port`
  - Passwords, OpenSSH / PEM / PuTTY keys and SSH agent support
  - Local encrypted credential storage, key generation and public-key installation

- **Backup and restore**
  - Password-encrypted backups of hosts, credentials, snippets and settings, with restore on another device
  - Save locally or to WebDAV, S3-compatible storage and private GitHub Gists
  - Manual or scheduled backups, with configurable retention

- **Networking**
  - Multi-hop jump hosts and HTTP / SOCKS5 proxies
  - Local, remote and dynamic port forwarding

- **Everyday use**
  - Server metrics at a glance
  - Light and dark themes, with English, Simplified Chinese, Traditional Chinese, Korean and Japanese

## Development

Built with [mygo](https://github.com/egoist/mygo). Requires Go 1.27.3 (`GOTOOLCHAIN` can download it). No cgo is needed.

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
go tool mygo build -platform linux/amd64,linux/arm64,windows/amd64,windows/arm64,darwin/arm64,darwin/amd64 -o dist
```

To try it against devsshd, add a host `127.0.0.1`, port `2222`, user `demo`, password `demo`.

Contributor rules are in [AGENTS.md](AGENTS.md).

## Community Support

- [LinuxDO](https://linux.do)

## License

SSHYard is licensed under the [GNU Affero General Public License v3.0](LICENSE).
