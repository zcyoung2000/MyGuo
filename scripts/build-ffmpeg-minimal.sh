#!/usr/bin/env bash
# Build the media helper: Windows x64 with Zig, macOS with Apple Clang.
# This is a remux/decrypt/poster helper; system frameworks play the video.
set -euo pipefail

tool_root="$(cd "$(dirname "$0")/.." && pwd)"
project_root="${MYGUO_PROJECT_ROOT:-$tool_root}"
work="${MYGUO_FFMPEG_BUILD_ROOT:-$project_root/tmp_ffmpeg_minimal}"
target="${1:-windows/amd64}"
# Reuse only complete, checksum-verified outputs, including on Windows CI.
case "$target" in
  windows/amd64) cached="$work/output" ;;
  darwin/arm64|darwin/amd64) cached="$work/output-darwin-${target#*/}" ;;
  *) echo "Unsupported target: $target" >&2; exit 1 ;;
esac
if python3 "$tool_root/scripts/verify-ffmpeg-minimal.py" "$cached" "$target" 2>/dev/null; then
  echo "Using verified minimal FFmpeg ($target): $cached"
  exit 0
fi
case "$target" in
  windows/amd64)
    zig="${MYGUO_ZIG:-$(command -v zig || true)}"
    if [[ -z "$zig" || "$("$zig" version)" != "0.15.2" ]]; then
      echo "Set MYGUO_ZIG to Zig 0.15.2 (https://ziglang.org/download/)." >&2
      exit 1
    fi
    compiler="Zig 0.15.2"
    binary="ffmpeg.exe"
    ;;
  darwin/arm64|darwin/amd64)
    if [[ "$(uname -s)" != Darwin ]]; then
      echo "The macOS helper must be built on macOS with Xcode command line tools." >&2
      exit 1
    fi
    clang="$(xcrun --find clang)"
    sdk="$(xcrun --sdk macosx --show-sdk-path)"
    compiler="$("$clang" --version | head -n 1)"
    binary="ffmpeg"
    ;;
  *) echo "Unsupported target: $target (use windows/amd64, darwin/arm64 or darwin/amd64)" >&2; exit 1 ;;
esac
mkdir -p "$work"
work="$(cd "$work" && pwd)"
source_sha="8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e"
archive="$work/ffmpeg-9.0.2.tar.xz"
checksum() { shasum -a 256 "$1" | awk '{print $1}'; }
recipe_sha="$(checksum "$tool_root/scripts/build-ffmpeg-minimal.sh")"
if [[ ! -f "$archive" ]] || [[ "$(checksum "$archive")" != "$source_sha" ]]; then
  curl -fL --retry 2 --retry-max-time 150 --connect-timeout 10 --max-time 120 \
    -o "$archive" https://ffmpeg.org/releases/ffmpeg-9.0.2.tar.xz
fi
if [[ "$(checksum "$archive")" != "$source_sha" ]]; then
  echo "FFmpeg source checksum mismatch" >&2
  exit 1
fi
tar -xf "$archive" -C "$work"
src="$work/ffmpeg-9.0.2"
common=(
  --disable-everything --disable-autodetect --disable-doc --disable-debug
  --disable-shared --enable-static --disable-programs --enable-ffmpeg
  --enable-small --disable-asm --disable-avdevice --disable-iconv
  --disable-zlib --disable-bzlib --disable-lzma --enable-network
  --enable-demuxer=mov,aac,hls,mpegts,image2,gif,jpeg_pipe,webp_pipe
  --enable-muxer=mp4,mov,hls,mpegts,image2,mjpeg,null
  --enable-decoder=h264,hevc,aac,mjpeg,webp,gif
  --enable-encoder=mjpeg,wrapped_avframe,pcm_s16le
  --enable-parser=h264,hevc,aac,mjpeg,webp
  --enable-bsf=aac_adtstoasc,extract_extradata,h264_mp4toannexb,hevc_mp4toannexb
  --enable-filter=scale,format,null,buffer,buffersink,aformat,anull,aresample,abuffer,abuffersink
  --enable-protocol=file,http,https,tcp,tls,pipe,crypto,data
)
build="$work/build-${target//\//-}"
mkdir -p "$build"
cd "$build"
if [[ "$target" == windows/amd64 ]]; then
  export ZIG_GLOBAL_CACHE_DIR="$work/zig-cache"
  export MYGUO_FFMPEG_ZIG="$zig"
  cat > "$work/zig-cc" <<'CC'
#!/usr/bin/env bash
# FFmpeg's GNU PE linker flag is redundant with lld's PE output and is not
# understood by Zig's linker. Keep the entry point and ASLR/NX flags intact.
args=()
for arg in "$@"; do args+=("${arg/--pic-executable,/}"); done
exec "$MYGUO_FFMPEG_ZIG" cc -target x86_64-windows-gnu "${args[@]}"
CC
  chmod +x "$work/zig-cc"
  "$src/configure" "${common[@]}" \
    --enable-cross-compile --target-os=mingw32 --arch=x86_64 \
    --enable-schannel --enable-w32threads --disable-pthreads \
    --cc="$work/zig-cc" --ld="$work/zig-cc" \
    --ar="$zig ar" --ranlib="$zig ranlib" --extra-ldflags=-Wl,-s
  # The linker strips the PE; avoid requiring host GNU objcopy.
  make -j"${MYGUO_FFMPEG_JOBS:-4}" ffmpeg_g.exe
  built="ffmpeg_g.exe"
  out="$work/output"
  tls="Windows Schannel"
else
  arch="${target#*/}"
  if [[ "$arch" == arm64 ]]; then
    ff_arch=aarch64
    cc_arch=arm64
  else
    ff_arch=x86_64
    cc_arch=x86_64
  fi
  "$src/configure" "${common[@]}" \
    --enable-cross-compile --target-os=darwin --arch="$ff_arch" \
    --cc="$clang" --sysroot="$sdk" --enable-securetransport \
    --extra-cflags="-arch $cc_arch -mmacosx-version-min=13.0" \
    --extra-ldflags="-arch $cc_arch -mmacosx-version-min=13.0"
  make -j"${MYGUO_FFMPEG_JOBS:-4}" ffmpeg
  built="ffmpeg"
  out="$work/output-darwin-$arch"
  tls="macOS SecureTransport"
fi
mkdir -p "$out"
install -m 0755 "$built" "$out/$binary"
cp "$src/COPYING.LGPLv2.1" "$out/ffmpeg-LICENSE.txt"
cat > "$out/ffmpeg-README.txt" <<NOTICE
FFmpeg 9.0.2, minimal $target build for myguo.
Copyright FFmpeg contributors. LGPL version 2.1 or later.
Source: https://ffmpeg.org/releases/ffmpeg-9.0.2.tar.xz
Source SHA256: 8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e
Build recipe: scripts/build-ffmpeg-minimal.sh in the myguo source repository.
Compiler: $compiler; size optimized, no external codec libraries.
Includes CENC/MP4 processing, HLS, HTTP/HTTPS ($tls),
H.264/HEVC/AAC decoding, JPEG posters and HEIC conversion.
Video playback and audio output use the operating system's media framework.
NOTICE
python3 - "$out" "$source_sha" "$target" "$compiler" "$binary" "$recipe_sha" <<'PY'
from pathlib import Path
import hashlib,json,sys
out=Path(sys.argv[1])
info={"Format":"myguo-ffmpeg-minimal-v1","Version":"9.0.2","Target":sys.argv[3],
      "SourceSHA256":sys.argv[2],"Compiler":sys.argv[4],"License":"LGPL-2.1-or-later",
      "BuildRecipeSHA256":sys.argv[6],
      "Files":{name:hashlib.sha256((out/name).read_bytes()).hexdigest()
               for name in [sys.argv[5],"ffmpeg-LICENSE.txt","ffmpeg-README.txt"]}}
if sys.argv[3].startswith("darwin/"):
    info["DeploymentTarget"]="13.0"
(out/"ffmpeg-build-info.json").write_text(json.dumps(info,indent=2)+"\n")
print("Minimal FFmpeg directory:",out)
PY
python3 "$tool_root/scripts/verify-ffmpeg-minimal.py" "$out" "$target"
if [[ "$target" == windows/amd64 ]]; then
  echo "Package with: go run ./scripts/build-windows.go -ffmpeg-dir '$out'"
else
  echo "macOS helper ready in: $out"
fi
