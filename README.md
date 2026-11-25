# MyGuo

A native macOS and Windows client for short dramas, built with Go.

[English](README.md) | [简体中文](README.zh-CN.md)

## Overview

MyGuo uses a MyGo GPU-drawn native UI: sidebar navigation, search, catalogs, series details, episode selection, and local history/favorites/reservations call Go services directly. macOS uses native AVKit; Windows uses MyGo's `ui/video` Media Foundation backend. Both share episode switching, resume and system media controls. The repository's `internal/hgapi` owns the upstream protocol, including batch playback models, a dedicated player profile and Medusa V5. The local MediaServer handles caching, streaming and decryption; the external OpenHongguo SDK dependency has been removed.

Hongguo business APIs, account requests and website QR authorization are encapsulated in `internal/hgapi`. Business services own caching, account scope, durable synchronization and page composition. See [API architecture](docs/api-architecture.md) for the boundaries and endpoint extension workflow.

The project is intended for personal technical study and local use. It is not affiliated with or endorsed by the upstream content provider.

## Features

| Area | Description |
| --- | --- |
| Native desktop UI | MyGo `ui` GPU-drawn shell with hidden-inset title bar, sidebar navigation, dark theme, responsive/virtualized grids, and SVG icons. |
| Discovery | Recommendations, search and suggestions, filters, rankings, new-release schedules, actor pages, and series details. |
| Local library | History, favorites, likes, and reservations stored in SQLite; the native shell reads and writes these records directly through existing services. |
| Comments and danmaku | Episode comments, replies, timed danmaku playback, sending, and likes. |
| Playback | Episode lists, quality selection, progress saving, continuous playback, and keyboard controls. |
| Media pipeline | A local server proxies video, decrypts protected streams through FFmpeg, and supports HTTP range requests. |
| Local persistence | SQLite stores account credentials, device identity, profile, and user library data with WAL mode and automatic legacy-data import. |
| CGO-free build | The Go runtime and SQLite driver build with `CGO_ENABLED=0`. |

## Requirements

- macOS 13 or later, or Windows 10/11 x64
- Go 1.27 or later
- Xcode command line tools, Bash, curl, tar, make, Python 3 and `shasum` for macOS FFmpeg builds; Windows packaging only needs Go
- FFmpeg for development playback; release builds bundle a static binary

## Quick Start

```bash
git clone https://github.com/zcyoung2000/MyGuo.git
cd MyGuo
go mod tidy
go run .
```

### Build an App Bundle

```bash
./scripts/fetch-ffmpeg.sh
CGO_ENABLED=0 go run github.com/egoist/mygo/cmd/mygo build -skip-dmg
```

The bundle is written to `dist/darwin-arm64/MyGuo.app` on Apple Silicon, or `dist/darwin-amd64/MyGuo.app` on Intel.

To create the macOS release DMG, omit `-skip-dmg`:

```bash
./scripts/fetch-ffmpeg.sh darwin/arm64
CGO_ENABLED=0 go run github.com/egoist/mygo/cmd/mygo build -platform darwin/arm64
```

MyGo creates `dist/darwin-arm64/myguo-0.1.7-arm64.dmg`, containing `MyGuo.app` and a link to Applications for installation. Download published builds from [Releases](https://github.com/zcyoung2000/MyGuo/releases).

To build and install the minimal bundle in `/Applications`, run:

```bash
./build-install.sh
```

Both macOS workflows default to minimal FFmpeg. The first run verifies and compiles pinned FFmpeg 9.0.2 source using Apple Clang for macOS 13 or later; later runs validate and reuse the generated helper, license and build manifest. No Zig installation is needed for macOS. The installer verifies the signed bundle, backs up the previous app under `~/Library/Application Support/myguo-backups`, installs it and opens it. To prepare the full OSXExperts helper explicitly, use `./scripts/fetch-ffmpeg.sh --full` followed by the manual MyGo build command above; `build-install.sh` always prepares the minimal helper.

### Build Windows

Run from the repository root on Windows, macOS or Linux:

```sh
go run ./scripts/build-windows.go
```

This reuses a verified minimal FFmpeg when its build manifest exists in `resources/windows-amd64`; otherwise it fetches the checksum-verified Gyan FFmpeg 9.0.2 archive. Install NSIS first (`brew install makensis` on macOS, `sudo apt-get install nsis` on Linux). The script creates `dist/windows-amd64/myguo-setup-0.1.7-amd64.exe`. Run the EXE to install MyGuo for your user, with a Start menu shortcut and an entry in Windows Settings for uninstalling. FFmpeg and its notices are included; no separate FFmpeg installation is needed. A full release archive can be supplied explicitly with `-ffmpeg-archive /path/to/ffmpeg-9.0.2-essentials_build.zip`.

To rebuild the minimal Windows helper on macOS/Linux, install Zig 0.15.2 and use Bash, curl, tar, shasum, make and Python 3:

```sh
MYGUO_ZIG=/path/to/zig ./scripts/build-ffmpeg-minimal.sh
go run ./scripts/build-windows.go -ffmpeg-dir ./tmp_ffmpeg_minimal/output
```

The minimal build keeps CENC decryption, MP4/HLS remuxing, HTTP/HTTPS, H.264/HEVC/AAC decoding and JPEG/HEIC poster conversion. It omits unrelated encoders, external codec libraries, filters and debug symbols. Subsequent packaging commands verify and reuse the installed helper. The script pins the FFmpeg source checksum and records the target, compiler, license and binary checksums in `ffmpeg-build-info.json`.

Windows PiP uses a resizable always-on-top window. Feed transitions show the next poster instead of capturing the outgoing GPU frame. Background media preloading is conservative because MyGo exposes no Windows buffered ranges; resume and seeks use the complete local MP4. H.264/AAC use system codecs; HEVC needs the Windows HEVC codec, and Windows N editions need the Media Feature Pack. Real Windows playback and PiP still require validation on a Windows machine.

## App Updates

From 0.1.6, **Help → Check for Updates** and **Settings → Software Updates**
check this repository's latest GitHub Release. Automatic checks are on by
default; automatic downloads are opt-in. Background updates take effect on
the next launch and do not restart playback. Updates require a writable app
installation directory. Users on 0.1.5 or earlier must install 0.1.6 manually
once to get the updater.

Every supported platform must publish its `update-<platform>.json` and signed
`.tar.gz` update archive alongside the installers. The release workflow signs
these with the repository secret `MYGO_UPDATER_PRIVATE_KEY` and refuses to
publish missing or invalid updates. See [release maintenance](docs/releases.md)
for signing-key storage, local builds and publishing without GitHub Actions.

## Recommendation Continuity

The recommendation cursor, new pending cards and recent exclusions commit together; restarting restores the unplayed queue. A card is consumed only after the selected video displays a frame. Failed acknowledgements retry, and normal shutdown drains them alongside playback progress. Each channel retains 200 recent series IDs across cursor and anonymous-session resets; pending cards are also excluded from new pages. Repeated-only pages remain retryable without publishing the repeats as a fallback. Older series outside the recent window can still return.

## Account Sign-In

MyGuo can run with an anonymous device profile. To sync personal data:

1. Open **Settings**.
2. Choose **Sign in**.
3. Paste the account Cookie.
4. Optionally add `x-tt-token` for a longer-lived session.

The app can also detect credentials from a compatible local installation and offer a one-click import. Treat Cookies and tokens like passwords: keep them private and never commit or share them.

## Data and Storage

SQLite stores account credentials, device identity, the profile, history, favorites, likes, and reservations; there is no app-owned `account.json`. Cached media and converted images are stored separately and can be cleared from Settings.

| Platform | Application data | Cache |
| --- | --- | --- |
| macOS | `~/Library/Application Support/myguo/` | `~/Library/Caches/myguo/` |
| Windows | `%LOCALAPPDATA%\myguo\` | `%LOCALAPPDATA%\myguo\Cache\` |

## Architecture

- `internal/uiapp` and `internal/ipc` connect the MyGo native UI to application services.
- `internal/catalog`, `internal/library` and `internal/playback` own caching, SQLite state, synchronization and playback control.
- `internal/hgapi` encapsulates upstream endpoints, request and response models, signing, account sessions and website QR authorization.
- `internal/media` handles CDN downloads, caching, streaming and FFmpeg decryption; `internal/playerengine` selects the platform player, with `internal/avkit` providing macOS native playback.

See [API architecture](docs/api-architecture.md) for request boundaries, session policies and the workflow for adding endpoints.

## Development

```bash
go mod tidy
go test ./...
CGO_ENABLED=0 go build -o /dev/null .
node frontend/ui-regression.test.js
go test -race ./internal/uiapp
node --test e2e/*.test.cjs
```

When changing the UI, keep list views in responsive card grids, use inline SVG rather than emoji or bitmap glyphs, and preserve native window drag behavior.

## License

This project is provided for personal technical study and local use. Drama content and metadata remain the property of their respective owners.

Review `THIRD_PARTY.md` before redistribution. Release builds bundle FFmpeg under the license of the selected build: LGPL for the minimal macOS and Windows helpers, GPL for the full codec builds.
