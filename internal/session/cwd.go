package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path"
	"strings"

	"golang.org/x/crypto/ssh"
)

// TagEnv is the variable that marks a terminal's shell on its server,
// set with the "env" request before the shell starts. OpenSSH takes LC_*
// variables in the default configuration of most distributions.
const TagEnv = "LC_SSHYARD_TERM"

// ErrNoCwd is ShellCwd finding no shell, or no directory for it.
var ErrNoCwd = errors.New("session: the shell's directory could not be read")

func newTag() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Tag is the mark of the session's shell (TagEnv), and whether the server
// took it for the shell running now.
func (s *Session) Tag() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tag, s.tagged && s.shell != nil
}

// cwdScript prints the directory of the shell whose environment holds
// tag, from Linux's /proc: of the processes holding it, the one whose
// parent does not (sshd), as the programs the shell runs inherit it. It
// runs under sh, whatever the user's login shell is.
func cwdScript(tag string) string {
	return `sh -c 'p=/proc; t="` + TagEnv + `=` + tag + `"; ` +
		`for f in $(grep -lzx "$t" $p/[0-9]*/environ 2>/dev/null); do ` +
		`d=${f%/environ}; pp=$(sed "s/.*) //" "$d/stat" | cut -d" " -f2); ` +
		`grep -qzx "$t" "$p/$pp/environ" 2>/dev/null || { readlink "$d/cwd"; exit 0; }; ` +
		`done; exit 1'`
}

// ShellCwd asks the server over client which directory the shell tagged
// tag is in, for a shell that does not report it (OSC 7). It reads
// Linux's /proc, so it fails on other servers, as for a shell the server
// did not tag (ErrNoCwd).
func ShellCwd(ctx context.Context, client *ssh.Client, tag string) (string, error) {
	var out bytes.Buffer
	if err := Exec(ctx, client, cwdScript(tag), &out, nil); err != nil {
		var exit *ssh.ExitError
		if errors.As(err, &exit) {
			return "", ErrNoCwd
		}
		return "", err
	}
	d := strings.TrimSpace(out.String())
	if !path.IsAbs(d) || strings.ContainsAny(d, "\n\x00") {
		return "", ErrNoCwd
	}
	return path.Clean(d), nil
}
