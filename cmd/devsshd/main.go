//go:build !windows

// Command devsshd is a development SSH server on loopback that runs a real
// shell in a PTY, with SFTP, for trying SSHYard where no sshd is installed.
// It is not part of the app and not meant for anything but local testing.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"flag"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Licoy/sshyard/internal/sshtest"
	"golang.org/x/crypto/ssh"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:2222", "address to listen on")
	user := flag.String("user", "demo", "user name")
	pass := flag.String("password", "demo", "password")
	root := flag.String("root", "", "SFTP and shell directory")
	shell := flag.String("shell", "/bin/bash", "shell to run")
	keys := flag.String("authorized-keys", "", "authorized_keys file for public-key logins (default <root>/.ssh/authorized_keys when -root is set; StrictModes applies)")
	direct := flag.Bool("direct-tcpip", true, "allow direct-tcpip channels (ssh -L/-D, jump hosts)")
	remote := flag.Bool("tcpip-forward", true, "allow tcpip-forward requests (ssh -R)")
	execOn := flag.Bool("exec", true, "allow exec requests")
	hostKey := flag.String("host-key", "", "host key file, created when missing, so restarts keep the key (a fresh key per run when empty)")
	flag.Parse()
	if *keys == "" && *root != "" {
		*keys = filepath.Join(*root, ".ssh", "authorized_keys")
	}

	hk := sshtest.NewHostKey()
	if *hostKey != "" {
		var err error
		if hk, err = loadHostKey(*hostKey); err != nil {
			log.Fatal(err)
		}
	}
	srv, err := sshtest.StartWithKey(*addr, *user, *pass, hk)
	if err != nil {
		log.Fatal(err)
	}
	srv.SFTPRoot = *root
	srv.AuthorizedKeysFile = *keys
	srv.DirectTCPIP, srv.TCPIPForward = *direct, *remote
	enableExec(srv, runner{shell: *shell, root: *root}, *execOn)
	log.Printf("devsshd on %s (user %q, password %q, authorized keys %q, direct-tcpip %v, tcpip-forward %v, exec %v)", srv.Addr, *user, *pass, *keys, *direct, *remote, *execOn)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGUSR1)
	for s := range sig {
		if s == syscall.SIGUSR1 {
			// Cut every connection, as a network drop would.
			log.Print("devsshd: dropping connections")
			srv.Drop()
			continue
		}
		break
	}
	srv.Close()
}

// loadHostKey reads an OpenSSH private key from path, first writing a new
// ed25519 key there when there is none.
func loadHostKey(path string) (ssh.Signer, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		blk, err := ssh.MarshalPrivateKey(priv, "devsshd host key")
		if err != nil {
			return nil, err
		}
		b = pem.EncodeToMemory(blk)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(b)
}
