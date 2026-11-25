# 发布与自动更新

应用直接读取 `zcyoung2000/MyGuo` 最新正式 Release 的更新清单，不需要自建服务。
默认不使用 GitHub API 获取版本列表，也不需要在客户端放 GitHub Token。

## 签名密钥

`mygo.json` 中的 `updates.publicKey` 是客户端信任的 Ed25519 公钥。
对应的私钥存放在发布机器的仓库外目录：

```text
~/Library/Application Support/myguo-release/update-keys/mygo-update.key
```

该目录权限为 0700，私钥为 0600；公钥文件在同目录的 `mygo-update.pub`。
GitHub 仓库也已配置同一私钥的 Secret `MYGO_UPDATER_PRIVATE_KEY`，用于 CI。
应将原私钥另行备份到密码管理器；GitHub Secret 不能读回。
不要重新生成密钥覆盖原密钥，已安装客户端只信任它们内置的公钥。
更新签名与 Apple Developer ID、公证和 Windows Authenticode 是独立的。

## 发布步骤

1. 更新 `mygo.json` 的 `version`、两个 README 的下载文件名，并在 `CHANGELOG.md`
   添加对应的 `## x.y.z` 节。该节同时显示在应用内的更新窗口。
2. 运行 `go test ./...`，提交代码并推送 `main`。
3. 创建对应的 `vx.y.z` 标签并推送。GitHub Actions 会构建三个平台，使用同一
   私钥签名，验证所有安装包和更新包，上传文件后发布 Release。
4. 确保新 Release 是最新正式版本，且三个平台的清单、签名更新包、安装包均已
   上传。仅发布某个平台会让其他平台无法检查更新，不应这样发布。

工作流的手动运行必须指定已存在的 `release_tag`，所有 job 都检出该标签，
避免用当前 `main` 错误地构建旧版标签。

## 本机构建与发布

GitHub Actions 不可用时，在 macOS 上运行：

```sh
python3 scripts/build-release.py
```

脚本默认读取上述目录内的私钥，也支持 `--key-file /path/to/mygo-update.key`
或环境变量 `MYGO_UPDATER_PRIVATE_KEY`。私钥不会复制到安装包或输出目录。
macOS 精简 FFmpeg 会按需编译；Windows 安装器需要先安装 NSIS：`brew install makensis`。Windows 精简 FFmpeg 应提前通过
`scripts/build-ffmpeg-minimal.sh windows/amd64` 生成并放入 `resources/windows-amd64`
（这一步需要 Zig 0.15.2）。

脚本生成三个平台的安装包、签名更新包、更新清单和 `SHA256SUMS`，保存到
`dist/release-vx.y.z/`。签名不匹配、缺少平台会导致验证失败。
也可对下载的 Release 附件单独运行：

```sh
go run ./scripts/verify-release.go -dir /path/to/release-assets
```

确认版本标签已推送，再创建草稿并上传目录中的全部文件。上传完成后发布草稿，
并设为最新正式 Release。更新已有的公开 Release 时，先上传压缩包，最后上传
更新清单，避免客户端看到清单时对应文件还不存在。

0.1.6 是第一版内置更新器的版本。0.1.5 及以前的用户需要手动下载安装一次；
不能仅给旧 Release 补上传清单来为已安装的旧客户端增加更新功能。

## UI 组件选择

核对 MyGo v0.9.1 的官方组件目录：更新偏好使用 `ui.Switch`，立即执行的动作
复用设置页基于 `ui.ButtonBase` 的按钮；`Row`/`Column` 仅排列说明与控件。
下载进度、发行说明、安装、稍后提醒和跳过版本由 MyGo 原生更新插件提供。
MyGo 已有 v0.10.0，本次所用接口在项目固定的 v0.9.1 中均存在，无需升级框架。
设置内容继续遵循仓库现有的不透明内容卡设计，未改动侧栏的系统磨砂层。
