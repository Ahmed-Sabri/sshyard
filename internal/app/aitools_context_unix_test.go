//go:build !windows

package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Licoy/sshyard/internal/sshtest"
	"testing"
	"time"
)

func TestHostFileToolsCancelWhileServerSilent(t *testing.T) {
	for _, name := range []string{"list_dir", "search_files", "write_file"} {
		t.Run(name, func(t *testing.T) {
			a, _, srv, host := bashHost(t)
			fake := &sshtest.FakeFS{Dirs: map[string][]sshtest.FakeEntry{"/": {{Name: "file"}}}, Files: map[string][]byte{"/file": []byte("old")}}
			srv.SFTPHandlers = fake.Handlers
			srv.SFTPConn = fake.Conn
			h := newHostTools(a, &conn{host: host})
			defer h.end()
			if err := h.connect(t.Context()); err != nil {
				t.Fatal(err)
			}
			fake.Stop()
			defer fake.Resume()
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, _, err := h.Call(ctx, name, json.RawMessage(`{"path":"/file","query":"old","content":"new"}`))
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				fake.Resume()
				t.Fatal("tool waited for server resume")
			}
			fake.Resume()
			if _, err := h.sftp.Home(); err != nil {
				t.Fatalf("shared SFTP closed: %v", err)
			}
		})
	}
}

func TestHostToolRejectsInvalidTimeoutBeforeConnecting(t *testing.T) {
	h := &hostTools{}
	for _, args := range []string{`{"timeout":1.5}`, `{"timeout":9223372036854775808}`, `{"timeout":"1800"}`} {
		if _, _, err := h.Call(t.Context(), "run_command", json.RawMessage(args)); err == nil {
			t.Fatalf("accepted %s", args)
		}
	}
}

func TestHostRunCommandHonorsTimeoutSeconds(t *testing.T) {
	a, _, _, host := bashHost(t)
	h := newHostTools(a, &conn{host: host})
	defer h.end()
	start := time.Now()
	_, exit, err := h.Call(t.Context(), "run_command", json.RawMessage(`{"command":"sleep 30","timeout":1}`))
	elapsed := time.Since(start)
	if exit != -1 || !errors.Is(err, context.DeadlineExceeded) || elapsed < 4*time.Second || elapsed > 10*time.Second {
		t.Fatalf("timeout elapsed=%s exit=%d err=%v", elapsed, exit, err)
	}
}
