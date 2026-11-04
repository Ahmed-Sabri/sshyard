# 正式发布签名配置

在 GitHub 仓库 Settings → Secrets and variables → Actions → Repository secrets 中配置以下六项；证书和 Apple API 私钥只存入 Secrets，不提交到仓库

| Secret | 内容 |
| --- | --- |
| `MYGO_UPDATER_PRIVATE_KEY` | `/Users/licoy/Documents/mac-cert/sshyard/mygo-update.key` 的完整内容，与 `mygo.json` 的 `updates.publicKey` 配对；同目录 `.pub` 是公钥 |
| `MACOS_CERTIFICATE_P12_BASE64` | Developer ID Application 证书及其私钥导出的 `.p12` 文件，整体 Base64 编码 |
| `MACOS_CERTIFICATE_PASSWORD` | 导出 `.p12` 时设置的密码，这是证书导出密码，与 Mac 登录密码无关 |
| `APPLE_API_KEY_P8` | `/Users/licoy/Documents/mac-cert/AuthKey_2N46T35DQ8.p8` 的完整文本，包括 BEGIN/END 行与换行 |
| `APPLE_API_KEY_ID` | `2N46T35DQ8` |
| `APPLE_API_ISSUER_ID` | 对应 App Store Connect 团队 API 密钥的 Issuer ID，UUID 格式，不是 Team ID `395QP35ZGC` |

在本机执行以下命令可将证书 Base64 直接复制到剪贴板，然后粘贴到 `MACOS_CERTIFICATE_P12_BASE64`

```sh
base64 -i /Users/licoy/Documents/mac-cert/mac-sing.p12 | pbcopy
```

复制 Apple API 私钥原文到剪贴板，然后粘贴到 `APPLE_API_KEY_P8`

```sh
pbcopy < /Users/licoy/Documents/mac-cert/AuthKey_2N46T35DQ8.p8
```

Issuer ID 从 [App Store Connect](https://appstoreconnect.apple.com/access/integrations/api) → Users and Access → Integrations → App Store Connect API → Team Keys 页面获取；这里使用团队 API 密钥，该密钥必须有提交公证的权限；个人 API 密钥不适用于此工作流

## 工作流行为

1. `release.yml` 在创建草稿前检查六项 Secrets 是否非空，已有正式发布会拒绝覆盖
2. macOS runner 导入证书到临时钥匙串，自动选择其中唯一有效的 Developer ID Application 签名身份；找不到或存在多个身份时直接失败，无需单独配置签名身份；然后设置无人值守签名权限，并通过 `notarytool store-credentials` 校验、保存 Apple API 凭据
3. 仅在 runner 工作目录中改写 `mygo.json` 的 `macos.signingIdentity` 和 `macos.notarize`；仓库配置与 beta CI 不变
4. `go tool mygo build` 签署 App、DMG，向 Apple 提交公证并等待通过，再给 DMG 和用于更新的 App 装订公证票据
5. 上传前验证更新清单有签名，并在 macOS 上执行 `codesign`、`stapler validate` 和 Gatekeeper `spctl` 验收；安装包、更新归档、delta 与附加 ZIP 先上传，更新清单最后上传
6. macOS job 无论成功或失败都执行凭据清理；签名或公证失败时停止该平台上传，不生成未签名替代包

所有平台构建完成后，再人工检查并发布草稿；构建过程中不要手动发布草稿；配置缺失、凭据无效或 Apple 服务失败可能留下空白或部分资产的草稿，修复后可重新运行工作流

版本只修改 `mygo.json`；先推送版本提交，再由维护者创建对应 `v<version>` 标签；`v<version>-beta-<yyyymmddhhmmss>` 继续只运行 `ci.yml`，不会触发正式签名发布

无凭据的失败路径检查可本地运行 `bash .github/macos-release-test.sh`，覆盖缺少 Secrets、签名身份解析与零个或多个身份的拒绝、钥匙串删除失败仍清理文件并保留失败状态

本地静态检查无法代替真实 Apple 签名、公证和 Gatekeeper 验收，首次完整验收需要配置上述 Secrets 后运行正式发布工作流
