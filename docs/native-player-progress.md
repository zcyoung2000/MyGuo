# Native player implementation and verification

The subsequent recommendation/episode preload review is recorded in
[`preloading-2026-11-14.md`](preloading-2026-11-14.md).

The updated picture/title click behavior is recorded in
[`player-interactions-2026-11-14.md`](player-interactions-2026-11-14.md).

The subsequent browsing palette, loading and motion review is recorded in
[`ui-polish-2026-11-14.md`](ui-polish-2026-11-14.md).

The application uses MyGo v0.9.0 and native UI/AVKit for both recommendation
and series playback. Main no longer embeds or creates a playback WebView,
binds a playback JavaScript service, or relies on a web playback clock.
The installed /Applications application has not been replaced. No branch,
worktree, workspace or commit was created. Existing audit fixes were preserved.

## Implementation

- `internal/avkit`: generated native-module bridge plus handwritten AVPlayerView
  implementation. Playback intent/KVO readiness/deferred seek, buffered and
  seekable ranges, system PiP through public accessibility actions, native
  lifetime management, and GPU-reduced ambient sampling. Generated files must
  only be changed with the MyGo generator.
- `internal/playback`: serialized scene controller, stale-result cancellation,
  restored feed cursor/queue and explicit fresh refresh, bounded decoder
  residency, muted preroll and actual decoder runway admission. Resume, pause,
  quality/rate/volume/fit/mute, episodes/seasons, end/replay, reverse navigation,
  comments/replies, danmaku timeline/send/like/+1 and preference persistence.
- Native MyGo controls and responsive drawers; keyboard focus protection,
  B/F/P shortcuts, fullscreen/sidebar handling, reduced-motion-aware feed
  entrance, collision-free painted danmaku lanes and hover actions. Unchanged
  paused scenes no longer invalidate the entire UI every polling tick.
  AVKit's standard chrome is hidden outside PiP requests, avoiding duplicate
  controls. The complete right pane is a black video stage. Controls, title,
  actions and drawers float above it; opening a drawer leaves the stage and
  sidebar widths unchanged. Native clear glass on controls samples the real
  AVKit surface; text panels use a dark scrim and plain backdrop blur.
- Scope-bound delayed interactions and progress. SQLite commits visible
  progress/favorites together with their durable sync intent; account validation
  occurs under the same store lock as the local write. Progress is coalesced
  and drained before quit. Session-end persistence uses a published clock.
- Existing explicit WebKit danmaku keys are imported read-only into native
  preferences once. Existing native preferences take precedence. No hidden
  WebView is created for migration.
- `MYGUO_DATA_ROOT` isolates support and cache files. It also disables implicit
  legacy user-data import. Test builds may opt into MyGo automation with `e2e`.

## Runtime evidence (2026-11-13)

- Follow-up playback fix: the pinned SDK's old timestamp-only Argus/Ladon
  playback headers produced HTTP 200 with zero bytes even for newly registered
  devices. Pinning openhongguo `v0.2.1-0.20261112155120-1d3354a932bc` supplies
  full playback signatures and omits Medusa on this endpoint. The same real
  episode then returned five streams, 154 seconds of metadata, and a valid
  locally derived content key. A read-only check using the installed account's
  credentials also succeeded after the SDK's bounded device recovery.
- A fresh, isolated production app decoded multiple real upstream dramas
  through SDK → FFmpeg decryption/HLS → AVKit. The first cold selection displayed
  at 3058 ms; the real second episode loaded at 711 ms and advanced past 56 s.
  These are individual observations, without synthetic media or seeded caches.
  The checks made no cloud social or account-data writes.
- MediaServer now relies on the SDK's coordinated device recovery instead of
  resetting again and evicting unrelated playback models. HTTP media requests
  acquire a temporary spare-device session when needed. Pool clients disable
  independent SDK device replacement and accept usable protected models instead
  of treating the SDK's DRM marker as a failed device. A wire-level regression
  test checks the full playback envelope through the real account transport.
- Rendition negotiation reuses the resolved episode ID. This prevents an
  existing lower-quality cache from overriding an explicit quality switch.
  Selecting the preferred quality after a lower-quality cache bootstrap also
  renegotiates the source. Both cases have regression tests. Real 1080P → 720P
  → 1080P switching preserved the paused position at 18.566 seconds; resuming
  advanced past 20 seconds.
- Production application + actual AVKit decoded a synthetic cached MP4 through
  the real backend. First frame: 489 ms in this isolated run. A macOS window
  capture verified video pixels and danmaku compositing, including pause.
  The signed CGO-disabled app also decoded a restored cached fixture through
  the real backend (499 ms to first frame in that run).
- Static local HLS decoded continuously. A native probe measured 1324 ms to
  display in the final PiP-cycle run. Initial resume at 42 seconds was also
  exercised. These are single local observations, not P50/P95 or an upstream
  cold-stream/performance comparison.
- System PiP entered, reported active, restored, and continued advancing the
  clock. Current macOS labels the actions Minimize Video / Maximize Video;
  some controls require the public legacy accessibility action bridge.
- The production renderer/controller/AVKit local QA passed PiP enter, next
  drama while in PiP, reuse of the same native surface, restore, and a paused
  seek to 31.25 seconds. No cloud social calls were made by this fixture.
- Native controls were exercised for pause, quality, speed, mute, fit,
  comments/replies/pagination, episodes, clear screen, danmaku input/send and
  reverse navigation. The fixture replaces catalog/social responses; it does
  not establish authenticated upstream API success.
- Graceful application quit completed without an AppKit/actor deadlock.
  SQLite history and the pending sync intent contained the same final clock
  (19.342955352 seconds in the first quit run). The PiP handoff run additionally
  tests paused seek persistence; inspect its isolated database for the result.

AVFoundation rejects the old unknown-total progressive MP4 206 and the tested
chunked 200 alternative. Native cold playback therefore negotiates HLS;
completed MP4 remains the cache path. Seeking outside a growing HLS prefix
switches to the complete-file source while retaining duration and pause intent.

Recommendation queue consumption now occurs only after a ready frame, so
failed loads remain pending across restarts.

## Original migration checks

- Go: 617 test/subtest passes, 23 packages checked, no failures.
- Race: playback, library, store and UI; UI rerun after the final layout fix.
- Vet, native-module check and git diff --check passed.
- Legacy JavaScript reference: 128 passes; these do not verify native playback.
- Signed CGO-disabled darwin/arm64 application built successfully: 25.5 MB,
  `dist/darwin-arm64/myguo.app`. Strict recursive signature verification passed.
- The final native QA passed system PiP enter, switching drama in PiP while
  retaining the same surface, restore and paused seek. After graceful exit,
  both SQLite history and pending sync intent contained exactly 31.25 seconds
  for the selected second drama.

## Playback follow-up checks

- Full Go suite and `go vet ./...` pass after the SDK and cache fixes.
- Race checks pass for auth, media, IPC, spare-device management and playback;
  changed packages were rerun after their final changes.
- The final 25.5 MB CGO-disabled application passes strict recursive signature
  verification. Its embedded Go build information confirms the SDK revision
  above and MyGo v0.9.0. The installed application was not overwritten.
- `cmd/playback-diagnose` checks real model availability and content-key
  derivation without printing credentials, device IDs, signed URLs or keys.
  Its optional `-account-db` argument opens the database read-only; anonymous
  device profiles remain in memory.

## Immersive native layout follow-up

- Aspect-fit remains the default: the complete picture and subtitles stay
  visible, with plain black letterboxing. There is no blurred video fill.
  Portrait video uses the available height; landscape video uses the available
  width. Header, footer and action rail no longer reserve picture space.
- The danmaku canvas includes black margins. It reserves the channel selector
  and bottom controls vertically. Horizontal travel starts at the right stage
  edge and exits at the left, passing behind the rail and drawers. Opening a
  drawer leaves that path unchanged. Drawing, hover hit tests and interaction
  positioning use the same rectangle.
- Feed changes slide a full stage height over 260 ms, with a bounded outgoing
  JPEG captured once from the existing decoder. The incoming surface attaches
  behind that still until a frame is ready for display. A fresh per-selection
  animation clock fixes repeated swaps incorrectly retargeting from 0 to 0.
  Direction reverses for previous navigation; the newest selection interrupts
  the previous transition. Reduce Motion snaps, and PiP retains its view.
- Layout/interaction tests cover the edge-to-edge stage, floating drawers,
  letterbox danmaku, successive/reverse transitions, display readiness and
  Reduce Motion. Existing decoder-residency tests also check outgoing captures.
- Own-window macOS captures with real portrait and landscape files verified
  actual AVKit pixels, full-picture framing, black margins and native glass.
  A 30 fps contact sheet of the final recording shows the incoming landscape
  page and outgoing portrait still moving in opposite directions; no empty
  backing frame appeared in that capture after the display-readiness fix.
  Catalog and social responses in this visual check are isolated fixtures.
- Full Go tests, playback/UI race checks, vet, native-module validation,
  diff whitespace checks and strict recursive app-signature verification pass.
  The rebuilt signed application is `dist/darwin-arm64/myguo.app` (25.5 MB).

## Shared surfaces and playback menus

- `internal/uiapp/surfaces.go` is the only application source constructing
  glass/blur materials. Native clear glass is shared by player controls,
  channel selectors, page tabs, search fields and secondary navigation.
  Artwork badges sample the painted UI surface using the same clear profile
  and an additional dark tint; native glass cannot sample UI-painted posters.
- Content panels use a 92% black scrim with a 6 DIP plain backdrop blur,
  without the Liquid Glass lens or rim. Buttons inside panels use simple
  washes. Reduce Transparency gives those panels an opaque black base.
  MyGo's blur samples UI-painted content; AVKit is a separate native surface.
  The black scrim provides contrast independently of that video backdrop.
- Quality and speed buttons open actual system menus at the button. AppKit
  owns placement, keyboard handling, current-item checkmarks and dismissal.
  This avoids the drawn-menu dismissal click passing through to video pause.
- Shared tabs now use `SegmentedBase` for keyboard and accessibility semantics,
  keeping the sliding selection capsule. Search inputs retain their existing
  suggestion/IME handling and advertise native search-field semantics.
- Regression checks cover selecting and cancelling menus, unavailable quality,
  shared-tab arrow keys, panel contrast over white, Reduce Transparency and
  unchanged edge-to-edge danmaku when a drawer opens. Full Go/vet and UI race
  checks passed; the rebuilt 25.6 MB app passes strict signature validation.
  Runtime AX inspection confirmed actual native quality/speed menu items.
  Native menu selection could not be driven reliably by CUA; selection and
  cancellation were verified through MyGo's headless menu implementation.
  Own-window captures also checked the dark comments panel over actual AVKit
  video and the history page's shared capsule tabs/search field.

## Reproduce

```sh
go test ./...
go run ./cmd/playback-diagnose -episode 7694371347610209304
go test -race ./internal/playback ./internal/library ./internal/store ./internal/uiapp
go vet ./...
go run github.com/egoist/mygo/cmd/mygo native check ./internal/avkit
node --test e2e/*.test.cjs
CGO_ENABLED=0 go run github.com/egoist/mygo/cmd/mygo build -platform darwin/arm64 -skip-dmg
git diff --check
```

`cmd/player-probe URL --measure --pip --pip-cycle` tests actual engine/PiP
without application accounts. `cmd/native-player-qa -source LOCAL_URL -data
TEMP_DIRECTORY -exercise-pip -quit-after 35s` uses the production native UI,
controller, bridge and SQLite with local catalog/social fixtures. Run it with
a unique MYGO_AUTOMATION socket when interactive acceptance is needed.
`cmd/media-probe-server` serves prepared local test media. It does not generate
or download the media. MyGo screenshots omit native AVKit pixels; use an
own-window macOS capture when checking the composed video image.

## Validation boundaries

The checks above cover implementation and isolated native runtime behavior.
Legacy JavaScript tests remain behavioral references, not native rendering tests.

The original migration run's empty upstream playback models were fixed by the
SDK update above. Actual anonymous cold playback and a read-only account-backed
playback-model/key check now pass. A real weak network and real-account cloud
writes were not exercised; their regression tests use isolated stores and
mock responses. No comparative CPU/memory/power improvement or zero-regression
claim has been established. FFmpeg remains an external playback prerequisite
as before; the existing resolver checks standard Homebrew locations even for
GUI processes with a minimal PATH.
