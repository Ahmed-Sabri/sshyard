package acp

import (
	"context"
	"errors"
	"io"

	"github.com/Licoy/sshyard/internal/sftpsvc"
)

var (
	// ErrProtocolVersion means the agent did not negotiate protocol version 1.
	ErrProtocolVersion = errors.New("agent protocol version is not 1")
	// ErrUnsupportedAuth means the agent offered no agent-type login.
	ErrUnsupportedAuth = errors.New("unsupported authentication method")

	errNotConnected = errors.New("remote files are not connected yet")
	errExec         = errors.New("remote exec is not connected yet")
	errSession      = errors.New("unknown session")
	errTerminal     = errors.New("unknown terminal")
	errTooMany      = errors.New("too many terminals")
)

// Files is the remote file seam. A1's *remote.Files implements it:
// Read and Write use sftpsvc.Version, and a changed file returns
// sftpsvc.ErrChanged. Nil means the host is not connected yet; the gate
// still runs, and the call then fails.
type Files interface {
	ResolvePath(context.Context, string) (string, error)
	ReadContext(context.Context, string, int64) ([]byte, sftpsvc.Version, error)
	Read(path string) ([]byte, sftpsvc.Version, error)
	Write(path string, data []byte, was sftpsvc.Version) (sftpsvc.Version, error)
}

// EnvRunner is an optional extra on policy.Exec. That interface has no
// environment, and A1's runner does not implement this. When Exec does
// not, a terminal's LANG / LC_* / TERM variables are dropped.
type EnvRunner interface {
	RunEnv(ctx context.Context, cmd, dir string, env []string, stdout, stderr io.Writer) (exit int, err error)
}
