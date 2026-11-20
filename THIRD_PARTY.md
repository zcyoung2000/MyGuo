# Third-Party Components

## OpenHongguo

- Upstream: OpenHongguo (<https://github.com/beilunyang/openhongguo>)
- License: Apache-2.0
- Usage: Portions of the local data models, catalog parsing and registration encoding in `internal/hgapi` were adapted from the Apache-2.0 source. The external SDK dependency has been removed. The application owns API requests, signing, authentication and playback sessions; attribution and license files are retained in `internal/hgapi/NOTICE` and `licenses/`.

## MyGo

- Upstream: MyGo (<https://github.com/egoist/mygo>)
- License: MIT
- Usage: Native macOS windowing, GPU-drawn UI, native module integration and packaging.

## FFmpeg

macOS release builds bundle a static FFmpeg executable selected for the target architecture. The binary is fetched by `scripts/fetch-ffmpeg.sh` and embedded in `<App>.app/Contents/Resources`.

- Upstream: FFmpeg (<https://ffmpeg.org/>)
- Build source: OSXExperts (<https://www.osxexperts.net/>)
- License: GPL; the packaged builds enable GPL components
- Source: download the matching FFmpeg source from <https://ffmpeg.org/download.html>

Redistribution of these GPL binaries must follow the applicable GPL terms, including providing the corresponding source or a written source offer.

## SQLite Driver

- Package: `modernc.org/sqlite`
- License: BSD-3-Clause; SQLite itself is public domain.
