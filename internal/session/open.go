package session

import (
	"context"
	"net"

	"github.com/Licoy/sshyard/internal/serial"
	"github.com/Licoy/sshyard/internal/telnet"
)

// Opener opens a session's stream, for Options.Open.
type Opener = func(ctx context.Context, cols, rows int, term string) (Stream, error)

// Telnet opens a Telnet connection to cfg.Addr, dialed with cfg.Dial,
// through cfg.Proxy when set, within cfg.Timeout.
func Telnet(cfg Config) Opener {
	return func(ctx context.Context, cols, rows int, term string) (Stream, error) {
		dial := cfg.Dial
		if dial == nil {
			dial = (&net.Dialer{}).DialContext
		}
		if cfg.Proxy != nil {
			d := *cfg.Proxy
			d.Forward = dial
			dial = d.DialContext
		}
		dctx, cancel := context.WithTimeout(ctx, cfg.timeout())
		defer cancel()
		conn, err := dial(dctx, "tcp", cfg.Addr)
		if err != nil {
			return nil, err
		}
		return telnet.New(conn, telnet.Options{Term: term, Cols: cols, Rows: rows}), nil
	}
}

// openSerial opens a serial port; a variable so that tests can stand in.
var openSerial = serial.Open

// Serial opens the serial port of c.
func Serial(c serial.Config) Opener {
	return func(context.Context, int, int, string) (Stream, error) {
		p, err := openSerial(c)
		if err != nil {
			return nil, err
		}
		return serialStream{p}, nil
	}
}

// serialStream is a serial port as a stream: a line has no window size.
// The port's Break makes it a Breaker.
type serialStream struct{ serial.Port }

func (serialStream) Resize(int, int) error { return nil }
