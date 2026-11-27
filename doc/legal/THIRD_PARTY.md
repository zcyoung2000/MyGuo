# Third-Party Components

## OpenHongguo

- Upstream: OpenHongguo (<https://github.com/beilunyang/openhongguo>)
- License: Apache-2.0
- Usage: Portions of the local data models, catalog parsing and registration encoding in `internal/hgapi` were adapted from the Apache-2.0 source. The external SDK dependency has been removed. The application owns API requests, signing, authentication and playback sessions; attribution and license files are retained in `internal/hgapi/NOTICE` and `licenses/`.

## MyGo

- Upstream: MyGo (<https://github.com/egoist/mygo>)
- License: MIT
- Usage: Native macOS and Windows windowing, GPU-drawn UI, native module integration, system video and packaging.

## FFmpeg

Release builds bundle an FFmpeg executable with static FFmpeg libraries. macOS defaults to the minimal helper built by `scripts/build-ffmpeg-minimal.sh`; `scripts/fetch-ffmpeg.sh` prepares it for `<App>.app/Contents/Resources`. Windows x64 uses the verified minimal helper when available, or the checksum-verified Gyan FFmpeg 9.0.2 essentials archive. The minimal executable, its LGPL license, README and build manifest ship in the Mac bundle or beside `myguo.exe`.

- Upstream: FFmpeg (<https://ffmpeg.org/>)
- Build source: pinned upstream FFmpeg for minimal builds; OSXExperts (<https://www.osxexperts.net/>) and Gyan (<https://www.gyan.dev/ffmpeg/builds/>, [pinned Windows release](https://github.com/GyanD/codexffmpeg/releases/tag/9.0.2)) for optional full builds
- License: GPL for full OSXExperts/Gyan builds; LGPL-2.1-or-later for the minimal macOS and Windows builds
- Source: download the matching FFmpeg source from <https://ffmpeg.org/download.html>

The minimal builds are LGPL-2.1-or-later, as reported by their FFmpeg configure output, and link no external codec libraries or GPL components. Their exact source is [FFmpeg 9.0.2](https://ffmpeg.org/releases/ffmpeg-9.0.2.tar.xz), SHA-256 `8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e`. macOS uses Apple Clang, a macOS 13 deployment target and system SecureTransport; Windows uses Zig 0.15.2 and system Schannel. The Windows helper also enables FFmpeg's `h264_mf` encoder, which calls the Windows Media Foundation encoder supplied by the OS; it does not bundle another codec library. The repository includes the complete build recipe; the package includes the matching license and a manifest of the compiler, target and file checksums before code signing. Full OSXExperts/Gyan builds retain their GPL licensing.

Redistribution must follow the license of the selected binary, including its requirements for corresponding source. The package carries the matching license text.

## SQLite Driver

- Package: `modernc.org/sqlite`
- License: BSD-3-Clause; SQLite itself is public domain.
