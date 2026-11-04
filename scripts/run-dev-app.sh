#!/bin/sh
# Runs SSHYard with `mygo dev`. On macOS, prefer an Apple Development
# identity with a stable team ID, which can reduce keychain authorization
# prompts. Self-signed identities may be identified by each build's hash;
# using the same certificate does not guarantee access across rebuilds.
# macOS keychain policy may still require authorization with either identity.
#
# `mygo dev` rebuilds and relaunches the app whenever a source file changes.
# Unless DEV_WATCH=1, it builds a snapshot of the working tree instead, so
# editing files leaves the running app alone; run the script again to pick
# the changes up.
#
# MACOS_DEV_IDENTITY picks another identity. Arguments go to `mygo dev`.
set -eu

cd "$(dirname "$0")/.."

if [ "${DEV_WATCH:-}" != 1 ]; then
	snapshot="${TMPDIR:-/tmp}/sshyard-dev"
	rm -rf "$snapshot"
	mkdir -p "$snapshot"
	git ls-files -z -co --exclude-standard | tar -cf - --null -T - | tar -xf - -C "$snapshot"
	cd "$snapshot"
fi

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
