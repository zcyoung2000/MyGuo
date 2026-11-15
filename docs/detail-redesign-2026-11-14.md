# 剧集详情页与官方关联、剧评接口

按参考图调整结构，沿用应用的黑底、红色强调与轻量原生控件。统一顶部小返回图标，无额外页面名称。以下为 2026-11-15 的最终实现。

- 上方全宽剧集介绍：封面、片名、真实统计、标签、可展开简介、紧凑原著卡片、播放/续看、收藏和预约。演员可进入主页，无头像时使用姓名头像。
- 下方页签固定为 **选集｜剧评｜相关作品**。一张原生 `ui.List` 承载整页，页签滚动后固定；选集与剧评按行虚拟化，没有嵌套纵向滚动。
- 选集保留真实时长、续看定位和长列表跳转；没有时长时不编造。
- 剧评打开时才加载，每页 10 条；显示头像、正文、真实评分、日期、观看说明和赞同数。长文可展开，回复按需读取并就地展开，支持继续翻页。
- 相关作品使用自适应 Grid 分组展示“同系列其他季”“同一剧本改编”，排除当前剧；不加载“猜你喜欢”。其他改编版本自身的续季仍按官方作品条目显示，不把条目数宣称为独立版本数。

## 相关作品：已替换旧搜索流程

`GET /reading/bookapi/plan/v1/`，`from=detail_page_more_related`、`scene=10`、`book_id/filter_ids=当前短剧ID`、`offset=0`。

只读取该场景下 `show_type=547` 的 `video_data`：`tag_info.text=同IP` 进入改编组，“第 N 季”进入同系列其他季组。不能按标题或 `season_index` 分组，因为其他改编版本也可能有多季。两个组都排除当前剧并去重；`show_type=506` 的猜你喜欢始终忽略。

2026-11-15 修复：替换接口时误丢弃了季条目。现在 `SeriesCollectionDTO.Seasons` 随同一次相关作品请求返回，不追加 SDK Seasons、搜索或批量详情请求；分页合并两组，失败保留已有条目。季数角标、待上线及预约状态沿用官方数据，卡片进入各自详情。继续采用 MyGo v0.9.1 的原生 List + Grid，分区使用稳定 key，保持静默加载。

旧“原著名搜索＋候选批量详情＋原著 ID 比对”代码已删除；详情加载也不再先请求 `Seasons`。固定 SDK 尚未封装新场景，直接请求复用现有设备、账号、HTTP transport 与签名。成功页缓存五分钟；游标校验所属剧集及 offset 前进，错误不缓存。

真实游客请求：《十八岁的二次回响》返回 7 条相关作品，其中 2 条当前续季、5 条同IP作品；《凡人百世书第十季》返回 10 条当前各季、12 条同IP作品。两例未声明更多页，不能推导为全站完整总数。原著详情仍来自主详情接口，关联列表无需原著名或原著 ID 才能读取。

## 剧评：已验证的读取入口

`POST /novel/commentapi/comment/list/<seriesID>/v1/`，`comment_source=1`、`server_channel=34`、`group_type=1`、`comment_type=2`；不是播放页的单集评论。

回复为 `POST /novel/commentapi/reply/list/<reviewID>/v1/`，`comment_source=501`、`server_channel=34`，仍使用当前剧 ID 作为 `group_id`。具体请求和响应结构见 [逆向记录第三节](hongguo-api-reverse-engineering-2026-11-14.md)。此前把两者归到 UGC mget 的静态推断已更正。

客户端保留成功页并去重，失败重试保留已有数据及游标；校验剧集归属、有效状态及分页前进，导航后的旧响应不会写入新详情。主详情元数据刷新不会清掉独立的剧评请求、已加载页和回复状态。评分缺失或返回 -1 时不展示虚假评分。当前提供读取、展开及分页；没有放置尚未接通的发表/点赞按钮。

组件核对：MyGo v0.9.0 的 `Tabs`、`List`、`Avatar`、`Collapsible`、`Divider` 满足页面所需；评分只读文字，避免可编辑 `Rating` 的交互暗示。

## 验证

- 回归覆盖官方相关场景与筛选、缓存与错误、游标归属和循环、评分与评论归属、重复 ID 的元数据一致性。
- UI 测试覆盖 Tab 顺序、懒加载、翻页去重、错误重试、过滤空页的重试、长文展开、回复复用、导航旧响应和元数据刷新时序。
- 生产服务真实只读验证：同IP作品 5 条，剧评总数 911；首页及次页各 10 条，剧评回复 3 条。日志 `/tmp/myguo-official-detail-live.log`。
- 隔离数据原生窗口验证了实际 Tab 顺序、剧评排版、长文展开、回复展开、加载更多和官方关联作品；截图 `/tmp/myguo-official-detail-qa/reviews-wide.png`、`related-wide.png`。未修改真实账号，未发送评论/剧评或覆盖安装版。
- 全量 Go、IPC/UI race、vet 通过；对应 `/tmp/myguo-official-detail-{all,race,vet}-final.log`。构建为 `dist/darwin-arm64/myguo.app`，日志 `/tmp/myguo-official-detail-build-final.log`。

其他季恢复验证：`/tmp/myguo-detail-seasons-live-reloaded.log`，真实《十八岁的二次回响》同一页返回第二季、第三季及 5 条同IP作品。后端与 UI 回归覆盖分组、去重、待上线、卡片导航与不额外请求季接口；全量/race/vet/打包日志为 `/tmp/myguo-detail-seasons-{all,race,vet,build}.log`。

原著卡片按用户选择移到上方简介与播放操作之间，内容自适应宽度、最大 420 DIP，长标题最多两行、标签单行省略。相关作品底部不再重复全宽原著卡片。`detail_source_layout_test.go` 验证 800/1600 宽窗口、短/长标题的顺序、收窄与边界；沿用原生 Row/Column/Image/Text，无新增自绘控件或加载提示。

简介展开修复：移除 110 字阈值。正文与 `ShapeText` 使用相同字体、字号和可用列宽，按实际换行是否超过三行决定展开/收起入口；排版结果按正文/宽度/字体缓存。回归覆盖截图同款长简介在宽窗口完整显示、窄窗口截断、展开/收起、再次放宽，以及三行/四行换行文本。`Text.MaxLines` 没有截断查询，`TextLines` 仅服务编辑区，因此采用官方 `ShapeText` 的行几何，无自绘文本。

上述三项调整的 Go/race/vet 验证日志：`/tmp/myguo-detail-final-{all,race,vet}.log`。它们与历史身份修复合并后的最终检查及打包见 `/tmp/myguo-history-final-{all,race,vet,build}.log`。
