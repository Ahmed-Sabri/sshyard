package serial

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sync/atomic"
	"syscall"
	"time"

	bugserial "go.bug.st/serial"
)

// bugst is the go.bug.st/serial Backend.
type bugst struct{}

var (
	bugParity = map[Parity]bugserial.Parity{
		ParityNone: bugserial.NoParity, ParityOdd: bugserial.OddParity, ParityEven: bugserial.EvenParity,
		ParityMark: bugserial.MarkParity, ParitySpace: bugserial.SpaceParity,
	}
	bugStopBits = map[StopBits]bugserial.StopBits{
		Stop1: bugserial.OneStopBit, Stop1Half: bugserial.OnePointFiveStopBits, Stop2: bugserial.TwoStopBits,
	}
)

// bugMode converts c; nil InitialStatusBits leaves DTR and RTS on.
func bugMode(c Config) *bugserial.Mode {
	return &bugserial.Mode{BaudRate: c.Baud, DataBits: c.DataBits, Parity: bugParity[c.Parity], StopBits: bugStopBits[c.StopBits]}
}

func (bugst) Open(c Config) (Port, error) {
	raw, err := bugserial.Open(c.Device, bugMode(c))
	if err != nil {
		return nil, mapErr(c.Device, err)
	}
	if err := raw.SetReadTimeout(bugserial.NoTimeout); err != nil {
		raw.Close()
		return nil, mapErr(c.Device, err)
	}
	return newPort(c.Device, raw), nil
}

func (bugst) Ports() ([]string, error) { return bugserial.GetPortsList() }

// mapErr wraps an open error in the sentinel it stands for, keeping the
// cause. go.bug.st reports some failures as a coded PortError and passes
// others through as raw OS errors.
func mapErr(device string, err error) error {
	var sentinel error
	var coded interface {
		Code() bugserial.PortErrorCode
	}
	switch {
	case errors.As(err, &coded):
		switch coded.Code() {
		case bugserial.PortNotFound:
			sentinel = ErrNotFound
		case bugserial.PortBusy:
			sentinel = ErrBusy
		case bugserial.PermissionDenied:
			sentinel = ErrPermission
		case bugserial.InvalidSerialPort, bugserial.InvalidSpeed, bugserial.InvalidDataBits,
			bugserial.InvalidParity, bugserial.InvalidStopBits, bugserial.InvalidTimeoutValue:
			sentinel = ErrInvalidConfig
		case bugserial.PortClosed:
			sentinel = ErrDisconnected
		}
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENXIO), errors.Is(err, syscall.ENODEV):
		sentinel = ErrNotFound
	case errors.Is(err, syscall.EBUSY):
		sentinel = ErrBusy
	case errors.Is(err, fs.ErrPermission):
		sentinel = ErrPermission
	}
	if sentinel == nil {
		return fmt.Errorf("serial: %s: %w", device, err)
	}
	return fmt.Errorf("%w: %s: %w", sentinel, device, err)
}

// port gives a backend port the Port contract: io.EOF after our own Close,
// ErrDisconnected for any other failure, and no (0, nil) reads.
type port struct {
	raw    Port
	device string
	closed atomic.Bool
}

func newPort(device string, raw Port) *port { return &port{raw: raw, device: device} }

func (p *port) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	for {
		if p.closed.Load() {
			return 0, io.EOF
		}
		n, err := p.raw.Read(b)
		switch {
		case err != nil && p.closed.Load():
			return n, io.EOF
		case err != nil:
			return n, p.lost(err)
		case n > 0:
			return n, nil
		}
	}
}

func (p *port) Write(b []byte) (int, error) {
	total := 0
	for len(b) > 0 {
		if p.closed.Load() {
			return total, os.ErrClosed
		}
		n, err := p.raw.Write(b)
		total += n
		b = b[n:]
		switch {
		case err != nil && p.closed.Load():
			return total, os.ErrClosed
		case err != nil:
			return total, p.lost(err)
		case n == 0:
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func (p *port) Break(d time.Duration) error { return p.control(func() error { return p.raw.Break(d) }) }
func (p *port) SetDTR(on bool) error        { return p.control(func() error { return p.raw.SetDTR(on) }) }
func (p *port) SetRTS(on bool) error        { return p.control(func() error { return p.raw.SetRTS(on) }) }

func (p *port) control(f func() error) error {
	if p.closed.Load() {
		return os.ErrClosed
	}
	return f()
}

func (p *port) Close() error {
	if !p.closed.CompareAndSwap(false, true) {
		return nil
	}
	return p.raw.Close()
}

func (p *port) lost(err error) error {
	return fmt.Errorf("%w: %s: %w", ErrDisconnected, p.device, err)
}
