# SSHYard

[English](README.md) · 中文

SSHYard 是一款跨平台桌面 SSH / SFTP 客户端，支持 macOS、Windows 和 Linux。基于 [mygo](https://github.com/egoist/mygo) 纯原生 UI 构建，不使用 webview，也不嵌入 HTML；终端是用 Go 编写的 VT/ANSI 引擎，由应用自行绘制。

## 功能

- **终端**：多标签与分屏、真彩色、宽字符、输入法、鼠标上报（tmux、vim、htop）、搜索。
- **文件**：SFTP 面板与终端并排；传输队列支持暂停、续传、重试和文件夹传输；拖放；内置编辑器；文件面板可跟随终端当前目录。
- **AI 助手**：按主机打开的助手面板，可在该主机上执行命令、编辑文件，并提供权限模式（询问、审阅、完全访问）。
- **共享连接**：同一主机的所有面板共用一条已认证的 SSH 连接，断线后一起重连。
- **主机与凭据**：密码和私钥（OpenSSH、PEM、PuTTY .ppk）只存系统钥匙串；支持 SSH Agent、生成密钥和安装公钥。
- **备份**：用密码加密（Argon2id、XChaCha20-Poly1305）的单个文件，包含主机、凭据及其机密、代理、端口转发、偏好设置和已信任的主机密钥，可在其他设备上恢复。
- **网络**：多级跳板机、HTTP / SOCKS5 代理、本地 / 远程 / 动态端口转发。
- **便捷功能**：从 `~/.ssh/config` 导入、快速连接（`user@host:port`）、服务器监控栏、浅色与深色主题、英文与简体中文。

## 开发

需要 Go 1.27.3（可借助 `GOTOOLCHAIN` 自动下载），不需要 cgo。

```sh
# 运行
go run .

# 把数据放到单独的目录
SSHYARD_DATA_DIR=/tmp/demo go run .

# 本地测试用 SSH 服务器（macOS / Linux）
go run ./cmd/devsshd -addr 127.0.0.1:2222 -user demo -password demo -root /tmp/remote

# 本地测试用 HTTP / SOCKS 代理
go run ./cmd/devproxy -addr 127.0.0.1:3128

# 测试与检查
go test -race -count=1 ./...
go vet ./...
gofmt -l .

# 修改 locales/*.json 后重新生成多语言目录
GOFLAGS=-mod=mod go run github.com/egoist/mygo/cmd/mygo generate

# 打包到 dist/
go tool mygo build -platform linux/amd64,windows/amd64,darwin/arm64,darwin/amd64 -o dist
```

连接 devsshd 测试时，新建主机 `127.0.0.1`，端口 `2222`，用户 `demo`，密码 `demo`。

开发约定见 [AGENTS.md](AGENTS.md)。
