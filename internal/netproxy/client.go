// Package netproxy is SSHYard's proxies, both ways: dialing out through
// an HTTP CONNECT or SOCKS5 proxy (the first hop to a host), and serving
// HTTP (CONNECT and plain absolute-URI requests), SOCKS4, SOCKS4a and
// SOCKS5 on a local port (a dynamic -D forward). It has no UI and no
// secrets of its own: a password is passed in by the caller.
package netproxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Kind is a proxy protocol.
type Kind string

const (
	HTTP   Kind = "http"
	SOCKS5 Kind = "socks5"
)

// ErrAuth reports a proxy that refused the user name and password, or
// that wants them and was given none.
var ErrAuth = errors.New("proxy: authentication failed")

// Error is a failure of the proxy itself (reaching it, or what it said),
// not of what was dialed through it.
type Error struct {
	Addr string
	Err  error
}

func (e *Error) Error() string { return "proxy " + e.Addr + ": " + e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// RefusedError is a proxy that reached itself but would not connect to
// the target.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return "proxy refused the connection: " + e.Reason }

// Dialer dials TCP connections through a proxy.
type Dialer struct {
	Kind Kind
	Addr string // the proxy's host:port
	User string
	// Password is read when dialing; nil or "" is none.
	Password func(ctx context.Context) (string, error)
	// Forward dials the proxy itself; nil is net.Dialer.
	Forward func(ctx context.Context, network, addr string) (net.Conn, error)
}

// DialContext connects to addr (host:port) through the proxy. A host
// name is resolved by the proxy, never locally.
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	pw := ""
	if d.User != "" && d.Password != nil {
		var err error
		if pw, err = d.Password(ctx); err != nil {
			return nil, &Error{d.Addr, err}
		}
	}
	fwd := d.Forward
	if fwd == nil {
		fwd = (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext
	}
	conn, err := fwd(ctx, "tcp", d.Addr)
	if err != nil {
		return nil, &Error{d.Addr, err}
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	switch d.Kind {
	case HTTP:
		var tunnel net.Conn
		if tunnel, err = httpConnect(conn, addr, d.User, pw); err == nil {
			conn = tunnel
		}
	case SOCKS5:
		err = socks5Connect(conn, addr, d.User, pw)
	default:
		err = fmt.Errorf("unknown proxy kind %q", d.Kind)
	}
	if !stop() && err == nil {
		err = ctx.Err()
	}
	if err != nil {
		conn.Close()
		var re *RefusedError
		if errors.As(err, &re) {
			return nil, err
		}
		return nil, &Error{d.Addr, err}
	}
	conn.SetDeadline(time.Time{})
	return conn, nil
}

// bufConn is a connection whose first bytes were read into a buffer.
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// httpConnect opens the tunnel. The returned connection starts with any
// bytes the proxy sent after its 200 response, which a proxy forwarding
// at once (an SSH server's banner, in the same packet) often does.
func httpConnect(conn net.Conn, addr, user, pw string) (net.Conn, error) {
	req := "CONNECT " + addr + " HTTP/1.1\r\nHost: " + addr + "\r\n"
	if user != "" {
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pw)) + "\r\n"
	}
	req += "\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusProxyAuthRequired:
		return nil, ErrAuth
	case resp.StatusCode/100 != 2:
		return nil, &RefusedError{resp.Status}
	}
	if br.Buffered() > 0 {
		return &bufConn{conn, br}, nil
	}
	return conn, nil
}

func socks5Connect(conn net.Conn, addr, user, pw string) error {
	host, portS, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portS)
	if err != nil || port < 0 || port > 65535 {
		return fmt.Errorf("bad port %q", portS)
	}
	methods := []byte{5, 1, 0}
	if user != "" {
		methods = []byte{5, 2, 0, 2}
	}
	if _, err := conn.Write(methods); err != nil {
		return err
	}
	var rep [2]byte
	if _, err := io.ReadFull(conn, rep[:]); err != nil {
		return err
	}
	if rep[0] != 5 {
		return errors.New("not a SOCKS5 proxy")
	}
	switch rep[1] {
	case 0:
	case 2:
		if user == "" || len(user) > 255 || len(pw) > 255 {
			return ErrAuth
		}
		b := []byte{1, byte(len(user))}
		b = append(append(append(b, user...), byte(len(pw))), pw...)
		if _, err := conn.Write(b); err != nil {
			return err
		}
		if _, err := io.ReadFull(conn, rep[:]); err != nil {
			return err
		}
		if rep[1] != 0 {
			return ErrAuth
		}
	case 0xff:
		return ErrAuth
	default:
		return fmt.Errorf("SOCKS5 method %d not supported", rep[1])
	}
	req := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = append(append(req, 1), ip4...)
		} else {
			req = append(append(req, 4), ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return errors.New("host name too long")
		}
		req = append(append(req, 3, byte(len(host))), host...)
	}
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := conn.Write(req); err != nil {
		return err
	}
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return err
	}
	if hdr[1] != 0 {
		return &RefusedError{socks5Reply(hdr[1])}
	}
	var skip int
	switch hdr[3] {
	case 1:
		skip = 4
	case 4:
		skip = 16
	case 3:
		var l [1]byte
		if _, err := io.ReadFull(conn, l[:]); err != nil {
			return err
		}
		skip = int(l[0])
	default:
		return errors.New("bad SOCKS5 reply")
	}
	_, err = io.ReadFull(conn, make([]byte, skip+2))
	return err
}

func socks5Reply(code byte) string {
	switch code {
	case 1:
		return "general failure"
	case 2:
		return "not allowed by ruleset"
	case 3:
		return "network unreachable"
	case 4:
		return "host unreachable"
	case 5:
		return "connection refused"
	case 6:
		return "TTL expired"
	case 7:
		return "command not supported"
	case 8:
		return "address type not supported"
	}
	return "error " + strconv.Itoa(int(code))
}
