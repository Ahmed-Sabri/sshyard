package serial

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	bugserial "go.bug.st/serial"
)

func TestNormalized(t *testing.T) {
	got := Config{Device: "  /dev/ttyUSB0 "}.Normalized()
	want := Config{Device: "/dev/ttyUSB0", Baud: DefaultBaud, DataBits: 8, Parity: ParityNone, StopBits: Stop1}
	if runtime.GOOS == "windows" {
		want.Device = got.Device
	}
	if got != want {
		t.Fatalf("Normalized = %+v, want %+v", got, want)
	}
	kept := Config{Device: "x", Baud: 9600, DataBits: 7, Parity: ParityEven, StopBits: Stop2}
	if kept.Normalized() != kept {
		t.Fatalf("Normalized changed explicit settings: %+v", kept.Normalized())
	}
}

func TestNormalizeDevice(t *testing.T) {
	for _, tc := range []struct{ goos, in, want string }{
		{"windows", "com3", "COM3"},
		{"windows", "COM3", "COM3"},
		{"windows", `\\.\COM10`, "COM10"},
		{"windows", `\\.\com10`, "COM10"},
		{"windows", "CNCA0", "CNCA0"},
		{"windows", "com", "com"},
		{"linux", "com3", "com3"},
		{"darwin", "/dev/cu.usbserial-1", "/dev/cu.usbserial-1"},
	} {
		if got := normalizeDevice(tc.goos, tc.in); got != tc.want {
			t.Errorf("normalizeDevice(%q, %q) = %q, want %q", tc.goos, tc.in, got, tc.want)
		}
	}
}

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		c  Config
		ok bool
	}{
		{Config{Device: "/dev/ttyUSB0"}, true},
		{Config{Device: "/dev/ttyUSB0", Baud: 1, DataBits: 5, Parity: ParitySpace, StopBits: Stop1Half}, true},
		{Config{Device: "/dev/ttyUSB0", Baud: maxBaud, Parity: ParityMark}, true},
		{Config{Device: "  "}, false},
		{Config{Device: "x", Baud: -1}, false},
		{Config{Device: "x", Baud: maxBaud + 1}, false},
		{Config{Device: "x", DataBits: 4}, false},
		{Config{Device: "x", DataBits: 9}, false},
		{Config{Device: "x", Parity: "weird"}, false},
		{Config{Device: "x", StopBits: "3"}, false},
	} {
		err := tc.c.Validate()
		if tc.ok && err != nil {
			t.Errorf("Validate(%+v) = %v, want nil", tc.c, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("Validate(%+v) = %v, want ErrInvalidConfig", tc.c, err)
		}
	}
}

func TestFrame(t *testing.T) {
	for _, tc := range []struct {
		c    Config
		want string
	}{
		{Config{}, "8N1"},
		{Config{DataBits: 7, Parity: ParityEven}, "7E1"},
		{Config{StopBits: Stop1Half}, "8N1.5"},
		{Config{Parity: ParityOdd, StopBits: Stop2}, "8O2"},
		{Config{DataBits: 5, Parity: ParityMark}, "5M1"},
		{Config{Parity: ParitySpace}, "8S1"},
	} {
		if got := tc.c.Frame(); got != tc.want {
			t.Errorf("Frame(%+v) = %q, want %q", tc.c, got, tc.want)
		}
	}
}

func TestNaturalCompare(t *testing.T) {
	got := []string{"COM10", "COM2", "COM1", "/dev/ttyUSB10", "/dev/ttyUSB2", "/dev/ttyACM0", "COM02", "/dev/ttyUSB"}
	slices.SortFunc(got, naturalCompare)
	want := []string{"/dev/ttyACM0", "/dev/ttyUSB", "/dev/ttyUSB2", "/dev/ttyUSB10", "COM1", "COM2", "COM02", "COM10"}
	if !slices.Equal(got, want) {
		t.Fatalf("sorted = %q, want %q", got, want)
	}
}

func TestListPorts(t *testing.T) {
	for _, tc := range []struct {
		goos string
		in   []string
		want []string
	}{
		{"darwin", []string{"/dev/tty.usbserial-2", "/dev/cu.usbserial-10", "/dev/cu.usbserial-2", "/dev/tty.Bluetooth"},
			[]string{"/dev/cu.usbserial-2", "/dev/cu.usbserial-10"}},
		{"windows", []string{"COM10", "COM3", "", "com3"}, []string{"COM3", "COM10"}},
		{"linux", []string{"/dev/ttyUSB10", "/dev/ttyS0", "/dev/ttyUSB2", "/dev/ttyACM0"},
			[]string{"/dev/ttyACM0", "/dev/ttyS0", "/dev/ttyUSB2", "/dev/ttyUSB10"}},
	} {
		if got := listPorts(tc.goos, tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("listPorts(%s) = %q, want %q", tc.goos, got, tc.want)
		}
	}
	if got := listPorts("linux", nil); got == nil || len(got) != 0 {
		t.Errorf("listPorts(nil) = %#v, want empty non-nil", got)
	}
}

type fakeBackend struct {
	opened []Config
	ports  []string
	err    error
}

func (b *fakeBackend) Open(c Config) (Port, error) {
	b.opened = append(b.opened, c)
	return newPort(c.Device, newFakeRaw()), b.err
}

func (b *fakeBackend) Ports() ([]string, error) { return b.ports, b.err }

func useBackend(t *testing.T, b Backend) {
	old := Default
	Default = b
	t.Cleanup(func() { Default = old })
}

func TestOpenValidatesBeforeBackend(t *testing.T) {
	b := &fakeBackend{}
	useBackend(t, b)
	if _, err := Open(Config{Device: "x", DataBits: 9}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Open(invalid) = %v, want ErrInvalidConfig", err)
	}
	if len(b.opened) != 0 {
		t.Fatalf("backend opened %v for an invalid config", b.opened)
	}
	p, err := Open(Config{Device: " /dev/ttyUSB0 ", Parity: ParityEven})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	want := Config{Device: "/dev/ttyUSB0", Baud: DefaultBaud, DataBits: 8, Parity: ParityEven, StopBits: Stop1}
	if len(b.opened) != 1 || b.opened[0] != want {
		t.Fatalf("backend opened %+v, want normalized %+v", b.opened, want)
	}
}

func TestPortsUsesBackend(t *testing.T) {
	useBackend(t, &fakeBackend{ports: []string{"/dev/cu.b10", "/dev/cu.b2"}})
	got, err := Ports()
	if err != nil {
		t.Fatal(err)
	}
	if want := listPorts(runtime.GOOS, []string{"/dev/cu.b10", "/dev/cu.b2"}); !slices.Equal(got, want) {
		t.Fatalf("Ports = %q, want %q", got, want)
	}
	cause := errors.New("registry gone")
	useBackend(t, &fakeBackend{err: cause})
	if _, err := Ports(); !errors.Is(err, cause) {
		t.Fatalf("Ports error = %v, want it to wrap %v", err, cause)
	}
}

func TestBugMode(t *testing.T) {
	got := bugMode(Config{Device: "x", Baud: 9600, DataBits: 7, Parity: ParityEven, StopBits: Stop2})
	want := &bugserial.Mode{BaudRate: 9600, DataBits: 7, Parity: bugserial.EvenParity, StopBits: bugserial.TwoStopBits}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bugMode = %+v, want %+v", got, want)
	}
	for p := range parityLetters {
		if _, ok := bugParity[p]; !ok {
			t.Errorf("no go.bug.st parity for %q", p)
		}
	}
	for _, s := range []StopBits{Stop1, Stop1Half, Stop2} {
		if _, ok := bugStopBits[s]; !ok {
			t.Errorf("no go.bug.st stop bits for %q", s)
		}
	}
}

type codedErr bugserial.PortErrorCode

func (e codedErr) Error() string                 { return fmt.Sprintf("code %d", int(e)) }
func (e codedErr) Code() bugserial.PortErrorCode { return bugserial.PortErrorCode(e) }

func TestMapErr(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want error
	}{
		{&bugserial.PortError{}, ErrBusy}, // the zero code is PortBusy
		{codedErr(bugserial.PortNotFound), ErrNotFound},
		{codedErr(bugserial.PortBusy), ErrBusy},
		{codedErr(bugserial.PermissionDenied), ErrPermission},
		{codedErr(bugserial.InvalidSerialPort), ErrInvalidConfig},
		{codedErr(bugserial.InvalidSpeed), ErrInvalidConfig},
		{codedErr(bugserial.InvalidDataBits), ErrInvalidConfig},
		{codedErr(bugserial.InvalidParity), ErrInvalidConfig},
		{codedErr(bugserial.InvalidStopBits), ErrInvalidConfig},
		{codedErr(bugserial.PortClosed), ErrDisconnected},
		{syscall.ENOENT, ErrNotFound},
		{&fs.PathError{Op: "open", Path: "/dev/x", Err: syscall.ENOENT}, ErrNotFound},
		{syscall.ENXIO, ErrNotFound},
		{syscall.ENODEV, ErrNotFound},
		{syscall.EBUSY, ErrBusy},
		{syscall.EACCES, ErrPermission},
		{syscall.EPERM, ErrPermission},
		{codedErr(bugserial.FunctionNotImplemented), nil},
		{errors.New("odd"), nil},
	} {
		got := mapErr("/dev/x", tc.err)
		if !errors.Is(got, tc.err) {
			t.Errorf("mapErr(%v) = %v, lost the cause", tc.err, got)
		}
		if !strings.Contains(got.Error(), "/dev/x") || !strings.Contains(got.Error(), tc.err.Error()) {
			t.Errorf("mapErr(%v) = %q, want device and cause in message", tc.err, got)
		}
		for _, s := range []error{ErrNotFound, ErrBusy, ErrPermission, ErrInvalidConfig, ErrDisconnected} {
			if errors.Is(got, s) != (s == tc.want) {
				t.Errorf("mapErr(%v) = %v; errors.Is(%v) = %v", tc.err, got, s, !(s == tc.want))
			}
		}
	}
}

// fakeRaw is an underlying port: reads come from a queue, and Read blocks
// until something is queued or the port is closed.
type fakeRaw struct {
	mu      sync.Mutex
	reads   chan read
	done    chan struct{}
	closes  int
	written []byte
	writeN  int // bytes accepted per Write; 0 means all
	ctl     []string
}

type read struct {
	data string
	err  error
}

func newFakeRaw() *fakeRaw { return &fakeRaw{reads: make(chan read, 16), done: make(chan struct{})} }

func (f *fakeRaw) Read(b []byte) (int, error) {
	select {
	case r := <-f.reads:
		return copy(b, r.data), r.err
	case <-f.done:
		return 0, codedErr(bugserial.PortClosed)
	}
}

func (f *fakeRaw) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(b)
	if f.writeN > 0 && f.writeN < n {
		n = f.writeN
	}
	f.written = append(f.written, b[:n]...)
	return n, nil
}

func (f *fakeRaw) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	if f.closes == 1 {
		close(f.done)
	}
	return nil
}

func (f *fakeRaw) Break(d time.Duration) error {
	f.ctl = append(f.ctl, "break "+d.String())
	return nil
}
func (f *fakeRaw) SetDTR(on bool) error { f.ctl = append(f.ctl, fmt.Sprint("dtr ", on)); return nil }
func (f *fakeRaw) SetRTS(on bool) error { f.ctl = append(f.ctl, fmt.Sprint("rts ", on)); return nil }

func TestReadSkipsEmptyReads(t *testing.T) {
	raw := newFakeRaw()
	raw.reads <- read{}
	raw.reads <- read{}
	raw.reads <- read{data: "abc"}
	p := newPort("/dev/x", raw)
	b := make([]byte, 8)
	n, err := p.Read(b)
	if err != nil || string(b[:n]) != "abc" {
		t.Fatalf("Read = %q, %v; want \"abc\", nil", b[:n], err)
	}
}

func TestReadEOFAfterClose(t *testing.T) {
	raw := newFakeRaw()
	p := newPort("/dev/x", raw)
	got := make(chan error, 1)
	go func() {
		_, err := p.Read(make([]byte, 8))
		got <- err
	}()
	time.Sleep(10 * time.Millisecond) // let Read block
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-got:
		if err != io.EOF {
			t.Fatalf("Read after Close = %v, want io.EOF", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Read still blocked after Close")
	}
	if _, err := p.Read(make([]byte, 8)); err != io.EOF {
		t.Fatalf("second Read = %v, want io.EOF", err)
	}
}

func TestReadDisconnected(t *testing.T) {
	for _, cause := range []error{codedErr(bugserial.PortClosed), syscall.EIO, syscall.ENXIO} {
		raw := newFakeRaw()
		raw.reads <- read{data: "x", err: cause}
		p := newPort("/dev/x", raw)
		b := make([]byte, 8)
		n, err := p.Read(b)
		if n != 1 || !errors.Is(err, ErrDisconnected) || !errors.Is(err, cause) {
			t.Errorf("Read with %v = %d, %v; want 1 byte and ErrDisconnected wrapping it", cause, n, err)
		}
	}
}

func TestCloseIdempotent(t *testing.T) {
	raw := newFakeRaw()
	p := newPort("/dev/x", raw)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := p.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if raw.closes != 1 {
		t.Fatalf("underlying Close called %d times, want 1", raw.closes)
	}
}

func TestWriteAndControls(t *testing.T) {
	raw := newFakeRaw()
	raw.writeN = 2
	p := newPort("/dev/x", raw)
	if n, err := p.Write([]byte("hello")); n != 5 || err != nil || string(raw.written) != "hello" {
		t.Fatalf("Write = %d, %v (wrote %q); want all of it", n, err, raw.written)
	}
	if err := errors.Join(p.Break(250*time.Millisecond), p.SetDTR(false), p.SetRTS(true)); err != nil {
		t.Fatal(err)
	}
	if want := []string{"break 250ms", "dtr false", "rts true"}; !slices.Equal(raw.ctl, want) {
		t.Fatalf("controls = %q, want %q", raw.ctl, want)
	}
	p.Close()
	if _, err := p.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Write after Close = %v, want os.ErrClosed", err)
	}
	if err := p.SetDTR(true); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("SetDTR after Close = %v, want os.ErrClosed", err)
	}
}
