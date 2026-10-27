package sftpsvc

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// heldDownload starts downloading a 1 MB file with its first write held
// until release is called: until then the transfer is Running whatever
// is asked of it, so the asks below race nothing.
func heldDownload(t *testing.T) (ts *Transfers, id int, data []byte, dst string, release func()) {
	t.Helper()
	r, root := queueServer(t)
	data = bigFile(t, filepath.Join(root, "f.bin"), 1<<20)
	dst = filepath.Join(t.TempDir(), "f.bin")
	ts = newQueue(r)
	held, gate := make(chan struct{}), make(chan struct{})
	var once sync.Once
	ts.onWrite = func(*Transfer, int64) {
		once.Do(func() { close(held); <-gate })
	}
	id = ts.Add(Download, root+"/f.bin", dst, "f.bin")
	<-held
	if st := ts.List()[0].State; st != Running {
		t.Fatalf("held transfer %v", st)
	}
	return ts, id, data, dst, sync.OnceFunc(func() { close(gate) })
}

func downloaded(t *testing.T, dst string, data []byte) {
	t.Helper()
	got, err := os.ReadFile(dst)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("destination: %v, %d bytes of %d", err, len(got), len(data))
	}
	if LocalExists(PartName(dst, false)) {
		t.Error("part file left")
	}
}

func TestQueueLatestAskWins(t *testing.T) {
	t.Run("pause then resume runs on", func(t *testing.T) {
		ts, id, data, dst, release := heldDownload(t)
		ts.Pause(id)
		ts.Resume(id)
		release()
		ts.Wait()
		if tr := ts.List()[0]; tr.State != Done {
			t.Fatalf("%v %v", tr.State, tr.Err)
		}
		downloaded(t, dst, data)
	})
	t.Run("pause all then resume all runs on", func(t *testing.T) {
		ts, _, data, dst, release := heldDownload(t)
		ts.PauseAll()
		ts.ResumeAll()
		release()
		ts.Wait()
		if tr := ts.List()[0]; tr.State != Done {
			t.Fatalf("%v %v", tr.State, tr.Err)
		}
		downloaded(t, dst, data)
	})
	t.Run("pause then cancel cancels", func(t *testing.T) {
		ts, id, _, dst, release := heldDownload(t)
		ts.Pause(id)
		ts.Cancel(id)
		ts.Resume(id) // too late: canceled is not paused
		release()
		ts.Wait()
		if tr := ts.List()[0]; tr.State != Canceled || tr.Bytes != 0 {
			t.Fatalf("%v at %d", tr.State, tr.Bytes)
		}
		if LocalExists(dst) || LocalExists(PartName(dst, false)) {
			t.Error("a canceled download left a file")
		}
	})
	t.Run("pause, resume, pause pauses", func(t *testing.T) {
		ts, id, _, dst, release := heldDownload(t)
		ts.Pause(id)
		ts.Resume(id)
		ts.Pause(id)
		release()
		ts.Wait()
		if tr := ts.List()[0]; tr.State != Paused {
			t.Fatalf("%v", tr.State)
		}
		if !LocalExists(PartName(dst, false)) {
			t.Error("a paused download lost its part file")
		}
	})
	t.Run("cancel then retry starts again", func(t *testing.T) {
		ts, id, data, dst, release := heldDownload(t)
		ts.Cancel(id)
		ts.Retry(id)
		release()
		ts.Wait()
		if tr := ts.List()[0]; tr.State != Done {
			t.Fatalf("%v %v", tr.State, tr.Err)
		}
		downloaded(t, dst, data)
	})
}

// A canceled transfer retried at once does not start until its part file
// is removed, so the removal cannot take the new run's part.
func TestQueueRetryWaitsForPartRemoval(t *testing.T) {
	ts, id, data, dst, release := heldDownload(t)
	ts.Cancel(id)
	ts.mu.Lock()
	tr := ts.find(id)
	ts.mu.Unlock()
	release()
	// Settled as canceled: retry it before (or while) its part goes.
	for {
		ts.mu.Lock()
		st := tr.State
		ts.mu.Unlock()
		if st == Canceled {
			break
		}
		ts.Wait()
	}
	ts.onWrite = nil
	ts.Retry(id)
	ts.Wait()
	if tr := ts.List()[0]; tr.State != Done {
		t.Fatalf("%v %v", tr.State, tr.Err)
	}
	downloaded(t, dst, data)
}

func TestQueueCanceledUploadPartOutlastsOutage(t *testing.T) {
	old := partWait
	partWait = 20 * time.Millisecond
	t.Cleanup(func() { partWait = old })
	r, root := queueServer(t)
	src := filepath.Join(t.TempDir(), "up.bin")
	data := bigFile(t, src, 1<<20)
	dst := root + "/up.bin"
	part := PartName(dst, true)
	ts := newQueue(r)
	var down atomic.Bool
	ts.Client = func(ctx context.Context) (*Client, error) {
		if down.Load() {
			<-ctx.Done() // a link that is down: Client waits
			return nil, ctx.Err()
		}
		return r.get(ctx)
	}
	held, gate := make(chan struct{}), make(chan struct{})
	var once sync.Once
	ts.onWrite = func(*Transfer, int64) { once.Do(func() { close(held); <-gate }) }
	id := ts.Add(Upload, src, dst, "up.bin")
	<-held
	down.Store(true)
	ts.Cancel(id)
	close(gate)

	// Down for many waits: the part stays (nothing can remove it), the
	// canceled row stays until it can, and a new upload of the same file
	// waits instead of writing that part.
	time.Sleep(20 * partWait)
	if !LocalExists(part) {
		t.Fatal("no part file to remove: the test holds nothing")
	}
	ts.ClearFinished()
	again := ts.Add(Upload, src, dst, "up.bin")
	time.Sleep(5 * partWait)
	list := ts.List()
	if len(list) != 2 || list[0].State != Canceled || list[1].State != Queued {
		t.Fatalf("while down: %+v", list)
	}

	// Back: the part goes, then the new upload runs.
	down.Store(false)
	ts.Wait()
	ts.mu.Lock()
	st := ts.find(again).State
	ts.mu.Unlock()
	if st != Done {
		t.Fatalf("new upload %v", st)
	}
	if got, _ := os.ReadFile(dst); !bytes.Equal(got, data) || LocalExists(part) {
		t.Errorf("after the outage: %d bytes, part left %v", len(got), LocalExists(part))
	}
	ts.ClearFinished()
	if n := len(ts.List()); n != 0 {
		t.Errorf("%d rows left", n)
	}
}
