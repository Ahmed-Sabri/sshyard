#!/usr/bin/env bash
# Credential-free failure-path checks; never signs or contacts Apple/GitHub.
set -euo pipefail
script="$(cd "$(dirname "$0")" && pwd)/macos-release.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

if env -i PATH="$PATH" bash "$script" prepare > "$work/output" 2>&1; then
  echo 'Missing secrets unexpectedly accepted' >&2
  exit 1
fi
grep -F 'Required secret MACOS_CERTIFICATE_P12_BASE64 is not set' "$work/output"
# Exercise only identity parsing; no successful keychain import/sign/notary mocks.
source "$script"
identity='Developer ID Application: Fixture (TEAM123456)'
valid="  1) 0123456789ABCDEF0123456789ABCDEF01234567 \"$identity\""
[ "$(printf '%s\n' "$valid" '     1 valid identities found' | select_signing_identity)" = "$identity" ]
for fixture in '     0 valid identities found' "$valid
$valid" '  1) 0123456789ABCDEF0123456789ABCDEF01234567 "Apple Development: Fixture"'; do
  if printf '%s\n' "$fixture" | select_signing_identity > "$work/output" 2>&1; then
    echo 'Ambiguous or missing Developer ID identity unexpectedly accepted' >&2
    exit 1
  fi
  grep -F 'Expected exactly one valid Developer ID Application identity' "$work/output"
done

# Simulate keychain deletion failure; raw files must still be removed and error preserved.
mkdir -p "$work/bin" "$work/sshyard-signing"
printf '#!/bin/sh\necho "fixture: keychain deletion failed" >&2\nexit 9\n' > "$work/bin/security"
chmod +x "$work/bin/security"
touch "$work/sshyard-signing/signing.keychain-db" "$work/sshyard-signing/certificate.p12" "$work/sshyard-signing/AuthKey.p8"
status=0
PATH="$work/bin:$PATH" RUNNER_TEMP="$work" bash "$script" cleanup > "$work/output" 2>&1 || status=$?
[ "$status" -eq 9 ]
[ ! -d "$work/sshyard-signing" ]
grep -F 'fixture: keychain deletion failed' "$work/output"
echo 'macOS release failure-path checks passed'
