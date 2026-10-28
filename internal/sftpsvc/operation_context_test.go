package sftpsvc

import (
	"context"
	"errors"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/pkg/sftp"
	"io"
	"sync"
	"testing"
	"time"
)

func TestListAndWriteCancellationPreserveSharedSession(t *testing.T) {
	for _, name := range []string{"list", "write"} {
		t.Run(name, func(t *testing.T) {
			fake := &sshtest.FakeFS{Dirs: map[string][]sshtest.FakeEntry{"/": {{Name: "file"}}}, Files: map[string][]byte{"/file": []byte("old")}}
			c := connectFake(t, fake)
			fake.Stop()
			t.Cleanup(fake.Resume)
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				if name == "list" {
					_, err = c.ListContext(ctx, "/")
				} else {
					_, err = c.WriteFileNoLinksContext(ctx, "/file", []byte("new"), Version{})
				}
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				fake.Resume()
				t.Fatal("operation waited for server resume")
			}
			fake.Resume()
			if _, err := c.Home(); err != nil {
				t.Fatalf("shared session closed: %v", err)
			}
			if _, err := c.ListContext(t.Context(), "/"); err != nil {
				t.Fatalf("next operation: %v", err)
			}
		})
	}
}

// A hung request after path authorization must also be interrupted.
type stalledWriter struct {
	base    sftp.FileWriter
	fake    *sshtest.FakeFS
	reached chan struct{}
	once    sync.Once
}

func (w *stalledWriter) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	w.once.Do(func() { w.fake.Stop(); close(w.reached) })
	return w.base.Filewrite(r)
}

func TestWriteCancellationAfterValidation(t *testing.T) {
	r, _ := queueServer(t)
	fake := &sshtest.FakeFS{Files: map[string][]byte{"/file": []byte("old")}}
	reached := make(chan struct{})
	writer := &stalledWriter{base: fake.Handlers().FilePut, fake: fake, reached: reached}
	r.srv.SFTPHandlers = func() sftp.Handlers {
		h := fake.Handlers()
		h.FilePut = writer
		return h
	}
	r.srv.SFTPConn = fake.Conn
	c, err := r.get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer fake.Resume()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.WriteFileNoLinksContext(ctx, "/file", []byte("new"), Version{}); done <- err }()
	select {
	case <-reached:
	case <-time.After(time.Second):
		t.Fatal("write request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		fake.Resume()
		t.Fatal("write waited for server resume")
	}
	fake.Resume()
	if _, err := c.Home(); err != nil {
		t.Fatalf("shared SFTP closed: %v", err)
	}
}

func TestDeliveredOperationReleasesPendingQuota(t *testing.T) {
	fake := &sshtest.FakeFS{Dirs: map[string][]sshtest.FakeEntry{"/": {}}}
	c := connectFake(t, fake)
	for range 100 {
		sub, err := c.operationClient(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		c.operations.mu.Lock()
		pending := c.operations.pending
		c.operations.mu.Unlock()
		if pending != 0 {
			sub.Close()
			t.Fatalf("delivered operation still occupies %d pending slots", pending)
		}
		sub.Close()
	}
}
