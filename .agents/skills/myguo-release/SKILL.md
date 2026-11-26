---
name: myguo-release
description: 为本仓库 MyGuo 提交待发布改动、准备版本并通过 GitHub Release 调度器发布 macOS 与 Windows 版本；也用于重发、发布演练及发布耗时排查。用户要求提交并发布、发布新版本或排查发布过程时使用。
---

# MyGuo 发布

在仓库根目录执行命令。先读一次 [开发约定](../../../doc/development/README.md) 和
[发布手册](../../../doc/release/releases.md)，本轮已经读过且文件未变则复用。
仓库为 `zcyoung2000/MyGuo`。签名、缓存和恢复细节以发布手册及当前工作流为准。
排查耗时时读 [2026-11-26 执行复盘](../../../doc/release/release-execution-retrospective-2026-11-26.md)；
遇到 runner 或 CI 故障再参考 [2026-11-25 复盘](../../../doc/release/release-retrospective-2026-11-25.md)。

## 范围与计时

- 用户要求“提交并发布”已授权对应的提交、推送、标签和公开 Release；准备完直接执行。
  仅要求编写文档、创建 skill、排查或演练时，不推断为公开发布授权。
- 沿用当前工作区。使用 super.engineering 时，按其 worktree 指南通过
  `se worktree status --json` 读取目标分支；不要为发布自行创建分支或工作区。
- 从开始处理任务时记录带时区的时间，保留五个里程碑：开始处理、规定检查完成、
  标签推送、Release 公开、最终汇报。可放临时文件，无需为每次发布增加仓库文件。
  指令到达时间不可得时明确说“开始处理”，不要伪造总对话耗时。
- 将本地准备、远端排队、远端执行、公开后汇报分别记录。用户关心对话总耗时，
  不能仅用 Actions 耗时作答；未知区间标为未知，不把差额归咎于 GitHub。

## 一次完成准备

1. 集中获取工作区状态、diff 统计、最近提交、远端默认分支、最新正式 Release。
   `git fetch origin --tags` 后检查与远端 `main` 的差异；有缺失提交先按开发约定合入。
   对无关改动保留原状；确实无法确定提交范围时再询问。
2. 按文件或模块读取待发布 diff，各读一次。大 diff 分批读取，避免整份输出被截断后
   再重复全量读取。首次范围检查通过后，仅追查疑点和新增修改，不扩展成无关重构或审计。
3. 用户指定版本则采用指定版本；未指定时，根据当前正式版及改动性质选择下一个未使用版本，
   在进度中说明。检查标签是否占用，已存在的发布标签保持不变。
4. 更新 `mygo.json`、`README.md`、`README.zh-CN.md` 的版本与下载文件名，
   在 `CHANGELOG.md` 添加 `## x.y.z`。发行说明以实际改动为依据。
5. 独立检查可并行运行：

   ```sh
   go test -timeout 4m ./...
   python3 -m unittest discover -s scripts/tests -v
   git diff --check
   ```

   检查变动 Go 文件的 gofmt 状态。通过后不重复运行相同检查，除非后续改动、
   失败或新证据影响其有效性。正常发版保留这些规定检查；修改 CI、缓存或签名流程后，
   还需按手册先完成 `publish=false` 的三平台演练。
6. 检查最终提交范围，提交并推送 `main`。记录 commit SHA，再从该提交创建并推送
   匹配版本的 `vx.y.z` 标签。推送失败先解决原因，不继续假定远端已同步。

## 只触发一次，跟踪实际构建

新标签 push 自动触发 `release.yml`，不要再手动 dispatch 一份。
重发旧标签从默认分支启动新调度器，不重跑旧失败记录：

```sh
gh workflow run release.yml --repo zcyoung2000/MyGuo --ref main -f release_tag="$tag" -F publish=true
```

上例的 `tag` 应先设为已核实的目标版本。演练使用 `publish=false`。
保留调度器的队列预算、最多一次备用 runner 切换、取消清理和全平台验证。
代码、测试、签名或打包错误先定位，不通过换 runner 重复碰运气。

1. 保存本次 `release.yml` 的 run ID、URL、触发时间和 SHA。按标签、触发时间、
   source/tooling SHA 及调度请求 ID 关联 `release-build.yml` 子运行；
   不把列表第一项直接当成本轮任务，备用运行也需保持同一请求链路。
2. 使用一种监控方式：默认每隔约 30–60 秒读取紧凑状态，并给用户简短进度；
   不反复启停 `gh run watch`，不同时 watch 调度器和轮询同一状态。
   每次只输出 job 名、状态、结论及当前步骤，完整 JSON 留给诊断。

   ```sh
   gh run view "$build_run" --repo zcyoung2000/MyGuo --json status,conclusion,jobs,url \
     --jq '{status,conclusion,url,jobs:[.jobs[]|{name,status,conclusion,current:[.steps[]|select(.status=="in_progress")|.name]}]}'
   ```

3. 顶层运行的 `queued` 和 `startedAt` 都不足以判断 runner 是否开工。
   依据子运行的 job 步骤与实际 runner/日志证据报告“验证中”“打包中”或“待分配”；
   无法确定时说状态尚不明确，不先讲排队故障。时间戳矛盾时保留矛盾，不编造时长。
4. 构建完成前不反复查询 Release 是否存在；最终发布 job 成功后再查。
   诊断失败时查失败 job 的已完成日志与 annotations；`gh run view --log` 提示
   运行未结束时，不反复执行同一命令。
5. 遇到失败先分类并采取对应恢复。若两个批准镜像均不可用，或取消无法确认，
   按手册停止替代任务并报告；本机回退也必须保留三平台、原签名密钥与完整校验。

## 完成条件与最终汇报

子构建的三平台 job、汇总校验/发布 job 及调度器均成功后，集中检查一次：

```sh
gh release view "$tag" --repo zcyoung2000/MyGuo --json tagName,isDraft,isPrerelease,publishedAt,assets,url
gh release list --repo zcyoung2000/MyGuo --limit 3
git status --short --branch
```

- Release 是目标标签、非草稿、非预发布，列表显示为 Latest。
- macOS arm64、macOS amd64、Windows x64 均有安装包、签名更新包、更新清单，
  并有 `SHA256SUMS`；附件为 uploaded。差分包随当前流程提供，不硬编码附件总数量。
- 最终 Git 状态与已报告的提交/推送一致；若保留了无关本地改动就如实说明。
- 不必为“再确认”重新下载全部附件；CI 已验证且没有新疑点时，以上证据足以完成。

汇报包含版本与 Release 链接、commit、验证结论，以及本轮总处理时长和
GitHub 触发到公开的时长。有异常则说明已知耗时及未知区间，不能把并行 job 时长相加。
成功证据齐备后立即汇报，不继续轮询。

## 避免已发生的无效往返

- 搜索先用 `rg --files` 确认可用路径，或对确定路径使用 `rg`；不要让 zsh 对不存在的
  `Makefile*` 等通配符报错后再重试。
- 使用已知支持的 `gh --json` 字段；`gh release view` 不支持 `isLatest`，
  Latest 用 release 列表确认。遇到字段错误按 CLI 返回的支持列表修正一次。
- 进度更新交代新事实、风险或下一完成节点；等待期间简短说明状态，避免堆叠整屏重复输出。
