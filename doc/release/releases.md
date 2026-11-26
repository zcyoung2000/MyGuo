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

代理执行提交与发布时使用仓库内的 [MyGuo 发布 skill](../../.agents/skills/myguo-release/SKILL.md)，
其中约定一次性审阅、低频子构建监控、总处理耗时记录与完成标准。
相关执行教训见 [2026-11-26 发布执行复盘](release-execution-retrospective-2026-11-26.md)。

1. 更新 `mygo.json` 的 `version`、两个 README 的下载文件名，并在 `CHANGELOG.md`
   添加对应的 `## x.y.z` 节。该节同时显示在应用内的更新窗口。
2. 先同步并检查 `main`，合入当前分支缺少的提交。运行 `go test -timeout 4m ./...`
   和 `python3 -m unittest discover -s scripts/tests -v`，提交代码并推送 `main`。
3. 从包含最新发布工作流的提交创建对应的 `vx.y.z` 标签并推送。
   `Release` 调度器会检查标签和版本，再构建三个平台，使用同一私钥签名，
   验证所有安装包和更新包，上传文件后发布 Release。
4. 确保新 Release 是最新正式版本，且三个平台的清单、签名更新包、安装包均已
   上传。仅发布某个平台会让其他平台无法检查更新，不应这样发布。

重发旧标签时，**从默认分支触发新的调度器，不要重跑旧的失败记录**：

```sh
gh workflow run release.yml --ref main -f release_tag=v0.1.7 -F publish=true
```

旧运行仍使用其原来的工作流，旧标签中也不会自动出现后续 CI 修复。
调度器和构建脚本使用默认分支上固定 SHA 的发布工具；应用源码始终检出
`release_tag` 在预检时解析到的固定 SHA。标签移动会导致检查失败。
这允许修复旧版构建流程，同时避免把当前 `main` 的应用代码打进旧版本。

### 只验证，不覆盖公开 Release

修改发布工作流、缓存或签名流程后，先做全平台演练：

```sh
gh workflow run release.yml --ref main -f release_tag=v0.1.7 -F publish=false
```

演练仍运行 Go 测试、生成签名安装包及更新包、验证三个平台并生成校验和，
但不创建、上传或修改公开 Release。最终文件保存在构建运行的
`verified-release-v0.1.7` Actions artifact，保留 7 天。
演练还能预热默认分支缓存；需要测量缓存收益时再运行一次，比较同一 runner
和同一源码 SHA 的步骤耗时，不把冷构建与热构建混在一起比较。

只做远端版本和工作流预检，不启动构建：

```sh
python3 scripts/release.py --tag v0.1.7 --preflight-only
```

本地直接运行调度脚本默认也是仅构建；只有显式传入 `--publish` 才公开发布。
正常发布优先从 GitHub 的 `Release` 入口启动，让监控不依赖本机终端保持在线。

## 防阻塞与自动恢复

职责分成两层，配置都保存在仓库中：

- `.github/workflows/release.yml`：在 Ubuntu 24.04 上执行调度与监控。
- `scripts/release.py`：预检、关联唯一构建请求、监控队列、取消和有限切换。
- `.github/workflows/release-build.yml`：版本检查、macOS 测试、并行打包和最终校验。

主 runner 是 `macos-15-intel`，备用为 `macos-26-intel`，均显式指定 Intel。
不用 `macos-latest`，也不会自动退回这次出现容量问题的 ARM 队列。
修改白名单时必须同时修改 Python 调度器与构建工作流，并执行测试。

| 情况 | 自动处理 |
| --- | --- |
| macOS job 连续约 3 分钟未获得 runner | 取消本轮，确认其已停止，再切备用 Intel 镜像 |
| 平台明确报告不支持配置或分配失败，且尚未执行构建步骤 | 切备用镜像；最多两个镜像各一次 |
| 两个镜像都不可用 | 明确失败，转下方本机构建方案；不无限重试 |
| 代码、测试、签名或打包失败 | 直接失败，不通过换机器重复执行同一错误 |
| Linux 队列或整个 Actions 服务不可用 | 明确失败，不误判成 macOS 镜像问题 |
| 调度后的总耗时达到约 30 分钟 | 取消未完成的子运行并报错；调度 job 另有 35 分钟兜底 |
| 取消 45 秒后仍未停止 | 请求强制取消，再等至多 30 秒；停止不了则禁止启动替代发布 |
| 人工取消调度器 | 清理步骤尝试取消关联的子运行；子 job 自身另有执行超时 |

队列按实际 runner 分配和步骤状态判断，**不把 `started_at` 当作已开工证据**。
同一标签的调度和构建分别有并发锁，避免重复点击导致两个任务同时上传。
提交构建请求前记录唯一请求 ID；即使请求响应丢失，也先查找已接收的运行，
不会盲目重复提交。每次镜像选择、失败原因、切换及成功 job 的时间会写入
调度器的 Actions 日志和 Summary。

上述时限从调度器获得 Linux runner 后开始，另有 API 调用、轮询和取消的少量
收尾时间，**不是从点击发布起计算的服务保证**。GitHub 整体故障、调度器自身
未获分配、网络故障或权限被撤销，仍可能需要人工处理；无法承诺托管平台永不排队。
job 的 `timeout-minutes` 约束执行阶段，不能替代这里独立实现的排队监控。

## 构建缓存与耗时检查

所有构建子运行都从默认分支触发，再单独检出应用标签。这使新版本复用默认
分支的缓存，避免每个新标签从头建立孤立缓存。

- **FFmpeg 成品缓存**：按目标架构、构建配方及校验器哈希隔离；macOS 还包括
  runner 镜像、Clang 和 SDK 指纹，Windows 固定 Zig 0.15.2。
  不对成品使用宽泛的恢复前缀。
- **命中也校验**：重新核对源代码 SHA256、FFmpeg 版本、目标、许可证、
  macOS 最低版本、构建配方 SHA256、二进制及许可文件校验和。校验失败重新编译，
  不把缓存命中当作可信结论。Windows 构建脚本也会复用已验证的产物。
- **提前保存 FFmpeg**：辅助程序构建后立即缓存；之后应用打包失败不丢失编译成果。
  GitHub cache key 不可覆盖；同 key 的缓存损坏时会安全重建，但需删除坏缓存才能恢复缓存收益。
- **Go 缓存**：显式缓存模块和编译对象，按宿主、目标/测试用途、依赖文件及
  应用提交区分 key；相同依赖下可以恢复上一提交的增量编译缓存。
  新建 macOS 目标缓存时还能复用刚完成的同宿主测试缓存；Linux 打包与最终校验
  也可互相借用兼容缓存，减少首次建立独立 key 时的重复编译。Go 自身继续按
  编译参数和工具链校验对象，不混用不兼容的编译结果。
- **有界下载**：FFmpeg 下载有连接、单次请求、重试时间限制；缓存下载段也有
  超时，缓存服务故障可以回退至正常编译。
- **不缓存签名发布物**：DMG、EXE、更新包、清单和校验和仍为本轮重新生成并验证。

macOS 测试通过后，macOS arm64、macOS amd64 和 Windows x64 并行构建。
单个测试 job 限 8 分钟、打包 job 限 15 分钟、汇总发布限 5 分钟；另外各关键
构建/下载步骤设有超时。macOS 矩阵一项失败就停止另一项，避免继续消耗资源。

性能基线、故障时间线与维护经验见 [发布耗时复盘](release-retrospective-2026-11-25.md)。

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

两个平台的辅助程序在再次构建前都会验证已有产物；配方改变或文件损坏才会重建。
本地回退也应保留原签名密钥，不为了绕过 CI 失败重新生成密钥。

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

## macOS 图标

`resources/AppIcon.icon` 为 Icon Composer 源文件。CI runner 的 Xcode 可能无法编译它，
而 MyGo 会输出警告后继续用 PNG 图标构建，不能只以构建退出码判断图标正确。
v0.1.9 的 arm64 构建日志出现了这一回退，更新后的应用缺少 `Assets.car`。

使用 Xcode 26+ 的本机生成并一同提交两份编译产物：

```sh
python3 scripts/build-macos-icon.py
```

- `resources/AppIconCatalog/Assets.car`：`macos.icon` 指向的现代图标目录；打包时直接读取，
  不依赖 CI 上的 `actool`。原始目录由 `resourcesExclude` 排除，避免重复复制。
- `resources/darwin/MyGuo.icns`：同一次编译生成的旧系统回退；作为 macOS 平台资源复制，
  `macos.infoPlist.CFBundleIconFile` 显式引用它。Windows 继续使用 `resources/icon.png`。

修改 `.icon` 后重新生成以上文件。`verify-release.go` 在验证签名之后读取 macOS 更新归档，
核对两份图标与仓库产物完全一致，并检查 `Info.plist` 引用；缺失或回退时阻止发布。
不要为修图标手工替换已签名的公开更新包，修复应随下一版本正常发布。

## UI 组件选择

核对 MyGo v0.9.1 的官方组件目录：更新偏好使用 `ui.Switch`，立即执行的动作
复用设置页基于 `ui.ButtonBase` 的按钮；`Row`/`Column` 仅排列说明与控件。
下载进度、发行说明、安装、稍后提醒和跳过版本由 MyGo 原生更新插件提供。
MyGo 已有 v0.10.0，本次所用接口在项目固定的 v0.9.1 中均存在，无需升级框架。
设置内容继续遵循仓库现有的不透明内容卡设计，未改动侧栏的系统磨砂层。
