// Command devproxy is a development proxy on loopback, speaking HTTP
// CONNECT, SOCKS4/4a and SOCKS5 on one port, for trying SSHYard's proxy
// support where no proxy is installed. It is not part of the app and not
// meant for anything but local testing.
package main

import (
	"flag"
	"log"
	"net"

	"github.com/Licoy/sshyard/internal/netproxy"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:1080", "address to listen on")
	user := flag.String("user", "", "user name clients must give (none when empty)")
	pass := flag.String("password", "", "password clients must give with -user")
	flag.Parse()
	l, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("devproxy: HTTP CONNECT and SOCKS on %s", l.Addr())
	s := &netproxy.Server{
		User: *user, Password: *pass,
		Dial:    (&net.Dialer{}).DialContext,
		OnError: func(err error) { log.Printf("devproxy: %v", err) },
	}
	log.Fatal(s.Serve(l))
}
