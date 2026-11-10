<p align="left"><a href="README.md">English</a> · <a href="README_ZH.md">简体中文</a> · <strong>繁體中文</strong> · <a href="README_KO.md">한국어</a> · <a href="README_JA.md">日本語</a></p>

<p align="center">
  <img src="resources/icon.png" alt="SSHYard 圖示" width="128" height="128">
</p>

<h1 align="center">SSHYard</h1>

SSHYard 是一款原生桌面 SSH / SFTP 用戶端，支援 macOS、Windows 和 Linux，將終端機、檔案管理與 AI 助手集於一體。

## 截圖

<table>
  <tr>
    <td width="50%" align="center">
      <img src="docs/terminal-ai-assistant.webp" alt="終端機與 AI 助手" width="100%"><br>
      終端機與 AI 助手
    </td>
    <td width="50%" align="center">
      <img src="docs/sftp-file-manager.webp" alt="SFTP 檔案管理" width="100%"><br>
      SFTP 檔案管理
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <img src="docs/system-monitor.webp" alt="系統監視" width="100%"><br>
      系統監視
    </td>
    <td width="50%" align="center">
      <img src="docs/ai-assistant-settings.webp" alt="AI 助手設定" width="100%"><br>
      AI 助手設定
    </td>
  </tr>
</table>

## 功能

- **終端機**
  - 多分頁、分割窗格與終端機搜尋，分頁可拖動排序和重新命名
  - 真彩色、寬字元、輸入法，以及 tmux、vim、htop 滑鼠操作
  - 十套內建配色方案並可匯入 `.itermcolors`，支援關鍵字醒目提示、行號與時間戳記，可調整游標、行高和字寬
  - 多種輸出編碼，可依主機設定；⌘ / Ctrl + 點按開啟終端機中的網址
  - 除 SSH 外還支援 Telnet、序列埠和本機終端機（Windows 可選 PowerShell、CMD、Git Bash 與 WSL）
  - 同一主機的多個面板共享連線，斷線自動重新連線

- **工作區**
  - 系統監視、佔用、主機詳情、Docker、tmux、快速指令、歷史指令、廣播指令和工作階段記錄都是可拖曳窗格，可停靠到任意窗格旁或其他分頁
  - tmux 工作階段管理，斷線重新連線後回到原工作階段
  - 檢視 Docker 容器與映像檔的詳情和資源佔用，並直接啟動或停止容器
  - 廣播指令與同步輸入，工作階段記錄可依文字或原始格式記錄
  - 命令面板與可自訂的快速鍵，可記住視窗與分割版面

- **檔案管理**
  - SFTP 與終端機並排，支援跟隨終端機目錄，伺服器沒有 SFTP 時自動改用 SCP
  - 多分頁內建編輯器，附目錄樹
  - 可透過 sudo 提權存取檔案
  - 支援檔案拖放和資料夾傳輸，上傳保留權限和修改時間
  - 傳輸佇列支援暫停、續傳和重試

- **AI 助手**
  - 每臺主機獨立的助手面板，支援執行命令、編輯檔案，SSH、Telnet、序列埠和本機終端機均可使用，對話依主機儲存
  - 接入 AI 服務供應商，或使用自動偵測到的本機 ACP 代理程式（Claude Code、Codex、Grok、Gemini CLI 等），可同時新增多個，並選擇各自支援的模型與思考等級；操作始終在遠端主機上進行
  - 支援貼上圖片和檔案作為附件
  - 詢問、自動審核、完全允許三種權限模式，可設定審核模型、單輪工具呼叫上限和請求逾時

- **代理程式接入**
  - 透過開放 API、MCP 和 Skill 接入本機 AI 代理程式
  - 檢視主機、建立連線和執行命令，無需向代理程式暴露已儲存的憑證
  - 支援為 Claude Code、Codex 一鍵設定 Skill，接入功能預設關閉

- **主機與憑證**
  - 從 SSH 設定匯入，從 Xshell、MobaXterm、PuTTY 等十種用戶端遷移，或貼上文字批次匯入；也可透過 `user@host:port` 快速連線
  - 支援密碼、OpenSSH / PEM / PuTTY 金鑰、OpenSSH 憑證和 SSH Agent
  - 群組不限層級，可拖動整理，支援多選與測速
  - 記錄服務商、續費週期和備註，續費前發送提醒
  - 憑證本地加密儲存，支援產生金鑰和安裝公鑰；主機金鑰變更時核對指紋，並可管理已知主機

- **備份與還原**
  - 密碼加密備份主機、憑證、快速指令和設定，支援跨裝置還原
  - 儲存到本地、WebDAV、S3 相容儲存或 GitHub 私密 Gist
  - 支援手動與定時備份，可設定保留份數

- **網路連線**
  - 多級跳板機與 HTTP / SOCKS5 代理，AI 服務供應商和更新檢查也可使用代理
  - 本地、遠端和動態連接埠轉發

- **日常體驗**
  - 伺服器狀態監視
  - 淺色與深色主題，支援背景圖片、半透明和介面縮放
  - 可關閉到系統匣，從系統匣彈出視窗快速連線主機
  - 支援英文、簡體中文、繁體中文、韓文和日文

## 開發

基於 [mygo](https://github.com/egoist/mygo) 建置，需要 Go 1.27.3（可藉助 `GOTOOLCHAIN` 自動下載），不需要 cgo。

```sh
# 執行
go run .

# 執行開發版 app；macOS 上優先用 "Apple Development" 憑證簽章
# 穩定簽章可減少鑰匙串提示，自簽章不能保證跨建置沿用授權
# macOS 仍可能要求重新授權
scripts/run-dev-app.sh

# 把資料放到單獨的目錄
SSHYARD_DATA_DIR=/tmp/demo go run .

# 本地測試用 SSH 伺服器（macOS / Linux）
go run ./cmd/devsshd -addr 127.0.0.1:2222 -user demo -password demo -root /tmp/remote

# 本地測試用 HTTP / SOCKS 代理
go run ./cmd/devproxy -addr 127.0.0.1:3128

# 測試與檢查
go test -race -count=1 ./...
go vet ./...
gofmt -l .

# 修改 locales/*.json 後重新產生多語言目錄
GOFLAGS=-mod=mod go run github.com/egoist/mygo/cmd/mygo generate

# 打包到 dist/
go tool mygo build -platform linux/amd64,linux/arm64,windows/amd64,windows/arm64,darwin/arm64,darwin/amd64 -o dist
```

連線 devsshd 測試時，新建主機 `127.0.0.1`，連接埠 `2222`，使用者 `demo`，密碼 `demo`。

開發約定見 [AGENTS.md](AGENTS.md)。

## 社群支援

- [LinuxDO](https://linux.do)

## 開放原始碼授權條款

SSHYard 基於 [GNU Affero General Public License v3.0](LICENSE) 開放原始碼。
