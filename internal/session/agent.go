package session

import (
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// ErrAgentUnavailable reports that no SSH agent answers: SSH_AUTH_SOCK is
// unset or dead (macOS, Linux), or the OpenSSH agent service is not
// running (Windows).
var ErrAgentUnavailable = errors.New("session: no SSH agent is running")

// ErrAgentEmpty reports an agent that holds no keys.
var ErrAgentEmpty = errors.New("session: the SSH agent has no keys")

// DialAgent connects to the system's SSH agent; a var for tests.
var DialAgent = dialAgent

// agentAuth signs with the agent's keys. The keys never leave the agent:
// SSHYard neither reads nor caches them. The connection to the agent is
// closed with closer once the handshake is done.
func agentAuth() (ssh.AuthMethod, io.Closer, error) {
	rw, err := DialAgent()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrAgentUnavailable, err)
	}
	ag := agent.NewClient(rw)
	signers, err := ag.Signers()
	if err != nil {
		rw.Close()
		return nil, nil, fmt.Errorf("%w: %v", ErrAgentUnavailable, err)
	}
	if len(signers) == 0 {
		rw.Close()
		return nil, nil, ErrAgentEmpty
	}
	return ssh.PublicKeys(signers...), rw, nil
}
