# AGENTS.md

本仓库是一个原生 macOS 短剧浏览客户端。开始开发前，先检查 `main` 是否包含当前分支缺少的提交；如果有，先合并 `main`。

发布和修改 CI 前阅读 `docs/releases.md`。统一从默认分支的 `Release` 调度器启动，
重发旧版本不要重跑旧的失败工作流。runner 配置/排队错误最多切换一次，代码和签名
错误不盲目重试；保留全平台验证。修改发布流程后先运行调度器测试，再做
`publish=false` 演练；排队与执行耗时分别报告。历史经验见
`docs/release-retrospective-2026-11-25.md`。
