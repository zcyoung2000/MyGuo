# 退出、更新与重启

应用的退出协调在根目录 `shutdown.go`。关闭窗口时先阻止关闭，后台保存播放进度、
释放播放器、写入偏好，再关闭窗口。系统注销或关机仍使用已发布的播放快照，避免
在系统结束会话的回调里等待播放器。

普通退出和更新重启必须在原始 `OnBeforeQuit` 请求内完成。MyGo v0.9.1 的
`App.Relaunch` 在退出被 `PreventDefault` 取消时会清除重启标记；如果清理后补调
`App.Quit`，结果只有退出，macOS 的更新助手也不会收到重新打开应用的标记。
更新窗口和设置页都走 `App.Relaunch`，因此修复位于应用生命周期层。

播放器的最终时钟读取和资源释放需要 UI 主线程。`native.WaitForShutdown` 在等待
后台清理期间继续处理这些原生任务：macOS 运行 CoreFoundation 默认模式，Windows
处理消息队列。不要改成在主线程直接等待 channel，也不要把播放器清理移到
`OnClosed` 后才执行，否则会阻塞原生桥接。

在有桌面会话的 macOS / Windows 上运行原生进程回归：

```sh
MYGUO_NATIVE_LIFECYCLE_TESTS=1 go test -timeout 90s -run TestNativeShutdownPreservesRelaunchAndOrdinaryQuit -v .
```

测试使用隐藏窗口和独立的测试可执行文件，验证重启确实启动第二个进程、清理时
能回到主线程保存最终时钟，以及普通退出和关闭窗口都不会重启。测试不下载更新、
不替换已安装的应用；它验证退出与重启协调，不代替发布包更新安装验收。
