#!/bin/sh
# Runs SSHYard with `mygo dev`. On macOS, prefer an Apple Development
# identity with a stable team ID, which can reduce keychain authorization
# prompts. Self-signed identities may be identified by each build's hash;
# using the same certificate does not guarantee access across rebuilds.
# macOS keychain policy may still require authorization with either identity.
#
# MACOS_DEV_IDENTITY picks another identity. Arguments go to `mygo dev`.
set -eu

cd "$(dirname "$0")/.."

if [ "$(uname -s)" != Darwin ]; then
	exec go tool mygo dev "$@"
fi

identities="$(security find-identity -v -p codesigning)"
identity="${MACOS_DEV_IDENTITY:-}"
if [ -z "$identity" ]; then
	identity="$(printf '%s\n' "$identities" | sed -n 's/^ *[0-9]*) [0-9A-F]* "\(Apple Development: [^"]*\)"$/\1/p' | head -n 1)"
fi
if [ -z "$identity" ]; then
	identity=SSHYard-Dev
	echo "run-dev-app: no \"Apple Development\" identity; signing with the self-signed \"$identity\", so macOS may request keychain access again after a rebuild." >&2
fi
if ! printf '%s\n' "$identities" | grep -qF "\"$identity\""; then
	echo "run-dev-app: no valid code signing identity \"$identity\" in the keychain." >&2
	echo "Add an Apple Development certificate in Xcode > Settings > Accounts, or create a self-signed code signing certificate of that name in the login keychain." >&2
	exit 1
fi
exec go tool mygo dev -sign "$identity" "$@"
