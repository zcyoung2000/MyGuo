# 静默分页与滚动稳定性（2026-11-15）

## 改动

找剧页原先用上下空白块模拟虚拟列表，同时对可见内容容器做位置动画；虚拟窗口切换会改变空白块、Gap 数量和动画目标。加载提示行的出现/消失也会改变滚动范围。现改用原生 List 按稳定条目 ID 保留可见位置，滚动行不再做位置动画。

MyGo 从 v0.9.0 升至 v0.9.1，使用新增的 ListState.EndReached / NearEnd。找剧、排行榜、演员作品、详情剧评和播放页评论由原生滚动事件触发分页，不再从重绘时的滚动距离反复调度请求。虚拟列表使用稳定身份，分页时保留固定页尾，不因加载状态增删行。导航与选择剧集只标记滚动重置请求，再由 UI 线程执行，避免后台导航与原生布局同时写入 ScrollState。

首屏不足一屏时，以不可见的 1 DIP 页尾配合 InView 自动补齐；相关作品的多张卡片属于一个列表分区，使用分区实际页尾的 InViewMargin。可见性来自上一轮布局：条目、游标或归属变化后，先完成一次新布局再判断，避免刚追加内容就因旧页尾仍可见而多取一页。连续三个没有新增可展示条目的页面会停止自动续取，保留游标及重试入口，避免无界空页请求。

移除常规“加载中”“正在更新”“滑动到底部加载更多内容”、分页按钮、转圈和骨架扫光。首屏是静态占位，已有内容在刷新中保留；错误与重试仍正常展示。播放页不再盖常规缓冲提示，底层缓冲状态、恢复逻辑及错误处理保持运行。海报解码后的内容淡入、用户触发的展开/收起及控件反馈仍保留。

## 组件选择

以 MyGo v0.9.1 的 docs/ui/list.md 和 ui/list.go 为依据：

- 原生 List：长列表虚拟化、稳定条目锚点、EndReached。
- 原生 Grid：同一分区内的相关作品排布。
- Element.InView / InViewMargin：首屏补齐及单个大分区实际页尾。
- 静态占位：普通 Box / Text 几何，不使用 Spinner、计时器或 AnimationFrame。
- 没有在虚拟行上添加 Position/Height 过渡；滚动位置由 List 管理。

## 验证

`pagination_stability_test.go` 使用实际 Wheel 事件和阻塞的分页提供者，验证等待请求、追加 30 张卡片和后续重绘期间，同一可见卡片的 Y 坐标误差不超过 0.5 DIP，并验证重绘不会再次请求。另覆盖筛选自动收起不振荡、首屏自动补齐后停止、连续空页停止且可重试。

`ui_skeleton_test.go` 比较间隔 3 秒的像素，确认占位静止且不再请求连续动画帧；跨浏览页面检查无常规加载提示。原有五千条目深滚动、响应式布局、详情分页失败重试和回复测试继续执行。

命令与结果记录：

- `/tmp/myguo-quiet-scroll-stability-last.log`：滚轮/分页边界测试通过。
- `/tmp/myguo-quiet-scroll-all-final.log`：全量 Go 测试通过。
- `/tmp/myguo-quiet-scroll-native-check.log`：MyGo 原生模块检查及 Swift 构建通过。
- UI 竞态检查通过（100.081 秒），`go vet ./...` 无诊断；结果分别记录在 `/tmp/myguo-quiet-scroll-{race-final,vet-final}.log`。
- 打包成功，`codesign --verify --deep --strict` 通过；`dist/darwin-arm64/myguo.app` 为 25.9 MB，构建日志 `/tmp/myguo-quiet-scroll-build-final.log`。

本次构建只输出 dist，不替换 /Applications 安装版；原生 UI 检查使用隔离数据目录和静音播放器。

原生隔离运行中检查了找剧页滚动/筛选折叠、AI 剧和排行榜；排行榜两次无输入快照的 10 个行节点 Y 坐标漂移为 0，无常规加载文字。截图 `/tmp/myguo-quiet-scroll-qa/rank-final.png`。分页网络延迟及追加位置用阻塞响应的滚轮回归精确覆盖，未将线上一次返回的终页当作多页验证。
