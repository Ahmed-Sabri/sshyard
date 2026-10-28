package acp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/sftpsvc"
	sdk "github.com/coder/acp-go-sdk"
)

type cancellingFiles struct {
	memFiles
	operation string
	started   chan struct{}
	finished  chan struct{}
}

func (f *cancellingFiles) wait(ctx context.Context) error {
	close(f.started)
	<-ctx.Done()
	close(f.finished)
	return ctx.Err()
}
func (f *cancellingFiles) ReadContext(ctx context.Context, p string, limit int64) ([]byte, sftpsvc.Version, error) {
	if f.operation == "read" {
		return nil, sftpsvc.Version{}, f.wait(ctx)
	}
	return f.memFiles.ReadContext(ctx, p, limit)
}
func (f *cancellingFiles) WriteContext(ctx context.Context, p string, data []byte, was sftpsvc.Version) (sftpsvc.Version, error) {
	return sftpsvc.Version{}, f.wait(ctx)
}

func TestPromptCancellationNotifiesAndDrainsFileCalls(t *testing.T) {
	for _, operation := range []string{"read", "write"} {
		t.Run(operation, func(t *testing.T) {
			files := &cancellingFiles{operation: operation, started: make(chan struct{}), finished: make(chan struct{})}
			files.seed("/home/me/note", "original")
			gate := &fixedGate{run: true}
			script := "---\n" + operation + " @sandbox/note"
			if operation == "write" {
				script += " updated"
			}
			s := mustStart(t, script+"\n", Config{Gate: gate, Files: files})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := s.Prompt(ctx, "go", nil); done <- err }()
			select {
			case <-files.started:
			case <-time.After(3 * time.Second):
				t.Fatal("file request did not start")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Prompt did not stop")
			}
			select {
			case <-files.finished:
			default:
				t.Fatal("Prompt returned before file cleanup")
			}
			if !strings.Contains(waitDiag(t, s, "cancel-notification"), "cancel-notification") {
				t.Fatal(s.Diagnostics())
			}
			_, _, dones := gate.snapshot()
			if len(dones) == 0 || dones[len(dones)-1].exit >= 0 {
				t.Fatalf("cancel audit %+v", dones)
			}
		})
	}
}

func TestRedactionOnlyAtExternalTextBoundary(t *testing.T) {
	files := &memFiles{}
	files.seed("/home/me/note", "password=example-old\nordinary\n")
	gate := &fixedGate{run: true}
	s := mustStart(t, "---\nread @sandbox/note\nwrite @sandbox/note ordinary-new\nterm echo hi\n", Config{Gate: gate, Files: files, Exec: &fakeExec{out: "token=example-output\nordinary"}, Redact: true})
	if _, err := s.Prompt(t.Context(), "password=example-prompt", []Resource{{URI: "file:///context", Text: "password=example-resource"}}); err != nil {
		t.Fatal(err)
	}
	d := waitDiag(t, s, "term-after")
	for _, secret := range []string{"example-old", "example-output", "example-prompt", "example-resource"} {
		if strings.Contains(d, secret) {
			t.Fatalf("external agent received %q: %s", secret, d)
		}
	}
	if !strings.Contains(d, "[REDACTED]") || !strings.Contains(d, "ordinary") {
		t.Fatal(d)
	}
	_, diffs, _ := gate.snapshot()
	if len(diffs) < 3 || !strings.Contains(diffs[2], "password=example-old") || strings.Contains(diffs[2], "[REDACTED]") {
		t.Fatalf("diff lost original content: %q", diffs)
	}
	got, _ := files.get("/home/me/note")
	if got != "ordinary-new" {
		t.Fatalf("write changed bytes: %q", got)
	}
}

func TestStoppedAgentCannotStartLateTools(t *testing.T) {
	files := &memFiles{}
	gate := &fixedGate{run: true}
	ex := &fakeExec{}
	s := mustStart(t, "---\nignore-cancel\n", Config{Gate: gate, Files: files, Exec: ex})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Prompt(ctx, "go", nil); done <- err }()
	if !strings.Contains(waitDiag(t, s, "ready"), "ready") {
		t.Fatal(s.Diagnostics())
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("prompt cancellation hung")
	}
	if !strings.Contains(waitDiag(t, s, "term-after-cancel"), "term-after-cancel") {
		t.Fatal(s.Diagnostics())
	}
	if _, err := s.Prompt(t.Context(), "new turn", nil); !errors.Is(err, ErrInterruptedSession) {
		t.Fatalf("interrupted session reused: %v", err)
	}
	actions, _, _ := gate.snapshot()
	if len(actions) != 0 {
		t.Fatalf("late tools authorized: %+v", actions)
	}
	if _, err := s.ReadTextFile(t.Context(), sdk.ReadTextFileRequest{SessionId: s.sid, Path: "/home/me/note"}); err == nil {
		t.Fatal("idle read accepted")
	}
	if _, err := s.CreateTerminal(t.Context(), sdk.CreateTerminalRequest{SessionId: s.sid, Command: "echo"}); err == nil {
		t.Fatal("idle terminal accepted")
	}
}

type cleanupExec struct{ started, cancelled, release, finished chan struct{} }

func (e *cleanupExec) Run(ctx context.Context, cmd, dir string, stdout, stderr io.Writer) (int, error) {
	close(e.started)
	<-ctx.Done()
	close(e.cancelled)
	<-e.release
	close(e.finished)
	return -1, ctx.Err()
}
func TestStopAndCloseWaitForTerminalCleanup(t *testing.T) {
	ex := &cleanupExec{make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})}
	s := mustStart(t, "---\nterm-store\nignore-cancel\n", Config{Gate: &fixedGate{run: true}, Exec: ex})
	prompt := make(chan error, 1)
	go func() { _, err := s.Prompt(t.Context(), "go", nil); prompt <- err }()
	select {
	case <-ex.started:
	case <-time.After(3 * time.Second):
		t.Fatal("terminal did not start")
	}
	stopped := make(chan error, 1)
	closed := make(chan error, 1)
	go func() { stopped <- s.Stop() }()
	go func() { closed <- s.Close() }()
	select {
	case <-ex.cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("terminal not cancelled")
	}
	select {
	case <-stopped:
		t.Fatal("Stop returned before remote cleanup")
	default:
	}
	select {
	case <-closed:
		t.Fatal("Close returned before remote cleanup")
	default:
	}
	close(ex.release)
	for _, done := range []chan error{stopped, closed, prompt} {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent shutdown hung")
		}
	}
	select {
	case <-ex.finished:
	default:
		t.Fatal("remote cleanup incomplete")
	}
}

func TestReadSlicesPreserveSensitiveFileLineOffsets(t *testing.T) {
	for name, content := range map[string]string{
		"PEM":               "-----BEGIN PRIVATE KEY-----\nsynthetic-key-line\n-----END PRIVATE KEY-----\nordinary\n",
		"quoted assignment": "password=\"synthetic-first\nsynthetic-second\nsynthetic-third\"\nordinary\n",
	} {
		t.Run(name, func(t *testing.T) {
			files := &memFiles{}
			files.seed("/home/me/note", content)
			s := mustStart(t, "---\nread @sandbox/note 2 1\nread @sandbox/note 4 1\n", Config{Gate: &fixedGate{run: true}, Files: files, Redact: true})
			if _, err := s.Prompt(t.Context(), "read", nil); err != nil {
				t.Fatal(err)
			}
			d := waitDiag(t, s, `read "ordinary"`)
			if strings.Contains(d, "synthetic-") || !strings.Contains(d, `read ""`) || !strings.Contains(d, `read "ordinary"`) {
				t.Fatal(d)
			}
		})
	}
}

func TestPromptWaitsForReleasedTerminalCleanup(t *testing.T) {
	ex := &cleanupExec{make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})}
	s := mustStart(t, "---\nterm-release-running\n", Config{Gate: &fixedGate{run: true}, Exec: ex})
	done := make(chan error, 1)
	go func() { _, err := s.Prompt(t.Context(), "go", nil); done <- err }()
	select {
	case <-ex.cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("released terminal not cancelled")
	}
	if !strings.Contains(waitDiag(t, s, "released-running"), "released-running") {
		t.Fatal(s.Diagnostics())
	}
	select {
	case err := <-done:
		t.Fatalf("Prompt returned before released terminal cleanup: %v", err)
	default:
	}
	close(ex.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Prompt did not finish after cleanup")
	}
}

type failedCleanupExec struct{ started chan struct{} }

func (e *failedCleanupExec) Run(ctx context.Context, cmd, dir string, stdout, stderr io.Writer) (int, error) {
	close(e.started)
	<-ctx.Done()
	return -1, fmt.Errorf("%w: remote cleanup unconfirmed", ctx.Err())
}
func TestStopPreservesRemoteCleanupFailure(t *testing.T) {
	ex := &failedCleanupExec{make(chan struct{})}
	s := mustStart(t, "---\nterm-store\n", Config{Gate: &fixedGate{run: true}, Exec: ex})
	done := make(chan error, 1)
	go func() { _, err := s.Prompt(t.Context(), "go", nil); done <- err }()
	select {
	case <-ex.started:
	case <-time.After(3 * time.Second):
		t.Fatal("terminal did not start")
	}
	if err := s.Stop(); err == nil || !strings.Contains(err.Error(), "remote cleanup unconfirmed") {
		t.Fatalf("lost cleanup failure: %v", err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "remote cleanup unconfirmed") {
			t.Fatalf("Prompt lost cleanup failure: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Prompt did not finish")
	}
}

func TestReadLineLimitCannotOverflow(t *testing.T) {
	line, limit := 2, int(^uint(0)>>1)
	got, err := sliceLines("a\nb\nc", &line, &limit)
	if err != nil || got != "b\nc" {
		t.Fatalf("slice=%q err=%v", got, err)
	}
}
