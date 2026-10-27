package netproxy

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Protocol limits what a Server accepts; Auto takes them all, told
// apart by the first byte.
type Protocol string

const (
	Auto        Protocol = ""
	OnlyHTTP    Protocol = "http"
	OnlySOCKS4  Protocol = "socks4" // SOCKS4 and SOCKS4a
	OnlySOCKS5  Protocol = "socks5"
	handshakeTO          = 30 * time.Second
)

// ErrProtocol is a client speaking a protocol the server does not take.
var ErrProtocol = errors.New("proxy: protocol not accepted")

// Server serves a local proxy (a dynamic forward): HTTP CONNECT and
// plain HTTP requests with an absolute URI, SOCKS4, SOCKS4a and SOCKS5
// CONNECT. Every target is dialed with Dial, so names are resolved on
// the far side.
type Server struct {
	Protocol Protocol
	// User and Password, when User is set, are required of HTTP (Basic)
	// and SOCKS5 (username/password) clients; SOCKS4, which cannot carry
	// a password, is then refused.
	User, Password string
	Dial           func(ctx context.Context, network, addr string) (net.Conn, error)
	Conns          *Registry
	// OnError, when set, hears why a client was turned away.
	OnError func(err error)
}

// Serve accepts on l until it is closed.
func (s *Server) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go func() {
			if err := s.ServeConn(c); err != nil && s.OnError != nil {
				s.OnError(err)
			}
		}()
	}
}

// ServeConn serves one client connection; it blocks until it ends.
func (s *Server) ServeConn(c net.Conn) error {
	c.SetDeadline(time.Now().Add(handshakeTO))
	br := bufio.NewReader(c)
	b, err := br.Peek(1)
	if err != nil {
		c.Close()
		return nil
	}
	proto := OnlyHTTP
	switch b[0] {
	case 4:
		proto = OnlySOCKS4
	case 5:
		proto = OnlySOCKS5
	}
	if s.Protocol != Auto && proto != s.Protocol {
		err := fmt.Errorf("%w: %s on a %s-only port", ErrProtocol, proto, s.Protocol)
		switch proto {
		case OnlyHTTP:
			// Say so in a way curl and browsers show.
			body := "This proxy port accepts " + strings.ToUpper(string(s.Protocol)) + " only.\n"
			fmt.Fprintf(c, "HTTP/1.1 400 Bad Request\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
		case OnlySOCKS4:
			c.Write([]byte{0, 0x5b, 0, 0, 0, 0, 0, 0})
		case OnlySOCKS5:
			c.Write([]byte{5, 0xff})
		}
		c.Close()
		return err
	}
	bc := &bufConn{c, br}
	switch proto {
	case OnlySOCKS4:
		err = s.socks4(bc)
	case OnlySOCKS5:
		err = s.socks5(bc)
	default:
		err = s.http(bc)
	}
	if err != nil {
		c.Close()
	}
	return err
}

// NetConn is the connection under the buffer.
func (c *bufConn) NetConn() net.Conn { return c.Conn }

// CloseWrite half-closes when the connection under it can.
func (c *bufConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return errors.ErrUnsupported
}

func (s *Server) dial(target string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), handshakeTO)
	defer cancel()
	return s.Dial(ctx, "tcp", target)
}

func (s *Server) relay(c *bufConn, up net.Conn, target, proto string) error {
	c.SetDeadline(time.Time{})
	s.Conns.Relay(c, up, target, proto)
	return nil
}

func (s *Server) socks4(c *bufConn) error {
	var hdr [8]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return err
	}
	fail := func(err error) error {
		c.Write([]byte{0, 0x5b, 0, 0, 0, 0, 0, 0})
		return err
	}
	if _, err := c.r.ReadString(0); err != nil { // user id
		return err
	}
	if hdr[1] != 1 {
		return fail(errors.New("socks4: only CONNECT is supported"))
	}
	if s.User != "" {
		return fail(fmt.Errorf("%w: socks4 cannot authenticate", ErrAuth))
	}
	port := binary.BigEndian.Uint16(hdr[2:4])
	host := net.IP(hdr[4:8]).String()
	proto := "socks4"
	if hdr[4] == 0 && hdr[5] == 0 && hdr[6] == 0 && hdr[7] != 0 {
		// SOCKS4a: the name follows, resolved on the far side.
		name, err := c.r.ReadString(0)
		if err != nil {
			return err
		}
		host, proto = strings.TrimSuffix(name, "\x00"), "socks4a"
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(port)))
	up, err := s.dial(target)
	if err != nil {
		return fail(err)
	}
	if _, err := c.Write([]byte{0, 0x5a, 0, 0, 0, 0, 0, 0}); err != nil {
		up.Close()
		return err
	}
	return s.relay(c, up, target, proto)
}

func (s *Server) socks5(c *bufConn) error {
	var hdr [2]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return err
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return err
	}
	want := byte(0)
	if s.User != "" {
		want = 2
	}
	if !strings.ContainsRune(string(methods), rune(want)) {
		c.Write([]byte{5, 0xff})
		return fmt.Errorf("%w: socks5 client offered no usable method", ErrAuth)
	}
	if _, err := c.Write([]byte{5, want}); err != nil {
		return err
	}
	if want == 2 {
		var v [2]byte
		if _, err := io.ReadFull(c, v[:]); err != nil {
			return err
		}
		user := make([]byte, v[1])
		if _, err := io.ReadFull(c, user); err != nil {
			return err
		}
		var l [1]byte
		if _, err := io.ReadFull(c, l[:]); err != nil {
			return err
		}
		pw := make([]byte, l[0])
		if _, err := io.ReadFull(c, pw); err != nil {
			return err
		}
		if !s.authOK(string(user), string(pw)) {
			c.Write([]byte{1, 1})
			return fmt.Errorf("%w: socks5 wrong user name or password", ErrAuth)
		}
		if _, err := c.Write([]byte{1, 0}); err != nil {
			return err
		}
	}
	var req [4]byte
	if _, err := io.ReadFull(c, req[:]); err != nil {
		return err
	}
	reply := func(code byte) {
		c.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
	}
	var host string
	switch req[3] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(c, b); err != nil {
			return err
		}
		host = net.IP(b).String()
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(c, b); err != nil {
			return err
		}
		host = net.IP(b).String()
	case 3:
		var l [1]byte
		if _, err := io.ReadFull(c, l[:]); err != nil {
			return err
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(c, b); err != nil {
			return err
		}
		host = string(b)
	default:
		reply(8)
		return errors.New("socks5: bad address type")
	}
	var pb [2]byte
	if _, err := io.ReadFull(c, pb[:]); err != nil {
		return err
	}
	if req[1] != 1 {
		reply(7)
		return errors.New("socks5: only CONNECT is supported")
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb[:]))))
	up, err := s.dial(target)
	if err != nil {
		reply(dialReply(err))
		return err
	}
	reply(0)
	return s.relay(c, up, target, "socks5")
}

// dialReply is the SOCKS5 code for a failed dial.
func dialReply(err error) byte {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "refused"):
		return 5
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "unreachable"):
		return 4
	}
	return 1
}

func (s *Server) authOK(user, pw string) bool {
	return subtle.ConstantTimeCompare([]byte(user), []byte(s.User)) == 1 &&
		subtle.ConstantTimeCompare([]byte(pw), []byte(s.Password)) == 1
}

func (s *Server) http(c *bufConn) error {
	req, err := http.ReadRequest(c.r)
	if err != nil {
		return err
	}
	respond := func(code int, extra string) {
		body := http.StatusText(code) + "\n"
		fmt.Fprintf(c, "HTTP/1.1 %d %s\r\n%sContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", code, http.StatusText(code), extra, len(body), body)
	}
	if s.User != "" {
		ok := false
		if v, found := strings.CutPrefix(req.Header.Get("Proxy-Authorization"), "Basic "); found {
			if b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v)); err == nil {
				u, p, _ := strings.Cut(string(b), ":")
				ok = s.authOK(u, p)
			}
		}
		if !ok {
			respond(http.StatusProxyAuthRequired, "Proxy-Authenticate: Basic realm=\"SSHYard\"\r\n")
			return fmt.Errorf("%w: http proxy credentials missing or wrong", ErrAuth)
		}
	}
	if req.Method == http.MethodConnect {
		target := req.Host
		if _, _, err := net.SplitHostPort(target); err != nil {
			target = net.JoinHostPort(target, "443")
		}
		up, err := s.dial(target)
		if err != nil {
			respond(http.StatusBadGateway, "")
			return err
		}
		if _, err := io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			up.Close()
			return err
		}
		return s.relay(c, up, target, "connect")
	}
	// A plain request with an absolute URI: send it on in origin form,
	// one request per connection.
	if req.URL == nil || req.URL.Scheme != "http" || req.URL.Host == "" {
		respond(http.StatusBadRequest, "")
		return errors.New("http proxy: not an absolute http:// request")
	}
	target := req.URL.Host
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = net.JoinHostPort(target, "80")
	}
	up, err := s.dial(target)
	if err != nil {
		respond(http.StatusBadGateway, "")
		return err
	}
	req.Header.Del("Proxy-Authorization")
	req.Header.Del("Proxy-Connection")
	req.Close = true
	req.RequestURI = ""
	if err := req.Write(up); err != nil {
		up.Close()
		respond(http.StatusBadGateway, "")
		return err
	}
	return s.relay(c, up, target, "http")
}
