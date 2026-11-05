# 正式发布签名配置

在 GitHub 仓库 Settings → Secrets and variables → Actions → Repository secrets 中配置以下六项；证书和 Apple API 私钥只存入 Secrets，不提交到仓库

| Secret | 内容 |
| --- | --- |
| `MYGO_UPDATER_PRIVATE_KEY` | `/Users/licoy/Documents/mac-cert/sshyard/mygo-update.key` 的完整内容，与 `mygo.json` 的 `updates.publicKey` 配对；同目录 `.pub` 是公钥 |
| `MACOS_CERTIFICATE_P12_BASE64` | Developer ID Application 证书及其私钥导出的 `.p12` 文件，整体 Base64 编码 |
| `MACOS_CERTIFICATE_PASSWORD` | 导出 `.p12` 时设置的密码，这是证书导出密码，与 Mac 登录密码无关 |
| `APPLE_API_KEY_P8` | `/Users/licoy/Documents/mac-cert/AuthKey_2N46T35DQ8.p8` 文件整体 Base64 编码，包含 BEGIN/END 行与换行；Secret 名称保持不变 |
| `APPLE_API_KEY_ID` | `2N46T35DQ8` |
| `APPLE_API_ISSUER_ID` | 对应 App Store Connect 团队 API 密钥的 Issuer ID，UUID 格式，不是 Team ID `395QP35ZGC` |

在本机执行以下命令可将证书 Base64 直接复制到剪贴板，然后粘贴到 `MACOS_CERTIFICATE_P12_BASE64`

```sh
base64 -i /Users/licoy/Documents/mac-cert/mac-sing.p12 | pbcopy
```

将 Apple API 私钥文件整体 Base64 编码并复制到剪贴板，然后粘贴到 `APPLE_API_KEY_P8`

```sh
base64 -i /Users/licoy/Documents/mac-cert/AuthKey_2N46T35DQ8.p8 | pbcopy
```

Issuer ID 从 [App Store Connect](https://appstoreconnect.apple.com/access/integrations/api) → Users and Access → Integrations → App Store Connect API → Team Keys 页面获取；这里使用团队 API 密钥，该密钥必须有提交公证的权限；个人 API 密钥不适用于此工作流

## 工作流行为

1. `release.yml` 先检查标签与 `mygo.json` 版本一致、六项 Secrets 非空，已有正式发布会拒绝覆盖；同时按 `cliff.toml` 用 git-cliff 生成上一个正式标签以来的发布说明，并等待同一标签上的 `ci.yml` 通过；`bump.sh` 的版本号提交之前的提交已在 main 上通过完整 CI 时（仍在运行则等它结束），标签上的 `ci.yml` 直接复用该结果，不再重复测试和构建
2. macOS runner 先解码 Base64 格式的证书和 Apple API 私钥，再导入证书到临时钥匙串，自动选择其中唯一有效的 Developer ID Application 签名身份；解码失败、找不到或存在多个身份时直接失败，无需单独配置签名身份；然后设置无人值守签名权限，并通过 `notarytool store-credentials` 校验、保存 Apple API 凭据
3. 仅在 runner 工作目录中改写 `mygo.json` 的 `macos.signingIdentity` 和 `macos.notarize`；仓库配置与 beta CI 不变
4. `go tool mygo build` 签署 App、DMG，向 Apple 提交公证并等待通过，再给 DMG 和用于更新的 App 装订公证票据
5. 构建时把发布说明写入更新清单，供应用内更新窗口显示；验证更新清单有签名且带说明，并在 macOS 上执行 `codesign`、`stapler validate` 和 Gatekeeper `spctl` 验收
6. macOS job 无论成功或失败都执行凭据清理；签名或公证失败时该平台没有产物，不生成未签名替代包
7. 所有平台构建成功且 CI 通过后，汇总全部安装包、更新归档、delta 与更新清单，生成 `SHA256SUMS`，在发布说明后追加各平台下载链接，先建草稿上传全部资产，再自动发布为 latest

Windows 与 Linux 都在 GitHub 的 arm 运行器（`windows-11-arm`、`ubuntu-24.04-arm`）上原生构建 arm64 产物，与 amd64 一起进入 CI 产物和正式 Release；Release 的 `install.sh` 只取 linux/amd64 那份，脚本运行时按 `uname -m` 选择对应架构的压缩包

Windows 的普通 CI、beta 与正式 Release 都额外按架构提供 `sshyard-<版本>-windows-<amd64|arm64>-portable.zip`；ZIP 直接归档 MyGo 构建的程序及完整资源，排除安装器、安装脚本和更新产物；正式 Release 将其纳入 `SHA256SUMS` 并提供下载链接，解压到可写目录后运行 `SSHYard.exe` 即可，无需安装；应用数据仍保存在 `%APPDATA%\SSHYard`，已保存凭据仍绑定当前 Windows 用户，不代表数据可随 ZIP 跨设备迁移

本地可运行 `python3 .github/windows-portable-test.py` 验证 ZIP 内容、无签名 beta 产物及下载链接，Windows CI 在归档前也会执行这些检查

任一平台构建或 CI 失败时不会创建 Release；修复或重跑 CI 的失败 job 后，重跑本工作流的失败 job 即可；之前运行留下的草稿会被替换，已发布的版本不会被覆盖

发布用 `./bump.sh -v <x.y.z> -p`：只修改 `mygo.json` 的版本，提交 `chore(release): bump version to <x.y.z>`（不进入发布说明），创建 `v<x.y.z>` 标签并推送分支与标签；不加 `-p` 只在本地提交和打标签；`v<version>-beta-<yyyymmddhhmmss>` 继续只运行 `ci.yml`，不会触发正式签名发布

无凭据的失败路径检查可本地运行 `bash .github/macos-release-test.sh`，覆盖缺少 Secrets、签名身份解析与零个或多个身份的拒绝、钥匙串删除失败仍清理文件并保留失败状态

本地静态检查无法代替真实 Apple 签名、公证和 Gatekeeper 验收，首次完整验收需要配置上述 Secrets 后运行正式发布工作流
