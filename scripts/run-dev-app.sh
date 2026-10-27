#!/bin/sh
# Runs SSHYard with `mygo dev`. On macOS the development app is code signed
# so the keychain can remember "Always Allow".
#
# The keychain also checks a partition ID, which securityd derives from the
# signature: "teamid:<team>" for an Apple Development (or Developer ID)
# certificate, but "cdhash:<hash of this build>" for anything else, such as a
# self-signed certificate. Only a team ID survives a rebuild, so an
# "Apple Development" identity (a free Apple ID in Xcode > Settings > Accounts
# is enough) is used when there is one; with the self-signed "SSHYard-Dev"
# the keychain asks again after every rebuild.
#
# SSHYARD_DEV_IDENTITY picks another identity. Arguments go to `mygo dev`.
set -eu

cd "$(dirname "$0")/.."

if [ "$(uname -s)" != Darwin ]; then
	exec go tool mygo dev "$@"
fi

identities="$(security find-identity -v -p codesigning)"
identity="${SSHYARD_DEV_IDENTITY:-}"
if [ -z "$identity" ]; then
	identity="$(printf '%s\n' "$identities" | sed -n 's/^ *[0-9]*) [0-9A-F]* "\(Apple Development: [^"]*\)"$/\1/p' | head -n 1)"
fi
if [ -z "$identity" ]; then
	identity=SSHYard-Dev
	echo "run-dev-app: no \"Apple Development\" identity; signing with the self-signed \"$identity\", so the keychain asks again after every rebuild." >&2
fi
if ! printf '%s\n' "$identities" | grep -qF "\"$identity\""; then
	echo "run-dev-app: no valid code signing identity \"$identity\" in the keychain." >&2
	echo "Add an Apple Development certificate in Xcode > Settings > Accounts, or create a self-signed code signing certificate of that name in the login keychain." >&2
	exit 1
fi
exec go tool mygo dev -sign "$identity" "$@"
