# 项目文档

文档按主题组织。首次参与开发先读 [开发与协作约定](development/README.md)，
发布版本先读 [发布与自动更新](release/releases.md)。带日期的记录反映当时的
实现、验证结果和限制，不能直接当作当前状态。当前原生功能与验证入口见
[原生功能对齐情况](architecture/native-feature-parity.md)。

## 开发与协作 `development/`

- [开发、验证和文档维护约定](development/README.md)
- [avkit 原生模块约定](development/native-module-avkit.md)
- [退出、更新与重启协调](development/application-shutdown.md)

## 架构与原生迁移 `architecture/`

- [上游 API 分层与扩展](architecture/api-architecture.md)
- [原生功能对齐情况](architecture/native-feature-parity.md)

## 账号与历史同步 `accounts/`

- [观看历史与双向同步](accounts/history-sync.md)
- [账号身份规范化与历史恢复](accounts/history-identity-recovery-2026-11-15.md)
- [扫码登录](accounts/qr-login-2026-11-15.md)

## 播放、预加载与恢复 `playback/`

- [原生播放器实现和验证进展](playback/native-player-progress.md)
- [推荐流和剧集预加载](playback/preloading-2026-11-14.md)
- [持续缓冲与进度保存锁冲突修复](playback/playback-buffering-fix-2026-11-15.md)
- [播放页点击交互](playback/player-interactions-2026-11-14.md)
- [播放器控件与点击行为](playback/player-controls-2026-11-15.md)
- [无分集作品与播放首屏修复](playback/player-startup-2026-11-16.md)

## 界面与交互 `ui/`

- [浏览界面与动效检查](ui/ui-polish-2026-11-14.md)
- [找剧页与筛选条件](ui/catalog-filters-2026-11-14.md)
- [剧集详情页与关联内容](ui/detail-redesign-2026-11-14.md)
- [静默分页与滚动稳定性](ui/quiet-pagination-2026-11-15.md)

## 发布、构建与更新 `release/`

- [发布手册、签名密钥、队列监控、构建缓存和本机回退](release/releases.md)
- [MyGuo 发布 skill：版本准备、提交、监控与完成判断](../.agents/skills/myguo-release/SKILL.md)
- [2026-11-25 发布耗时复盘与改进经验](release/release-retrospective-2026-11-25.md)
- [2026-11-26 发布执行复盘：42 分钟反馈、重复操作与状态误判](release/release-execution-retrospective-2026-11-26.md)

## 历史审查 `audits/`

这些记录保留缺陷原因、修复依据与验证边界。旧 WebView 实现、Node 夹具测试、
`ui-audit/` 和 `ui-review/` 截图已移除；记录中的历史源码和证据需按所列提交从
Git 历史查看。当前截图可通过 `MYGUO_NATIVE_SCREENSHOT_DIR` 按需导出。

- [2026-11-09 全量审查](audits/AUDIT-2026-11-09.md)
- [2026-11-09 补充审查](audits/AUDIT-SUPPLEMENT-2026-11-09.md)
- [2026-11-13 代码审查](audits/AUDIT-2026-11-13.md)

## 第三方与许可 `legal/`

- [第三方组件及再分发说明](legal/THIRD_PARTY.md)

应用使用说明保留在根目录 [中文 README](../README.zh-CN.md) 和
[English README](../README.md)，版本说明保留在 [CHANGELOG](../CHANGELOG.md)。
