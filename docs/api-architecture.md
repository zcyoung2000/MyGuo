# 上游 API 分层

红果业务接口统一归 `internal/hgapi` 所有。业务模块不再持有上游 URL、协议
路径、签名计算、HTTP 请求构造、gzip 正文或 JSON 响应解析。

## 各层职责

| 层 | 目录 | 职责 |
| --- | --- | --- |
| UI 和应用入口 | `internal/uiapp`、`internal/ipc` | 页面操作、当前账号与播放场景归属、公开 DTO、本地图片地址转换、服务组合 |
| 业务状态 | `internal/catalog`、`internal/library`、`internal/playback` | 缓存、列表富化、持久化队列、账号切换保护、同步调度、播放恢复 |
| API 和协议 | `internal/hgapi` | 接口参数、线上路径、请求与响应模型、游标校验、签名与压缩、错误分类 |
| HTTP 传输 | `hgapi.Exchange`、`internal/auth`、`internal/upstream` | 有界 HTTP 收发、账号注入策略、官方 Host 选择；没有业务参数改写 |
| 媒体 | `internal/media`、`internal/avkit` | CDN 下载、解密、缓存、本机流式传输、原生解码与显示 |

媒体下载和本机图片缓存仍使用各自的流式读取方式；它们不属于 JSON 业务 API，
不能套用业务接口的正文大小限制。

## API 文件

- `client.go`、`catalog.go`、`rank.go`、`series.go`：匿名设备和目录接口。
- `playback.go`：专用播放 profile、设备、批量模型及有界协议恢复。
- `session_client.go`：绑定账号和设备快照的公共请求入口。
- `account.go`：账号凭据验证，不负责保存或切换用户。
- `comments.go`、`danmaku.go`、`reviews.go`：评论、回复、弹幕、剧评参数和解析。
- `detail_endpoints.go`、`related.go`：相关作品和详情互动。
- `account_rank.go`：登录态排行榜、游标和解析。
- `library_endpoints.go`、`library_models.go`、`progress_payload.go`：历史、进度、
  收藏、点赞、预约；单位和分集索引转换在 API 层完成。
- `passport/`：官网扫码协议，独立 Cookie jar、手机确认和同域跳转限制。
- `http.go`：统一的 HTTP 请求转换、正文限额、响应头和 Cookie 分离。

原来的 `internal/filterapi` 和 `internal/qrauth` 已移除。画风筛选直接由
`Client.ShortFilter` 编码并签名，继续接受旧 `art1.` 游标；官网登录直接使用
`hgapi/passport`。传输层不再修改已经签名的分类正文。

## 会话与重试

`hgapi.Session` 是一次请求的账号、设备、UA 和区域快照。
`ipc.requestAPI(ctx)` 从播放场景绑定的账号取得快照；`library.apiForSession`
使用同步任务已有的 `syncSession`。账号变更后的落库和队列确认检查仍由
业务层负责，延迟结果不会写入另一个账号。

账号接口、匿名目录、播放专用 profile 和官网 Passport 使用各自的会话策略，
共用 HTTP 收发边界。匿名设备的 Cookie 不构成登录；Guest 会话不能发送
收藏、点赞、预约或弹幕写入。

账号写入接口只发送一次。同步队列决定是否重试；100103 仍能通过
`errors.Is(err, hgapi.ErrParameterInvalid)` 被业务层识别，用于缩小历史批次。
发生连接中断时，公共 API 层不会自动重复一次可能已经生效的写入。
目录、播放和 Passport 的原有有界恢复继续由其对应客户端负责。

传输错误不会打印带凭据的 URL。HTTP 正文解压与业务状态解析分开，避免把
`{"code":0}` 写入确认当成空匿名数据，或丢失历史批次的参数错误信号。

## 添加接口

1. 在对应 `hgapi` 领域文件中添加具名方法和请求/响应类型；URL 与协议字段
   只出现于这一层。调用方传入应用单位和内容引用，不传 URL 或请求头。
2. 复用 `SessionClient.call` / `mutation`，或已有的目录、播放、Passport
   请求流程。需要独立身份时明确设置会话策略。
3. 业务层调用具名方法，组合数据、检查所属账号、更新本地缓存或持久化。
4. 使用 HTTP fixture 验证线上参数和签名正文，以及错误后是否会重复提交。

`TestBusinessPackagesDoNotOwnUpstreamHTTPOrSigning` 检查 `ipc`、`catalog`、
`library`、`auth` 的源码边界，防止上游路径、请求构造或签名重新散落到业务层。
现有同步、分页、账号与播放回归继续运行；公共入口另有会话快照、gzip
签名绑定、写入不重放、Guest 写入拒绝、响应限额和错误脱敏检查。

外部 openhongguo SDK 依赖继续为零。部分本地模型、目录解析和注册编码的
Apache-2.0 来源声明仍保留，见 `internal/hgapi/NOTICE`。

## 本机验证（2026-11-17）

全量 Go 测试、`hgapi` / `passport` / `auth` / `library` 的 race 检查，以及
macOS arm64 应用包构建均通过。构建产物已安装到 `/Applications/myguo.app`，
安装版与构建产物的可执行文件 SHA-256 一致。

实际启动日志确认协议客户端、媒体服务和原生窗口初始化完成；缓存命中的
推荐视频出现首帧并开始推进播放时钟。这次运行仍出现历史上报 `100103`
参数错误及部分分集的 `video_model` 空响应。安装前的日志中也存在这两类
失败；历史、进度、收藏的六组请求正文与重构前一致。API 分层验证不代表
所有上游账号写入和分集取址都已成功，这两类线上失败仍未解决。

上述内容是首次安装时的记录。后续根据参考程序和实网对照修正了历史读取
路径、顶层 `video_data` 解析、单剧进度请求和两个上报字段，并处理历史更新
`100103` 阻塞进度上报的问题，见
[历史与播放 API 故障核对](obsidian://open?vault=HenryNote&file=30_%E7%A0%94%E7%A9%B6%2F%E5%B7%A5%E5%85%B7%E6%8A%80%E6%9C%AF%E8%B0%83%E7%A0%94%2F%E7%BA%A2%E6%9E%9C%E7%9F%AD%E5%89%A7+API%2F%E9%80%86%E5%90%91%E8%AE%B0%E5%BD%95%2Fapi-failure-fix-2026-11-17)。
