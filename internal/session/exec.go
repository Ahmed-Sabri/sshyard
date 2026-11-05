package session

import (
	"context"
	"errors"
	"io"

	"golang.org/x/crypto/ssh"
)

// Exec runs command on an already-authenticated client. It never dials
// and does not take a pool reference. stdout and stderr receive the
// remote output as it arrives; a nil writer discards that stream.
// Cancelling ctx closes the channel. Exec waits for the remote exit
// status: a non-zero status is *ssh.ExitError, and a cancel with no
// status returns ctx.Err().
func Exec(ctx context.Context, client *ssh.Client, command string, stdout, stderr io.Writer) error {
	return ExecStarted(ctx, client, command, stdout, stderr, nil)
}

// ExecStarted is Exec. started, when set, is called after the command
// starts and must not block. The session stays open until ExecStarted
// returns; the callback may Signal it until then.
func ExecStarted(ctx context.Context, client *ssh.Client, command string, stdout, stderr io.Writer, started func(*ssh.Session)) error {
	return execStarted(ctx, client, command, execStreams{stdout: stdout, stderr: stderr}, started)
}

type execStreams struct {
	stdin          io.Reader
	stdout, stderr io.Writer
}

// execStarted configures all streams before Start begins copying them.
func execStarted(ctx context.Context, client *ssh.Client, command string, streams execStreams, started func(*ssh.Session)) error {
	if client == nil {
		return errors.New("session: no client")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	sess.Stdin, sess.Stdout, sess.Stderr = streams.stdin, streams.stdout, streams.stderr
	if err := sess.Start(command); err != nil {
		return err
	}
	if started != nil {
		started(sess)
	}
	waited := make(chan error, 1)
	go func() { waited <- sess.Wait() }()
	select {
	case err := <-waited:
		return err
	case <-ctx.Done():
		sess.Close()
		err := <-waited
		var exit *ssh.ExitError
		if errors.As(err, &exit) {
			return err
		}
		return ctx.Err()
	}
}
