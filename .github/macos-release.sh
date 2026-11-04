#!/usr/bin/env bash
# GitHub macOS runners only; prepare credentials/config or verify MyGo output.
set -euo pipefail

# find-identity -v reports valid identities only; require one Developer ID app certificate.
select_signing_identity() {
  sed -nE 's/^[[:space:]]*[0-9]+\) [[:xdigit:]]{40} "(Developer ID Application: [^"]+)"[[:space:]]*$/\1/p' |
    jq -Rrse 'split("\n") | map(select(length > 0)) |
      if length == 1 then .[0]
      else error("Expected exactly one valid Developer ID Application identity, found \(length)") end'
}

# Allow credential-free parser checks without running a release action.
if [[ "${BASH_SOURCE[0]}" != "$0" ]]; then return; fi

case "${1:-}" in
  prepare)
    for name in MACOS_CERTIFICATE_P12_BASE64 MACOS_CERTIFICATE_PASSWORD APPLE_API_KEY_P8 APPLE_API_KEY_ID APPLE_API_ISSUER_ID; do
      if [ -z "${!name:-}" ]; then
        echo "::error::Required secret $name is not set"
        exit 1
      fi
    done
    credential_dir="$RUNNER_TEMP/sshyard-signing"
    mkdir -m 700 "$credential_dir"
    umask 077
    keychain="$credential_dir/signing.keychain-db"
    password="$(openssl rand -base64 32)"
    echo "::add-mask::$password"
    printf '%s' "$MACOS_CERTIFICATE_P12_BASE64" | base64 --decode > "$credential_dir/certificate.p12"
    printf '%s' "$APPLE_API_KEY_P8" | base64 --decode > "$credential_dir/AuthKey.p8"
    security create-keychain -p "$password" "$keychain"
    security set-keychain-settings -lut 21600 "$keychain"
    security unlock-keychain -p "$password" "$keychain"
    security import "$credential_dir/certificate.p12" -k "$keychain" -P "$MACOS_CERTIFICATE_PASSWORD" -T /usr/bin/codesign
    security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$password" "$keychain" > /dev/null
    # Preserve the runner's existing keychains, including Apple trust roots.
    security list-keychains -d user > "$credential_dir/keychains.txt"
    existing=()
    while IFS= read -r line; do
      line="${line#*\"}"
      existing+=("${line%\"*}")
    done < "$credential_dir/keychains.txt"
    security list-keychains -d user -s "$keychain" "${existing[@]}"
    identity="$(security find-identity -v -p codesigning "$keychain" | select_signing_identity)"
    xcrun notarytool store-credentials sshyard-release --key "$credential_dir/AuthKey.p8" \
      --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER_ID" --keychain "$keychain"
    jq --arg identity "$identity" --arg keychain "$keychain" \
      '.macos.signingIdentity = $identity | .macos.notarize = {keychainProfile: "sshyard-release", keychain: $keychain}' \
      mygo.json > "$credential_dir/mygo.json"
    mv "$credential_dir/mygo.json" mygo.json
    rm "$credential_dir/certificate.p12" "$credential_dir/AuthKey.p8"
    ;;
  verify)
    identity="$(jq -er '.macos.signingIdentity | select(type == "string" and startswith("Developer ID Application: "))' mygo.json)"
    dir="build/darwin-${2:?Expected architecture}"
    app="$dir/SSHYard.app"
    shopt -s nullglob
    images=("$dir"/*.dmg)
    [ "${#images[@]}" -eq 1 ] || { echo "::error::Expected exactly one DMG in $dir"; exit 1; }
    codesign --verify --deep --strict --verbose=2 "$app"
    details="$(codesign --display --verbose=4 "$app" 2>&1)"
    printf '%s\n' "$details" | grep -Fx "Authority=$identity"
    printf '%s\n' "$details" | grep -E 'flags=.*\(.*runtime.*\)'
    xcrun stapler validate "$app"
    spctl --assess --type execute --verbose=2 "$app"
    codesign --verify --strict --verbose=2 "${images[0]}"
    xcrun stapler validate "${images[0]}"
    spctl --assess --type open --context context:primary-signature --verbose=2 "${images[0]}"
    ;;
  cleanup)
    credential_dir="$RUNNER_TEMP/sshyard-signing"
    status=0
    if [ -f "$credential_dir/signing.keychain-db" ]; then
      security delete-keychain "$credential_dir/signing.keychain-db" || status=$?
    fi
    rm -rf "$credential_dir"
    exit "$status"
    ;;
  *) echo "Usage: $0 prepare|verify <arch>|cleanup" >&2; exit 2 ;;
esac
