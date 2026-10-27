package sftpsvc

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"sync/atomic"
	"testing"

	"github.com/Licoy/sshyard/internal/sshtest"
)

// A pager that breaks partway through a folder (a reply that makes no
// sense, or none) leaves the plan to read the folder, and the rest,
// whole: the plan is the same as without the pager.
func TestPlanDownloadPagerBreaksPartway(t *testing.T) {
	ffs := func() *sshtest.FakeFS {
		return &sshtest.FakeFS{
			Dirs: map[string][]sshtest.FakeEntry{"/": {{Name: "top", Dir: true}}, "/top": {{Name: "big", Dir: true}, {Name: "small", Dir: true}}},
			Many: func(p string) (int, bool) {
				switch p {
				case "/top/big":
					return 350, true
				case "/top/small":
					return 3, true
				}
				return 0, false
			},
		}
	}
	withoutPager(t)
	want, _, err := connectFake(t, ffs()).PlanDownload(context.Background(), []string{"/top"}, "/l")
	noPager = false
	if err != nil || len(want) != 3+353 {
		t.Fatalf("%d items, %v", len(want), err)
	}
	breaks := map[string]func(typ byte, b []byte) (byte, []byte, error){
		"bad name list": func(typ byte, b []byte) (byte, []byte, error) {
			return typ, append(b[:4:4], 0xff, 0xff, 0xff, 0xff), nil
		},
		"out of order": func(typ byte, b []byte) (byte, []byte, error) {
			b = append([]byte(nil), b...)
			binary.BigEndian.PutUint32(b, binary.BigEndian.Uint32(b)+7)
			return typ, b, nil
		},
		"unexpected reply": func(typ byte, b []byte) (byte, []byte, error) { return fxpHandle, b, nil },
		"cut off":          func(byte, []byte) (byte, []byte, error) { return 0, nil, io.ErrUnexpectedEOF },
	}
	for name, brk := range breaks {
		t.Run(name, func(t *testing.T) {
			var names atomic.Int32
			testPagerRecv = func(typ byte, b []byte) (byte, []byte, error) {
				// The second reply of names: partway through big.
				if typ == fxpName && names.Add(1) == 2 {
					return brk(typ, b)
				}
				return typ, b, nil
			}
			t.Cleanup(func() { testPagerRecv = nil })
			f := ffs()
			got, _, err := connectFake(t, f).PlanDownload(context.Background(), []string{"/top"}, "/l")
			if err != nil {
				t.Fatal(err)
			}
			if names.Load() != 2 {
				t.Fatalf("the pager read %d replies of names after it broke", names.Load())
			}
			if len(got) != len(want) {
				t.Fatalf("%d items, want %d", len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("item %d: %+v, want %+v", i, got[i], want[i])
				}
			}
		})
	}
	// The server refusing a folder is not the pager breaking: the plan
	// fails, as it did.
	t.Run("refused", func(t *testing.T) {
		testPagerRecv = func(typ byte, b []byte) (byte, []byte, error) {
			if typ == fxpName {
				id := b[:4:4]
				return fxpStatus, binary.BigEndian.AppendUint32(append(id, 0, 0, 0, fxPermDenied), 0), nil
			}
			return typ, b, nil
		}
		t.Cleanup(func() { testPagerRecv = nil })
		_, _, err := connectFake(t, ffs()).PlanDownload(context.Background(), []string{"/top"}, "/l")
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("%v, want permission denied", err)
		}
	})
}
