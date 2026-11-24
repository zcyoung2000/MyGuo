#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="$ROOT_DIR/mygo.json"

CONFIG="$(python3 -c '
import json, pathlib, sys
data = json.loads(pathlib.Path(sys.argv[1]).read_text())
print(data.get("name", ""), data.get("out", "dist"), sep="\n")
' "$CONFIG_FILE")"

APP_NAME="$(sed -n '1p' <<<"$CONFIG")"
OUTPUT_DIR="$(sed -n '2p' <<<"$CONFIG")"

if [[ -z "$APP_NAME" ]]; then
  echo "error: app name is missing from mygo.json" >&2
  exit 1
fi

cd "$ROOT_DIR"

BUILD_ARCH="$(go env GOARCH)"
echo "==> Preparing minimal FFmpeg (darwin/$BUILD_ARCH)"
"$ROOT_DIR/scripts/fetch-ffmpeg.sh" "darwin/$BUILD_ARCH"

echo "==> Building $APP_NAME (CGO_ENABLED=0)"
CGO_ENABLED=0 go run github.com/egoist/mygo/cmd/mygo build -platform "darwin/$BUILD_ARCH" -skip-dmg

BUILT_APP="$ROOT_DIR/$OUTPUT_DIR/darwin-$BUILD_ARCH/$APP_NAME.app"
INSTALL_APP="/Applications/$APP_NAME.app"
EXISTING_APP="$(python3 -c '
import os, pathlib, sys
target = pathlib.Path(sys.argv[1])
if target.exists():
    for entry in target.parent.iterdir():
        if entry.name.casefold() == target.name.casefold() and os.path.samefile(entry, target):
            print(entry)
            break
' "$INSTALL_APP")"

if [[ ! -d "$BUILT_APP" ]]; then
  echo "error: built app not found: $BUILT_APP" >&2
  exit 1
fi

echo "==> Verifying built app"
# Signing changes the helper's bytes, so its manifest is validated before
# packaging. The signed bundle seal verifies the installed resources.
[[ -x "$BUILT_APP/Contents/Resources/ffmpeg" ]]
[[ -f "$BUILT_APP/Contents/Resources/ffmpeg-build-info.json" ]]
codesign --verify --deep --strict "$BUILT_APP"

if [[ -n "$EXISTING_APP" ]]; then
  BACKUP_DIR="$HOME/Library/Application Support/myguo-backups/$(date +%Y%m%d-%H%M%S)"
  mkdir -p "$BACKUP_DIR"
  ditto "$EXISTING_APP" "$BACKUP_DIR/$APP_NAME.app"
  echo "==> Previous app saved to $BACKUP_DIR/$APP_NAME.app"
fi

echo "==> Stopping $APP_NAME"
PLIST="${EXISTING_APP:-$BUILT_APP}/Contents/Info.plist"
EXECUTABLE="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$PLIST")"
PROCESS_PATH="${EXISTING_APP:-$INSTALL_APP}/Contents/MacOS/$EXECUTABLE"
PIDS="$(pgrep -f "$PROCESS_PATH" || true)"

if [[ -n "$PIDS" ]]; then
  kill $PIDS
  for _ in {1..20}; do
    PIDS="$(pgrep -f "$PROCESS_PATH" || true)"
    [[ -z "$PIDS" ]] && break
    sleep 0.1
  done

  PIDS="$(pgrep -f "$PROCESS_PATH" || true)"
  if [[ -n "$PIDS" ]]; then
    kill -9 $PIDS
  fi
fi

echo "==> Installing to /Applications"
# A case-insensitive filesystem keeps the old directory spelling unless it
# moves through a temporary name. The previous bundle is already backed up.
if [[ -n "$EXISTING_APP" && "$EXISTING_APP" != "$INSTALL_APP" ]]; then
  RENAME_DIR="$(mktemp -d /Applications/.myguo-rename.XXXXXX)"
  mv "$EXISTING_APP" "$RENAME_DIR/app"
  mv "$RENAME_DIR/app" "$INSTALL_APP"
  rmdir "$RENAME_DIR"
fi
# rsync removes resources left by previous installs; ditto only overlays files.
rsync -a --delete "$BUILT_APP/" "$INSTALL_APP/"
codesign --verify --deep --strict "$INSTALL_APP"

# Refresh this app's registration without restarting Finder or Dock.
touch "$INSTALL_APP" "$INSTALL_APP/Contents/Info.plist"
LSREGISTER="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
if [[ -x "$LSREGISTER" ]]; then
  "$LSREGISTER" -f "$INSTALL_APP" >/dev/null 2>&1 || true
fi

echo "==> Opening $APP_NAME"
open "$INSTALL_APP"

echo "==> Done"
