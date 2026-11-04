#!/usr/bin/env bash
# Prints the download section appended to the GitHub release notes. Links
# are built from the final asset names, and a missing asset fails instead
# of publishing a dead link. Usage: release-download-notes.sh dist tag owner/repo
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 <dist-dir> <tag> <owner/repo>" >&2
  exit 2
fi
dist="$1"
base="https://github.com/$3/releases/download/$2"

# link <label> <glob>: Markdown link to the single asset matching glob
link() {
  local matches=()
  local path
  for path in "$dist"/$2; do
    [[ -e "$path" ]] && matches+=("${path##*/}")
  done
  if [[ ${#matches[@]} -ne 1 ]]; then
    echo "expected one asset matching $2, found ${#matches[@]}" >&2
    return 1
  fi
  printf '[%s](%s/%s)' "$1" "$base" "${matches[0]}"
}

# One assignment per link: in "$(a) · $(b)" only b's status reaches set -e
mac_arm_dmg="$(link DMG 'sshyard-*-arm64.dmg')"
mac_x64_dmg="$(link DMG 'sshyard-*-amd64.dmg')"
win_x64_setup="$(link Installer 'sshyard-setup-*-amd64.exe')"
linux_x64_deb="$(link .deb 'sshyard_*_amd64.deb')"
linux_x64_tar="$(link .tar.gz 'sshyard-*-linux-amd64.tar.gz')"
install_sh="$(link install.sh 'install.sh')"
checksums="$(link SHA256SUMS 'SHA256SUMS')"

cat <<EOF

---

## Downloads

### macOS

macOS 12 or later, signed and notarized.

| Architecture | Download |
| --- | --- |
| Apple Silicon | $mac_arm_dmg |
| Intel | $mac_x64_dmg |

### Windows

| Architecture | Download |
| --- | --- |
| x64 | $win_x64_setup |

The installer installs for the current user into \`%LOCALAPPDATA%\\Programs\` and supports in-app updates; no administrator rights needed.

### Linux

| Architecture | Download |
| --- | --- |
| x64 (amd64) | $linux_x64_deb · $linux_x64_tar |

Install the .deb with your package manager, or put $install_sh next to the .tar.gz and run \`sh install.sh\` to install into \`~/.local\`.

Verify downloads with $checksums.
EOF
