//go:build windows

package session

import (
	"io"
	"os"
)

// AgentPipe is the named pipe of Windows' OpenSSH Authentication Agent
// service. Pageant is not supported.
const AgentPipe = `\\.\pipe\openssh-ssh-agent`

func dialAgent() (io.ReadWriteCloser, error) {
	return os.OpenFile(AgentPipe, os.O_RDWR, 0)
}
