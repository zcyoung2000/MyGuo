#!/usr/bin/env python3
"""Build all installers and signed updates, with no private keys in the repo."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--key-file", type=Path, default=Path.home() / "Library/Application Support/myguo-release/update-keys/mygo-update.key")
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    os.chdir(root)
    config = json.loads((root / "mygo.json").read_text())
    env = os.environ.copy()
    if not env.get("MYGO_UPDATER_PRIVATE_KEY"):
        if not args.key_file.is_file():
            parser.error("Set MYGO_UPDATER_PRIVATE_KEY or supply --key-file with the existing signing key.")
        env["MYGO_UPDATER_PRIVATE_KEY"] = args.key_file.read_text().strip()
    env["CGO_ENABLED"] = "0"

    def run(*command):
        subprocess.run(command, env=env, check=True)

    for arch in ("arm64", "amd64"):
        run("./scripts/fetch-ffmpeg.sh", f"darwin/{arch}")
        run("go", "run", "github.com/egoist/mygo/cmd/mygo", "build", "-platform", f"darwin/{arch}")
    # Build-windows verifies its minimal helper before using it.
    run("go", "run", "./scripts/build-windows.go", "-ffmpeg-dir", "./resources/windows-amd64")

    dist = root / config.get("out", "dist")
    output = dist / f"release-v{config['version']}"
    # A failed build never reaches this point. Recreate staging so files from
    # a previous attempt cannot leak into a release.
    if output.exists():
        shutil.rmtree(output)
    output.mkdir()
    for target in ("darwin-arm64", "darwin-amd64", "windows-amd64"):
        for pattern in ("*.dmg", "*-setup-*.exe", "*.tar.gz", "*.delta", "update-*.json"):
            for source in (dist / target).glob(pattern):
                shutil.copy2(source, output / source.name)
    run("go", "run", "./scripts/verify-release.go", "-dir", str(output))
    lines = [f"{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}" for p in sorted(output.iterdir())]
    (output / "SHA256SUMS").write_text("\n".join(lines) + "\n")
    print(f"\nRelease assets: {output}")


if __name__ == "__main__":
    try:
        main()
    except subprocess.CalledProcessError as error:
        sys.exit(error.returncode)
