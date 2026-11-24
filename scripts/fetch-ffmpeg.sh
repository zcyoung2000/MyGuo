#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != Darwin ]]; then
  echo "This script prepares macOS resources; use build-windows.go for Windows." >&2
  exit 1
fi
mode=minimal
target=""
for arg in "$@"; do
  case "$arg" in
    --full) mode=full ;;
    darwin/arm64|darwin/amd64) target="$arg" ;;
    *) echo "Usage: $0 [darwin/arm64|darwin/amd64] [--full]" >&2; exit 1 ;;
  esac
done
if [[ -z "$target" ]]; then
  case "$(uname -m)" in
    arm64) target=darwin/arm64 ;;
    x86_64) target=darwin/amd64 ;;
    *) echo "Unsupported macOS architecture" >&2; exit 1 ;;
  esac
fi
arch="${target#*/}"
case "$arch" in
  arm64)
    arch="arm64"
    url="https://www.osxexperts.net/ffmpeg9arm.zip"
    expected="591260c945d0eef150e3bf82b0ef988bd36a9cecc18ff05d6679617159f0a95e"
    ;;
  amd64)
    arch="amd64"
    url="https://www.osxexperts.net/ffmpeg80intel.zip"
    expected="df3f1e3facdc1ae0ad0bd898cdfb072fbc9641bf47b11f172844525a05db8d11"
    ;;
  *)
    echo "unsupported macOS architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

root="$(cd "$(dirname "$0")/.." && pwd)"
output="$root/resources/darwin-$arch/ffmpeg"

# Older Intel downloads used uname's spelling, which MyGo rejects as a
# platform directory. Keep their contents when correcting the directory name.
if [[ -d "$root/resources/darwin-x86_64" ]]; then
  if [[ ! -e "$root/resources/darwin-amd64" ]]; then
    mv "$root/resources/darwin-x86_64" "$root/resources/darwin-amd64"
  else
    # Preserve an obsolete duplicate outside the packaged resources tree.
    legacy_backup="$(mktemp -d "$root/tmp_ffmpeg_x86_64.XXXXXX")"
    mv "$root/resources/darwin-x86_64" "$legacy_backup/darwin-x86_64"
  fi
fi

if [[ "$mode" == minimal ]]; then
  if python3 "$root/scripts/verify-ffmpeg-minimal.py" "$(dirname "$output")" "$target" 2>/dev/null; then
    echo "Using verified minimal FFmpeg ($target)"
    exit 0
  fi
  echo "Building minimal FFmpeg ($target); the first build downloads source and compiles it."
  "$root/scripts/build-ffmpeg-minimal.sh" "$target"
  build_root="${MYGUO_FFMPEG_BUILD_ROOT:-$root/tmp_ffmpeg_minimal}"
  helper="$build_root/output-darwin-$arch"
  python3 "$root/scripts/verify-ffmpeg-minimal.py" "$helper" "$target"
  mkdir -p "$(dirname "$output")"
  install -m 0755 "$helper/ffmpeg" "$output"
  for name in ffmpeg-LICENSE.txt ffmpeg-README.txt ffmpeg-build-info.json; do
    install -m 0644 "$helper/$name" "$(dirname "$output")/$name"
  done
  exit 0
fi

if [[ -x "$output" ]]; then
  actual="$(shasum -a 256 "$output" | awk '{print $1}')"
  if [[ "$actual" == "$expected" ]]; then
    exit 0
  fi
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

curl -fL --retry 3 --connect-timeout 10 -o "$work/ffmpeg.zip" "$url"
unzip -q "$work/ffmpeg.zip" ffmpeg -d "$work"

actual="$(shasum -a 256 "$work/ffmpeg" | awk '{print $1}')"
if [[ "$actual" != "$expected" ]]; then
  echo "ffmpeg checksum mismatch: expected $expected, got $actual" >&2
  exit 1
fi

mkdir -p "$(dirname "$output")"
install -m 0755 "$work/ffmpeg" "$output"
# Full builds have a different license and must not inherit minimal metadata.
rm -f "$(dirname "$output")/ffmpeg-build-info.json" "$(dirname "$output")/ffmpeg-LICENSE.txt" "$(dirname "$output")/ffmpeg-README.txt"
