# MyGuo

基于 Go 构建的原生 macOS / Windows 短剧客户端。

[English](README.md) | [简体中文](README.zh-CN.md)

## 项目简介

MyGuo 以 MyGo GPU 自绘的原生 UI 作为应用主体：侧栏导航、搜索、目录、详情、选集以及本地历史/收藏/预约直接调用 Go 服务。macOS 使用原生 AVKit，Windows 使用 MyGo `ui/video` 的 Media Foundation 后端；两者共用切集、续播和系统媒体控制。仓库内的 `internal/hgapi` 负责上游协议，播放使用批量模型接口、专用 profile 和 Medusa V5；本地 MediaServer 负责缓存、流式传输和解密，不再依赖外部 OpenHongguo SDK。

所有红果业务接口、账号请求和官网扫码协议统一封装在 `internal/hgapi`；业务层负责缓存、账号归属、同步队列和页面数据组合。分层与扩展方式见 [API 架构](doc/architecture/api-architecture.md)。

开发约定、播放实现、发布维护与历史审查按主题整理在 [项目文档](doc/README.md)。

本项目仅供个人技术研究与本地使用，与上游内容平台没有隶属或认可关系。

## 功能特性

| 分类 | 说明 |
| --- | --- |
| 原生桌面界面 | MyGo `ui` GPU 自绘外壳，包含隐藏式标题栏、侧边栏导航、暗色主题、响应式/虚拟化网格和 SVG 图标。 |
| 内容发现 | 推荐、搜索与搜索建议、筛选、排行榜、新剧日历、演员主页和剧集详情。 |
| 本地资料库 | 历史、收藏、点赞和预约保存在 SQLite；原生主体通过既有服务直接读写这些真实记录。 |
| 评论与弹幕 | 剧集评论、回复、时间轴弹幕播放、发送和点赞。 |
| 播放体验 | 剧集列表、画质选择、进度保存、自动连播和键盘控制。 |
| 媒体管线 | 本地服务代理视频，使用 FFmpeg 解密受保护流，并支持 HTTP Range 请求。 |
| 本地持久化 | SQLite 以 WAL 模式保存登录凭据、设备身份、资料和用户数据，并支持旧数据自动导入。 |
| 无 CGO 构建 | Go 运行时和 SQLite 驱动均可在 `CGO_ENABLED=0` 下构建。 |

## 环境要求

- macOS 13 或更高版本，或 Windows 10/11 x64
- Go 1.27 或更高版本
- macOS 构建内置 FFmpeg 时需要 Xcode 命令行工具、Bash、curl、tar、make、Python 3 和 `shasum`；Windows 打包只需要 Go
- 开发播放需要 FFmpeg；发布包会内置静态 FFmpeg

## 快速开始

```bash
git clone https://github.com/zcyoung2000/MyGuo.git
cd MyGuo
go mod tidy
go run .
```

### 构建应用包

```bash
./scripts/fetch-ffmpeg.sh
CGO_ENABLED=0 go run github.com/egoist/mygo/cmd/mygo build -skip-dmg
```

构建产物位于 Apple Silicon 的 `dist/darwin-arm64/MyGuo.app`，或 Intel 的 `dist/darwin-amd64/MyGuo.app`。

发布 macOS DMG 时，去掉 `-skip-dmg`：

```bash
./scripts/fetch-ffmpeg.sh darwin/arm64
CGO_ENABLED=0 go run github.com/egoist/mygo/cmd/mygo build -platform darwin/arm64
```

MyGo 会生成 `dist/darwin-arm64/myguo-0.1.12-arm64.dmg`，内含 `MyGuo.app` 和 Applications 快捷方式，拖入即可安装。已发布的版本可以从 [Releases](https://github.com/zcyoung2000/MyGuo/releases) 下载。

编译精简应用并直接安装到 `/Applications`：

```bash
./build-install.sh
```

两种 macOS 构建方式默认使用精简 FFmpeg。首次运行校验固定版本的 FFmpeg 9.0.2 源码，用 Apple Clang 编译为兼容 macOS 13 的工具；后续校验并复用工具、许可和构建清单。macOS 无需安装 Zig。安装脚本验证签名，将旧应用备份到 `~/Library/Application Support/myguo-backups`，随后安装并打开应用。如果需要完整版 OSXExperts 工具，显式执行 `./scripts/fetch-ffmpeg.sh --full` 后使用上方手动 MyGo 构建命令；`build-install.sh` 始终准备精简工具。

### 构建 Windows 版本

在 Windows、macOS 或 Linux 的仓库根目录执行：

```sh
go run ./scripts/build-windows.go
```

如果 `resources/windows-amd64` 已有精简 FFmpeg 和构建清单，脚本会校验并复用它；否则下载校验固定版本的 Gyan FFmpeg 9.0.2。打包前需安装 NSIS（macOS 执行 `brew install makensis`，Linux 执行 `sudo apt-get install nsis`）。脚本生成 `dist/windows-amd64/myguo-setup-0.1.12-amd64.exe`，运行 EXE 即可安装，支持开始菜单快捷方式和系统设置中的卸载入口。FFmpeg 和许可说明随程序一起安装，无需额外安装。也可以通过 `-ffmpeg-archive /path/to/ffmpeg-9.0.2-essentials_build.zip` 显式使用完整版 FFmpeg，脚本仍会验证校验值。

在 macOS/Linux 上重新构建 Windows 精简工具，需要 Zig 0.15.2、Bash、curl、tar、shasum、make 和 Python 3：

```sh
MYGUO_ZIG=/path/to/zig ./scripts/build-ffmpeg-minimal.sh
go run ./scripts/build-windows.go -ffmpeg-dir ./tmp_ffmpeg_minimal/output
```

精简配置保留 CENC 解密、MP4/HLS 封装、HTTP/HTTPS、H.264/HEVC/AAC 解码及 JPEG/HEIC 封面处理，移除无关编码器、外部编解码库、滤镜和调试信息。后续打包会校验并复用已安装的工具。构建脚本固定 FFmpeg 源码校验值，并在 `ffmpeg-build-info.json` 中记录目标平台、编译器、许可和文件校验值。

Windows 画中画使用可调整大小的置顶小窗。推荐流切换时显示下一部剧的封面，不截取上一部视频的 GPU 画面。由于 MyGo 未公开 Windows 缓冲区范围，媒体预加载采取保守策略，续播和拖动进度会使用完整的本地 MP4。H.264/AAC 使用系统解码器；HEVC 视频需要 Windows HEVC 编解码器，Windows N 版本需要 Media Feature Pack。真实 Windows 播放和画中画仍需在 Windows 机器上验收。

## 应用更新

从 0.1.6 开始，可以在 **帮助 → 检查更新** 或 **设置 → 软件更新** 中检查
GitHub 上的新版本。默认开启自动检查，自动下载需要用户主动开启；后台更新在
下次启动时生效，不会中断播放。应用安装目录必须可写。0.1.5 及更早版本需要
先手动安装 0.1.6 一次，之后即可在应用内更新。

发布时，每个平台都必须附带 `update-<platform>.json` 和带签名的 `.tar.gz`
更新包。发布工作流使用仓库 Secret `MYGO_UPDATER_PRIVATE_KEY` 签名，缺少文件
或签名校验失败时会停止发布。密钥保存、本机构建及不用 GitHub Actions 的
发布方法见 [发布维护说明](doc/release/releases.md)。

## 推荐流续接

推荐游标、新推荐卡片和近期去重记录会一起保存；重启后恢复未播放队列。只有当前视频实际显示画面后才确认消费，保存失败会重试，正常退出会等待最后的消费确认和观看进度写入。近期推荐按频道保留 200 个剧 ID，游标或匿名会话重置不会清空这些记录；待播卡片也会参与去重。一页全是近期重复内容时继续尝试获取新内容，不会把整页重复视频作为兜底重新展示。超出近期窗口的旧剧仍可能再次推荐。

## 账号登录

MyGuo 可以使用匿名设备配置运行。如需同步个人数据：

1. 打开 **设置**。
2. 选择 **立即登录**。
3. 使用红果手机 App 扫描二维码并确认登录。
4. 新登录校验并保存成功后才会切换账号；此前保留当前账号。

如果本机存在兼容的旧安装登录凭据，应用也会提供一键导入。Cookie 和 Token 应按密码同等对待：请勿提交、分享或泄露。

## 数据与存储

SQLite 保存登录凭据、设备身份、个人资料、历史、收藏、点赞和预约；应用自身不再创建 `account.json`。已缓存媒体和转换后的图片单独存放，可以在设置中清理。

| 平台 | 应用数据 | 缓存 |
| --- | --- | --- |
| macOS | `~/Library/Application Support/myguo/` | `~/Library/Caches/myguo/` |
| Windows | `%LOCALAPPDATA%\myguo\` | `%LOCALAPPDATA%\myguo\Cache\` |

## 架构

- `internal/uiapp` 和 `internal/ipc` 将 MyGo 原生 UI 连接到应用服务。
- `internal/catalog`、`internal/library` 和 `internal/playback` 负责缓存、SQLite 状态、同步与播放控制。
- `internal/hgapi` 封装上游接口、请求与响应模型、签名、账号会话和官网扫码授权。
- `internal/media` 负责 CDN 下载、缓存、流式传输与 FFmpeg 解密；`internal/playerengine` 选择平台播放器，其中 `internal/avkit` 提供 macOS 原生播放。

请求边界、会话策略和新增接口的流程见 [API 架构](doc/architecture/api-architecture.md)。

## 开发

```bash
go mod tidy
go test ./...
CGO_ENABLED=0 go build -o /dev/null .
go test -race ./internal/uiapp
```

修改界面时，保持列表页使用响应式卡片网格；图标使用内联 SVG，不要使用 Emoji 或位图符号；保留标题栏和侧边栏的原生窗口拖拽行为。

## 许可

本项目仅供个人技术研究与本地使用。短剧内容和元数据版权归原权利人所有。

重新分发前请阅读 [第三方组件与许可](doc/legal/THIRD_PARTY.md)。FFmpeg 需要遵守所选构建的许可：macOS 和 Windows 精简工具使用 LGPL，完整版编解码构建使用 GPL。
