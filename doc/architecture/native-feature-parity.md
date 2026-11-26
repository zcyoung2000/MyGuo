# Native migration feature parity

Current implementation: MyGo v0.9.1, checked against the repository on
2026-11-26. Browsing, settings, recommendation playback and the full player
use native MyGo UI. `internal/playback` owns playback state; macOS uses
`internal/avkit`, while Windows uses the MyGo `ui/video` Media Foundation backend.
`main.go` creates a native window and calls Go services directly.

The retired WebView shell and its Node fixture tests have been removed.
Historical macOS migration measurements and their limits remain in
[native-player-progress.md](../playback/native-player-progress.md); they are
not a current cross-platform acceptance report.

| Area | Preserved behavior | Implementation and verification evidence |
| --- | --- | --- |
| Navigation | Recommend, discover, search, ranks, new releases, history, favorites, likes, reservations, settings; actor/detail entry; menu and notification navigation; sidebar resizing and persistence | `view.go`, `features.go`, `preferences.go`, native navigation handler in `main.go`; sidebar click, persisted-width, navigation-history and all-pages control tests in `internal/uiapp` |
| Recommend | Real channels, refresh, restored queue, vertical scroll/arrows, sound unlock, first-frame handoff, reversible navigation, adaptive preload and diagnostics; outgoing scenes stop hidden playback | `internal/playback/feed.go` and `controller.go`: restored cursor/queue, explicit fresh refresh, canceled scenes, reverse navigation, three resident decoders, actual buffered runway admission; controller regressions and native QA |
| Search/discover | Submit/clear, debounced suggestions and keyboard selection, dynamic genre filters, pagination/deduplication/retry, authentic poster fallback, actor-card entry | `service.go`, `features.go`, `view.go`, `artwork.go`; input, suggestion, filter/provider, async artwork and cursor-cycle regressions. Filter rows scroll separately to leave results accessible at the minimum window size |
| Rankings | Content and board tabs, ranks/promoted entries, metadata, pagination, actor links and explicit immediate playback | `feature_views.go`, `LoadRank`, `RankItemDTO.EpisodeCount`; ranking/actor and cursor-cycle tests. Pages deduplicate by content type and identity; changing boards discards obsolete results |
| New releases | Hot releases, calendar dates, upcoming/online state, reserve/cancel | `LoadReleases` uses the original distinct board/calendar services; calendar test checks date, upcoming state and reservation controls. Reservation changes propagate to discover/search/new-release snapshots |
| Detail | Explicit play, metadata/tags/stats/publish date, actors, original novel and tags, all episodes, seasons, adaptations, related shows, favorite/reservation actions, loading/retry and stale-response protection | `renderDetail`, `loadDetailAt`, `loadSupplement`, `RetryRelated`; detail supplement, related retry, stale navigation and mutation/read regressions. Opening a normal detail does not start hidden playback |
| Actor | Profile/avatar/tags/stats and paginated works | `renderActor`, `loadActor`; actor pagination and cursor-cycle tests. Only one request for the current actor runs at a time |
| History | Authentic records, completed/incomplete/search filters, progress/badges/time, delete/confirmed clear, original episode and position on resume | Native library views and existing store/services; last-episode completion, episode metadata/progress, removal and confirmation tests; native controller and media-host regressions verify explicit/saved resume positions and session ordering |
| Favorites/likes | Authentic records and counts, independent errors, removal and the correct liked episode | Separate library state/generations and per-episode removal; library provider/error and liked-episode removal tests. Mutations invalidate older reads; repeated pending actions cannot invert a local toggle twice |
| Reservations | Online/pending counts, date ordering, cancellation confirmation, online-show entry | `renderLibrary`, `visibleLibrary`, `reservationBefore`, existing reservation service; mutation/cancellation tests. One cancellation action is rendered per reservation card |
| Playback | Quality, speed, seek/buffer/time, volume/mute, playlist/current highlight, previous/next/next season, close/escape, fullscreen, PiP, orientation/fit and progress persistence | Native renderer/controller/AVKit cover saved/explicit-zero resume, quality/rate/volume/fit, HLS-to-full-file seek, last-episode replay/next drama, stale sources, shutdown draining and account ownership. System PiP enter/restore and switching drama on the same surface were exercised with actual decoded video |
| Media interactions | Existing favorite/like actions, comments/replies, danmaku and persisted appearance controls, ambient glow | Native comments/replies with cursor-cycle protection, native timed danmaku with hover like/+1 and appearance migration, GPU ambient sampling. Fixture send/replies/pagination and native layout/input tests cover the controls; no live cloud messages were sent |
| Platform | Media keys/menu, Now Playing/artwork/rate, prevent display sleep, fullscreen/PiP events, notifications, confirmations and keyboard focus | Existing native implementations retained; scroll gestures, menus and media commands now target the Go controller directly; native callbacks route to the active scene. Sidebar/cards expose button roles and keyboard activation. System PiP handoff and graceful quit were exercised; broader live OS/account acceptance limits are recorded in native-player-progress.md |
| Settings | Account profile/session management, QR sign-in, cache statistics/clear, device reset and software updates | `view.go` routes settings to native `renderSettings`; `settings.go` implements the actions through the settings IPC provider. Old-app account detection/import is removed; anonymous users use the normal QR login flow. Native tests verify WebView is absent, rendered actions, anonymous QR login, account-switch isolation and software-update actions |
| Runtime | Local hgapi/MediaServer integration, authentic SQLite/WAL data and CGO-free builds | Main service/store/media initialization retained; unit tests use isolated fixtures and never populate the user's library. Go unit/race tests, vet and CGO-free builds cover the active implementation |

User preference: no global Back/Forward buttons on any native page. Navigation
history remains internal. `TestNoGlobalBackForwardControlsOnAnyNativePage`
covers every native route, including actor and detail.

Grid row sizes account for metadata and actions. The headless MyGo rendering
test `TestNativeCollectionActionsFitTheMinimumWindow` checks actionable controls
at 1000 × 640 with the widest allowed sidebar. It also supports optional PNG
export with `MYGUO_NATIVE_SCREENSHOT_DIR`; these are test-fixture renders, not
screenshots of the running application or user-library records.

The sidebar exposes the native system material directly. Browsing pages softly
reflect a blurred strip of adjacent content into its rightmost 144 DIPs, fading
leftward while preserving navigation pixels and the existing split-pane input.
`sidebar_bleed.go` samples the current painted frame, so artwork and scrolling
update the tint together. Native playback, Reduce Transparency and High Contrast
do not use this effect. `TestSidebarBleed*` covers artwork updates, scrolling,
navigation, idle frames and accessibility fallbacks.

## Verification and remaining acceptance

Run from the repository root:

```sh
go test ./...
go test -race ./internal/playback ./internal/library ./internal/store ./internal/uiapp
go vet ./...
CGO_ENABLED=0 go build -o /dev/null .
git diff --check
```

Native page layouts and loading states are audited through MyGo headless
`ui.Tester` tests. To export current fixture renders on demand:

```sh
MYGUO_NATIVE_SCREENSHOT_DIR=/tmp/myguo-native-screens go test ./internal/uiapp -run TestNativePageScreens -count=1
```

These images use deterministic test data. They do not establish real-account
API success or native video composition. The running-app probes, AVKit capture
limitations and historical macOS runtime evidence are described in
[native-player-progress.md](../playback/native-player-progress.md).
Windows playback and PiP still require acceptance on a Windows machine, as
recorded in the project README.
