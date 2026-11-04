# SSHYard

[English](README.md) · 中文

SSHYard 是一款跨平台桌面 SSH / SFTP 客户端，支持 macOS、Windows 和 Linux。基于 [mygo](https://github.com/egoist/mygo) 纯原生 UI 构建，不使用 webview，也不嵌入 HTML；终端是用 Go 编写的 VT/ANSI 引擎，由应用自行绘制。

## 功能

- **终端**：多标签与分屏、真彩色、宽字符、输入法、鼠标上报（tmux、vim、htop）、搜索。
- **文件**：SFTP 面板与终端并排；传输队列支持暂停、续传、重试和文件夹传输；拖放；内置编辑器；文件面板可跟随终端当前目录。
- **AI 助手**：按主机打开的助手面板，可在该主机上执行命令、编辑文件，并提供权限模式（询问、审阅、完全访问）。可在 设置 → AI 助手 中选择内置 OpenAI 兼容后端，或配置自定义 ACP 可执行程序与逐行参数，然后在面板标题栏选择外部 ACP；在“自动审核”模式下，已配置的对话模型、OpenAI Decisions 或 TypeSafe Jev 会审核每个操作，包括只读、未分类和高风险操作，仅模型允许时执行，模型要求确认或审核失败时由你确认；决策模型支持宽松、标准、严格三种阈值，未配置审核模型时继续使用原有权限规则；使用前请在外部程序中完成登录，SSHYard 不安装智能体，也不猜测命令行参数；ACP 文件和终端请求始终绑定当前主机并经过相同权限门，停止或安全配置变化会关闭该面板的智能体进程，不关闭其他 SSH 面板。
- **智能体接入：开放 API、MCP 与 Skill**（默认关闭）：让本机的 AI 智能体列出分组和主机、用已保存的凭证建立连接（凭证绝不外露，需要输入密码的主机直接拒绝）并运行命令。同一组工具既以 HTTP API 形式提供在 `http://127.0.0.1:47320/api/v1`，也以 MCP 形式提供在 `/mcp`，使用保存在本地加密库中的 Bearer 令牌；`/skill` 提供教智能体使用该 API 的 Skill，并可一键注入到检测到的 Claude Code、Codex 和 `~/.agents/skills`（注入的文件包含令牌）。在 设置 → MCP / Skill 中阅读风险说明后开启。
- **共享连接**：同一主机的所有面板共用一条已认证的 SSH 连接，断线后一起重连。
- **主机与凭据**：密码和私钥（OpenSSH、PEM、PuTTY .ppk）保存在本地加密文件 `secrets.enc`，系统钥匙串仅保存随机生成的 32 字节主密钥；支持 SSH Agent、生成密钥和安装公钥。
- **备份**：用密码加密（Argon2id、XChaCha20-Poly1305）的单个文件，包含主机、凭据及其机密、代理、端口转发、偏好设置和已信任的主机密钥，可在其他设备上用目标设备的主密钥重新加密恢复，不包含本机主密钥和设备 MCP 令牌。
- **网络**：多级跳板机、HTTP / SOCKS5 代理、本地 / 远程 / 动态端口转发。
- **便捷功能**：从 `~/.ssh/config` 导入、快速连接（`user@host:port`）、服务器监控栏、浅色与深色主题、英文与简体中文。

启动时会自动向系统钥匙串请求主密钥并解锁本地凭据库，授权被拒绝或失败时可重试，取消后停留在锁定页，生产环境不会退回仅内存存储；不迁移或读取旧版逐项存入钥匙串的数据。

## 开发

需要 Go 1.27.3（可借助 `GOTOOLCHAIN` 自动下载），不需要 cgo。

```sh
# 运行
go run .

# 运行开发版 app；macOS 上优先用 "Apple Development" 证书签名
# 稳定签名可减少钥匙串提示，自签名不能保证跨构建沿用授权
# macOS 仍可能要求重新授权
scripts/run-dev-app.sh

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
