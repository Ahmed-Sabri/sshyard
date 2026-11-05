package session

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/serial"
)

// TestTelnetOpener: a Telnet session dials its address and shows what
// the server sends, its option negotiation left out.
func TestTelnetOpener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	typed := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		// IAC DO ECHO, then a banner.
		c.Write([]byte{255, 253, 1})
		c.Write([]byte("Welcome to the switch\r\n> "))
		line, _ := bufio.NewReader(c).ReadString('\r')
		typed <- line
		time.Sleep(time.Second)
	}()
	s := New(Config{Addr: ln.Addr().String()}, Options{Open: Telnet(Config{Addr: ln.Addr().String()})}, 80, 24)
	defer s.Close()
	s.Start()
	waitState(t, s, Connected)
	waitText(t, s, "Welcome to the switch")
	if strings.ContainsRune(s.Term.ScreenText(), 0xfffd) {
		t.Fatal("negotiation shows on screen")
	}
	s.Write([]byte("show\r"))
	select {
	case got := <-typed:
		if !strings.HasSuffix(got, "show\r") {
			t.Fatalf("server read %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing typed reached the server")
	}
}

// serialStub is a serial port that has nothing to say.
type serialStub struct{ *fakeStream }

func (serialStub) Break(time.Duration) error { return nil }
func (serialStub) SetDTR(bool) error         { return nil }
func (serialStub) SetRTS(bool) error         { return nil }

// TestSerialOpener: a serial session opens its port with its settings,
// and can send a break on it.
func TestSerialOpener(t *testing.T) {
	var got serial.Config
	defer func(old func(serial.Config) (serial.Port, error)) { openSerial = old }(openSerial)
	openSerial = func(c serial.Config) (serial.Port, error) {
		got = c
		return serialStub{newFakeStream()}, nil
	}
	want := serial.Config{Device: "COM3", Baud: 9600, DataBits: 7, Parity: serial.ParityEven, StopBits: serial.Stop2}
	st, err := Serial(want)(context.Background(), 80, 24, "xterm")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if got != want {
		t.Fatalf("opened %+v", got)
	}
	if _, ok := st.(Breaker); !ok {
		t.Fatal("a serial port cannot break")
	}
	if err := st.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
}
