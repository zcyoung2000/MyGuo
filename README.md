# myguo

A native macOS client for short dramas, built with Go.

[English](README.md) | [简体中文](README.zh-CN.md)

## Overview

myguo uses a MyGo GPU-drawn native UI: sidebar navigation, search, catalogs, series details, episode selection, and local history/favorites/reservations call Go services directly. Native AVKit handles video playback, episode switching, resume, PiP and system media controls. The repository's `internal/hgapi` owns the upstream protocol, including batch playback models, a dedicated player profile and Medusa V5. The local MediaServer handles caching, streaming and decryption; the external OpenHongguo SDK dependency has been removed.

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

- macOS 12 or later
- Go 1.27 or later
- Bash, curl, unzip, and `shasum` for fetching the bundled FFmpeg binary
- FFmpeg for development playback; release builds bundle a static binary

## Quick Start

```bash
git clone https://github.com/zcyoung2000/hongguo-mygo.git
cd hongguo-mygo
go mod tidy
go run .
```

### Build an App Bundle

```bash
./scripts/fetch-ffmpeg.sh
CGO_ENABLED=0 go run github.com/egoist/mygo/cmd/mygo build -skip-dmg
```

The bundle is written to `dist/darwin-arm64/myguo.app` on Apple Silicon, or `dist/darwin-amd64/myguo.app` on Intel.

## Account Sign-In

myguo can run with an anonymous device profile. To sync personal data:

1. Open **Settings**.
2. Choose **Sign in**.
3. Paste the account Cookie.
4. Optionally add `x-tt-token` for a longer-lived session.

The app can also detect credentials from a compatible local installation and offer a one-click import. Treat Cookies and tokens like passwords: keep them private and never commit or share them.

## Data and Storage

Application data is stored under `~/Library/Application Support/myguo/`. SQLite stores account credentials, device identity, the profile, history, favorites, likes, and reservations; there is no app-owned `account.json`. Cached media and converted images are stored separately and can be cleared from Settings.

## Architecture

- `internal/uiapp` and `internal/ipc` connect the MyGo native UI to application services.
- `internal/catalog`, `internal/library` and `internal/playback` own caching, SQLite state, synchronization and playback control.
- `internal/hgapi` encapsulates upstream endpoints, request and response models, signing, account sessions and website QR authorization.
- `internal/media` handles CDN downloads, caching, streaming and FFmpeg decryption; `internal/avkit` handles native video playback.

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

Review `THIRD_PARTY.md` before redistribution. Release builds bundle GPL-enabled FFmpeg, so applicable GPL obligations apply to those binaries.
