package sshtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func dialKey(t *testing.T, s *Server, signer ssh.Signer) (*ssh.Client, error) {
	t.Helper()
	return ssh.Dial("tcp", s.Addr, &ssh.ClientConfig{
		User:            s.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
}

func dialPassword(t *testing.T, s *Server) *ssh.Client {
	t.Helper()
	c, err := ssh.Dial("tcp", s.Addr, &ssh.ClientConfig{
		User:            s.User,
		Auth:            []ssh.AuthMethod{ssh.Password(s.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestAuthorizedKeysFile(t *testing.T) {
	s, err := Start("127.0.0.1:0", "demo", "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := filepath.Join(t.TempDir(), ".ssh")
	os.Mkdir(dir, 0o700)
	file := filepath.Join(dir, "authorized_keys")
	s.AuthorizedKeysFile = file
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)

	if _, err := dialKey(t, s, signer); err == nil {
		t.Fatal("logged in with no authorized_keys")
	}
	os.WriteFile(file, append([]byte("# comment\n"), ssh.MarshalAuthorizedKey(signer.PublicKey())...), 0o600)
	c, err := dialKey(t, s, signer)
	if err != nil {
		t.Fatalf("authorized key refused: %v", err)
	}
	c.Close()
	if runtime.GOOS == "windows" {
		return // no Unix modes
	}
	// StrictModes: a directory others may write to is not trusted.
	os.Chmod(dir, 0o777)
	if _, err := dialKey(t, s, signer); err == nil {
		t.Fatal("logged in through a world-writable ~/.ssh")
	}
	os.Chmod(dir, 0o700)
	os.Chmod(file, 0o666)
	if _, err := dialKey(t, s, signer); err == nil {
		t.Fatal("logged in through a world-writable authorized_keys")
	}
}

// echoServer answers each connection with "echo:" and what it read.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				b := make([]byte, 64)
				n, _ := c.Read(b)
				io.WriteString(c, "echo:"+string(b[:n]))
			}()
		}
	}()
	return ln.Addr().String()
}

func TestDirectTCPIP(t *testing.T) {
	s, _ := Start("127.0.0.1:0", "demo", "pw")
	defer s.Close()
	target := echoServer(t)
	c := dialPassword(t, s)
	if _, err := c.Dial("tcp", target); err == nil {
		t.Fatal("direct-tcpip allowed while off")
	}
	s.DirectTCPIP = true
	conn, err := c.Dial("tcp", target)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(conn, "hi")
	got, _ := io.ReadAll(conn)
	if string(got) != "echo:hi" || s.Tunnels() != 1 {
		t.Fatalf("got %q, %d tunnels", got, s.Tunnels())
	}
	// A jump: an SSH connection through the tunnel.
	s2, _ := Start("127.0.0.1:0", "demo", "pw2")
	defer s2.Close()
	nc, err := c.Dial("tcp", s2.Addr)
	if err != nil {
		t.Fatal(err)
	}
	cc, chans, reqs, err := ssh.NewClientConn(nc, s2.Addr, &ssh.ClientConfig{User: "demo", Auth: []ssh.AuthMethod{ssh.Password("pw2")}, HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	ssh.NewClient(cc, chans, reqs).Close()
}

func TestTCPIPForward(t *testing.T) {
	s, _ := Start("127.0.0.1:0", "demo", "pw")
	defer s.Close()
	c := dialPassword(t, s)
	if _, err := c.Listen("tcp", "127.0.0.1:0"); err == nil {
		t.Fatal("tcpip-forward allowed while off")
	}
	s.TCPIPForward = true
	ln, err := c.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		b := make([]byte, 16)
		n, _ := conn.Read(b)
		io.WriteString(conn, "back:"+string(b[:n]))
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(conn, "x")
	got, _ := io.ReadAll(conn)
	if string(got) != "back:x" {
		t.Fatalf("got %q", got)
	}
	ln.Close()
	time.Sleep(50 * time.Millisecond)
	if c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second); err == nil {
		c.Close()
		t.Fatal("remote listener still open after cancel")
	}
}

func TestExec(t *testing.T) {
	s, _ := Start("127.0.0.1:0", "demo", "pw")
	defer s.Close()
	s.Exec = func(ch ssh.Channel, cmd string) int {
		io.WriteString(ch, strings.ToUpper(cmd))
		return 2
	}
	sess, err := dialPassword(t, s).NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out, err := sess.Output("hello")
	if string(out) != "HELLO" {
		t.Fatalf("out %q", out)
	}
	if e, ok := err.(*ssh.ExitError); !ok || e.ExitStatus() != 2 {
		t.Fatalf("err %v", err)
	}
}

func TestExecPTY(t *testing.T) {
	s, _ := Start("127.0.0.1:0", "demo", "pw")
	defer s.Close()
	s.Exec = func(ch ssh.Channel, cmd string) int {
		io.WriteString(ch, "plain:"+cmd)
		return 0
	}
	s.ExecPTY = func(ch ssh.Channel, cmd string, p PTY, _ <-chan PTY) int {
		io.WriteString(ch, "pty:"+cmd+":"+p.Term+":"+strconv.Itoa(p.Cols)+"x"+strconv.Itoa(p.Rows))
		return 0
	}
	c := dialPassword(t, s)
	run := func(withPTY bool) string {
		sess, err := c.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		defer sess.Close()
		if withPTY {
			if err := sess.RequestPty("xterm", 24, 80, nil); err != nil {
				t.Fatal(err)
			}
		}
		out, err := sess.Output("ls")
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	if got := run(false); got != "plain:ls" {
		t.Errorf("without pty: %q", got)
	}
	if got := run(true); got != "pty:ls:xterm:80x24" {
		t.Errorf("with pty: %q", got)
	}
	// Without ExecPTY, a PTY channel's exec still goes to Exec.
	s.ExecPTY = nil
	if got := run(true); got != "plain:ls" {
		t.Errorf("pty, no ExecPTY: %q", got)
	}
}
