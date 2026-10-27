#!/usr/bin/env bash
# Extra archives next to what `mygo build` makes: the macOS .app zipped, and
# the Windows .exe zipped (portable). Usage: package-extras.sh goos goarch version
set -euo pipefail
goos=$1 goarch=$2 version=$3
dir="build/$goos-$goarch"
case "$goos" in
  darwin)
    ditto -c -k --sequesterRsrc --keepParent "$dir/SSHYard.app" "$dir/SSHYard-$version-$goos-$goarch.app.zip"
    ;;
  windows)
    (cd "$dir" && 7z a -tzip "SSHYard-$version-$goos-$goarch-portable.zip" SSHYard.exe >/dev/null)
    ;;
esac
