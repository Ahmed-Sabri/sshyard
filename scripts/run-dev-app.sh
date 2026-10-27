#!/bin/sh
# Runs SSHYard with `mygo dev`. On macOS the development app is signed with
# the local code signing identity "SSHYard-Dev" (a self-signed certificate
# in the login keychain), so every rebuild has the same signature and the
# keychain's "Always Allow" lasts across rebuilds; signed ad hoc, each
# rebuild is a new app to the keychain and asks again.
#
# SSHYARD_DEV_IDENTITY picks another identity. Arguments go to `mygo dev`.
set -eu

cd "$(dirname "$0")/.."

if [ "$(uname -s)" != Darwin ]; then
	exec go tool mygo dev "$@"
fi

identity="${SSHYARD_DEV_IDENTITY:-SSHYard-Dev}"
if ! security find-identity -v -p codesigning | grep -q "\"$identity\""; then
	echo "run-dev-app: no valid code signing identity \"$identity\" in the keychain." >&2
	echo "Create a self-signed code signing certificate of that name in the login keychain and trust it for code signing." >&2
	exit 1
fi
exec go tool mygo dev -sign "$identity" "$@"
