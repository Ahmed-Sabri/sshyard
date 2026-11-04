#!/usr/bin/env python3
"""ZIP MyGo's Windows output, excluding its installer and update artifacts."""

import fnmatch
import json
from pathlib import Path
import sys
import zipfile


def package(build_dir, version):
    if not (build_dir / "SSHYard.exe").is_file():
        raise FileNotFoundError(f"Missing MyGo executable: {build_dir / 'SSHYard.exe'}")
    output = build_dir / f"sshyard-{version}-windows-amd64-portable.zip"
    artifacts = (
        "sshyard-setup-*.exe",
        "sshyard-*-windows-*.tar.gz",
        "sshyard-*-windows-*.delta",
        "sshyard-*-windows-*-portable.zip",
        "update-windows-*.json",
        "install.ps1",
    )
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as archive:
        for path in sorted(build_dir.rglob("*")):
            relative = path.relative_to(build_dir)
            if len(relative.parts) == 1 and any(
                fnmatch.fnmatchcase(path.name, pattern) for pattern in artifacts
            ):
                continue
            if path.is_symlink():
                raise ValueError(f"Unsupported symlink in MyGo output: {relative}")
            archive.write(path, relative.as_posix())
    return output


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("usage: windows-portable.py <windows-amd64-build-dir>")
    config = json.loads(Path("mygo.json").read_text(encoding="utf-8"))
    print(package(Path(sys.argv[1]), config["version"]))
