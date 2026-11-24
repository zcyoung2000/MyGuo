#!/usr/bin/env python3
"""Validate the pinned minimal helper before reusing generated resources."""
import hashlib
import json
import os
from pathlib import Path
import sys


def verify(directory, target):
    root = Path(directory)
    info = json.loads((root / "ffmpeg-build-info.json").read_text())
    expected = {
        "Format": "myguo-ffmpeg-minimal-v1",
        "Version": "9.0.2",
        "Target": target,
        "SourceSHA256": "8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e",
        "License": "LGPL-2.1-or-later",
    }
    if target.startswith("darwin/"):
        expected["DeploymentTarget"] = "13.0"
    for key, value in expected.items():
        if info.get(key) != value:
            raise ValueError(f"unexpected {key}")
    binary = "ffmpeg.exe" if target.startswith("windows/") else "ffmpeg"
    names = {binary, "ffmpeg-LICENSE.txt", "ffmpeg-README.txt"}
    if set(info["Files"]) != names:
        raise ValueError("unexpected resource files")
    for name in names:
        if hashlib.sha256((root / name).read_bytes()).hexdigest() != info["Files"][name]:
            raise ValueError(f"checksum mismatch: {name}")
    if target.startswith("darwin/") and not os.access(root / binary, os.X_OK):
        raise ValueError("ffmpeg is not executable")


if __name__ == "__main__":
    try:
        verify(sys.argv[1], sys.argv[2])
    except (OSError, ValueError, KeyError, TypeError, IndexError) as error:
        print(f"Minimal FFmpeg validation failed: {error}", file=sys.stderr)
        sys.exit(1)
