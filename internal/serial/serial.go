// Package serial opens local serial ports as terminal byte streams. It
// wraps a swappable Backend (go.bug.st/serial by default) behind its own
// types, so callers never see the backend's.
package serial

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
	"time"
)

// Parity is a port's parity setting.
type Parity string

// Parity settings.
const (
	ParityNone  Parity = "none"
	ParityOdd   Parity = "odd"
	ParityEven  Parity = "even"
	ParityMark  Parity = "mark"
	ParitySpace Parity = "space"
)

// StopBits is a port's stop-bit setting.
type StopBits string

// Stop-bit settings.
const (
	Stop1     StopBits = "1"
	Stop1Half StopBits = "1.5"
	Stop2     StopBits = "2"
)

// DefaultBaud is the baud rate of a Config that names none.
const DefaultBaud = 115200

// maxBaud bounds the baud rates Validate accepts.
const maxBaud = 20_000_000

// Bauds are the baud rates offered as choices; any positive rate is allowed.
var Bauds = []int{300, 1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200, 230400, 460800, 921600}

var parityLetters = map[Parity]byte{ParityNone: 'N', ParityOdd: 'O', ParityEven: 'E', ParityMark: 'M', ParitySpace: 'S'}

// Errors returned by Open and by a Port's Read; each wraps the original cause.
var (
	ErrNotFound      = errors.New("serial: no such port")
	ErrBusy          = errors.New("serial: port in use")
	ErrPermission    = errors.New("serial: permission denied")
	ErrInvalidConfig = errors.New("serial: invalid settings")
	ErrDisconnected  = errors.New("serial: device disconnected")
)

// Config is how to open a port.
type Config struct {
	Device   string
	Baud     int      // 0 means DefaultBaud
	DataBits int      // 0 means 8; valid 5..8
	Parity   Parity   // "" means none
	StopBits StopBits // "" means 1
}

// Normalized fills in defaults and tidies Device (on Windows "com3" and
// `\\.\COM3` become "COM3").
func (c Config) Normalized() Config {
	c.Device = normalizeDevice(runtime.GOOS, strings.TrimSpace(c.Device))
	if c.Baud == 0 {
		c.Baud = DefaultBaud
	}
	if c.DataBits == 0 {
		c.DataBits = 8
	}
	if c.Parity == "" {
		c.Parity = ParityNone
	}
	if c.StopBits == "" {
		c.StopBits = Stop1
	}
	return c
}

// Validate reports whether the normalized Config can be opened; errors wrap
// ErrInvalidConfig.
func (c Config) Validate() error {
	c = c.Normalized()
	var problem string
	switch {
	case c.Device == "":
		problem = "no device"
	case c.Baud <= 0 || c.Baud > maxBaud:
		problem = fmt.Sprintf("baud rate %d", c.Baud)
	case c.DataBits < 5 || c.DataBits > 8:
		problem = fmt.Sprintf("%d data bits", c.DataBits)
	case parityLetters[c.Parity] == 0:
		problem = fmt.Sprintf("parity %q", c.Parity)
	case c.StopBits != Stop1 && c.StopBits != Stop1Half && c.StopBits != Stop2:
		problem = fmt.Sprintf("stop bits %q", c.StopBits)
	default:
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalidConfig, problem)
}

// Frame is the conventional short form of the normalized framing, such as
// "8N1" or "7E1.5".
func (c Config) Frame() string {
	c = c.Normalized()
	letter := parityLetters[c.Parity]
	if letter == 0 {
		letter = '?'
	}
	return fmt.Sprintf("%d%c%s", c.DataBits, letter, c.StopBits)
}

// Port is an open serial port. Read blocks until data arrives; after Close
// it returns io.EOF, and if the device goes away it returns an error
// wrapping ErrDisconnected. Close is idempotent and may run concurrently
// with a blocked Read.
type Port interface {
	io.ReadWriteCloser
	Break(d time.Duration) error
	SetDTR(on bool) error
	SetRTS(on bool) error
}

// Backend opens ports and lists devices.
type Backend interface {
	// Open opens a normalized, validated Config.
	Open(Config) (Port, error)
	// Ports lists the serial devices present, in any order.
	Ports() ([]string, error)
}

// Default is the Backend Open and Ports use: go.bug.st/serial.
var Default Backend = bugst{}

// Open normalizes and validates c, then opens it with Default.
func Open(c Config) (Port, error) {
	c = c.Normalized()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return Default.Open(c)
}

// Ports lists the serial devices worth offering on this computer, sorted
// naturally (COM2 before COM10).
func Ports() ([]string, error) {
	names, err := Default.Ports()
	if err != nil {
		return nil, fmt.Errorf("serial: list ports: %w", err)
	}
	return listPorts(runtime.GOOS, names), nil
}

// normalizeDevice gives a Windows COM port its plain upper-case name; the
// backend adds the `\\.\` prefix itself. Other systems keep the path.
func normalizeDevice(goos, d string) string {
	if goos != "windows" {
		return d
	}
	d = strings.TrimPrefix(d, `\\.\`)
	if len(d) > 3 && strings.EqualFold(d[:3], "com") && strings.Trim(d[3:], "0123456789") == "" {
		d = "COM" + d[3:]
	}
	return d
}

// listPorts drops blanks, duplicates and, on macOS, the /dev/tty.* call-in
// devices (opening one blocks waiting for carrier; /dev/cu.* is the same
// port), then sorts naturally.
func listPorts(goos string, names []string) []string {
	out := []string{}
	for _, n := range names {
		n = normalizeDevice(goos, strings.TrimSpace(n))
		if n == "" || slices.Contains(out, n) || goos == "darwin" && !strings.HasPrefix(n, "/dev/cu.") {
			continue
		}
		out = append(out, n)
	}
	slices.SortFunc(out, naturalCompare)
	return out
}

// naturalCompare orders strings with runs of digits compared as numbers.
func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		if isDigit(a[0]) && isDigit(b[0]) {
			da, ra := leadingDigits(a)
			db, rb := leadingDigits(b)
			na, nb := strings.TrimLeft(da, "0"), strings.TrimLeft(db, "0")
			if c := cmp.Or(cmp.Compare(len(na), len(nb)), strings.Compare(na, nb), cmp.Compare(len(da), len(db))); c != 0 {
				return c
			}
			a, b = ra, rb
			continue
		}
		if a[0] != b[0] {
			return cmp.Compare(a[0], b[0])
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

func leadingDigits(s string) (digits, rest string) {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return s[:i], s[i:]
}
