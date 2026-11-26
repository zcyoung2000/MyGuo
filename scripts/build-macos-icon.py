#!/usr/bin/env python3
"""Compile the macOS icon once with Xcode 26+ for Xcode-independent packaging."""
import json
from pathlib import Path
import shutil
import subprocess
import tempfile


def main():
    root = Path(__file__).resolve().parent.parent
    config = json.loads((root / "mygo.json").read_text())
    with tempfile.TemporaryDirectory(prefix="myguo-appicon-") as staging:
        out = Path(staging)
        subprocess.run([
            "xcrun", "actool", str(root / "resources/AppIcon.icon"),
            "--compile", str(out), "--output-format", "human-readable-text",
            "--platform", "macosx", "--target-device", "mac",
            "--minimum-deployment-target", config["macos"]["minimumSystemVersion"],
            "--app-icon", "AppIcon", "--output-partial-info-plist", str(out / "partial.plist"),
        ], check=True)
        # actool may return success without producing either icon on old Xcode.
        for name in ("Assets.car", "AppIcon.icns"):
            if not (out / name).is_file() or (out / name).stat().st_size == 0:
                raise RuntimeError(f"actool did not produce {name}; use Xcode 26 or later")
        catalog = root / config["macos"]["icon"]
        fallback = root / "resources/darwin" / config["macos"]["infoPlist"]["CFBundleIconFile"]
        catalog.parent.mkdir(parents=True, exist_ok=True)
        fallback.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(out / "Assets.car", catalog)
        shutil.copyfile(out / "AppIcon.icns", fallback)
        print(f"Updated {catalog.relative_to(root)} and {fallback.relative_to(root)}")


if __name__ == "__main__":
    main()
