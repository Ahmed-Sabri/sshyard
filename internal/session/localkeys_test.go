package session

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func localKey(t *testing.T, passphrase string, explicit bool) LocalKey {
	t.Helper()
	pem, pub := keyPEM(t, passphrase)
	return LocalKey{Path: "/keys/" + KeyID(pub)[:8], Public: pub, PEM: pem, Encrypted: passphrase != "", Explicit: explicit}
}

func agentSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func offered(o *keyOffer) []string {
	var ids []string
	for _, s := range o.signers {
		ids = append(ids, KeyID(s.PublicKey()))
	}
	return ids
}

func id(pub ssh.PublicKey) string { return KeyID(pub) }

// TestOfferKeysOrder: the key that logged in last comes first, then
// those that sign unasked (named by the config, the agent's, the default
// files), then the encrypted ones; each once, skipped ones never, and no
// more than MaxOfferedKeys.
func TestOfferKeysOrder(t *testing.T) {
	ag := agentSigner(t)
	named := localKey(t, "", true)
	plain := localKey(t, "", false)
	locked := localKey(t, "pw", true)
	prefer := localKey(t, "", false)
	// The agent holds the named key too: it is offered once.
	agNamed, err := ssh.ParsePrivateKey(named.PEM)
	if err != nil {
		t.Fatal(err)
	}
	cr := Credentials{LocalKeys: []LocalKey{locked, plain, named, prefer}, Prefer: id(prefer.Public)}
	got := offered(offerKeys(cr, []ssh.Signer{ag, agNamed}))
	want := []string{id(prefer.Public), id(named.Public), id(ag.PublicKey()), id(plain.Public), id(locked.Public)}
	if len(got) != len(want) {
		t.Fatalf("offered %d keys, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("key %d is not the one expected", i)
		}
	}

	cr.Skip = []string{id(prefer.Public)}
	cr.IdentitiesOnly = true
	got = offered(offerKeys(cr, []ssh.Signer{ag, agNamed}))
	if len(got) != 2 || got[0] != id(named.Public) || got[1] != id(locked.Public) {
		t.Errorf("IdentitiesOnly offered %d keys", len(got))
	}

	var many []LocalKey
	for range MaxOfferedKeys + 2 {
		many = append(many, localKey(t, "", false))
	}
	if n := len(offerKeys(Credentials{LocalKeys: many}, nil).signers); n != MaxOfferedKeys {
		t.Errorf("offered %d keys, at most %d", n, MaxOfferedKeys)
	}
}

// TestLocalKeyLogin: an encrypted key the server refuses is never asked
// for; one it accepts stops the login for its passphrase, says when the
// one given is wrong, and logs in with the right one, naming the key.
// Keys all refused say how many were tried.
func TestLocalKeyLogin(t *testing.T) {
	srv := server(t)
	kh := trusted(t, srv)
	refused := localKey(t, "pw", false)
	ok := localKey(t, "", false)
	srv.AuthorizedKey = ok.Public
	var logged string
	creds := Credentials{LocalKeys: []LocalKey{refused, ok}, OnKey: func(id string) { logged = id }}
	cfg := Config{Addr: srv.Addr, User: "demo", HostKey: kh.Callback(),
		Credentials: func(context.Context) (Credentials, error) { return creds, nil }}
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if logged != id(ok.Public) {
		t.Error("the key that logged in was not reported")
	}

	srv.AuthorizedKey = refused.Public
	creds.OnKey = nil
	_, err = Dial(context.Background(), cfg)
	var le *LocalKeyError
	if !errors.As(err, &le) || le.Wrong || le.ID != id(refused.Public) || Classify(err) != KindLocalKey {
		t.Fatalf("no passphrase: %v", err)
	}
	creds.LocalKeys[0].Passphrase = []byte("nope")
	if _, err = Dial(context.Background(), cfg); !errors.As(err, &le) || !le.Wrong {
		t.Fatalf("wrong passphrase: %v", err)
	}
	creds.LocalKeys[0].Passphrase = []byte("pw")
	if c, err = Dial(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	c.Close()

	srv.AuthorizedKey = agentSigner(t).PublicKey()
	_, err = Dial(context.Background(), cfg)
	var kr *KeysRefusedError
	if !errors.As(err, &kr) || kr.Offered != 2 || Classify(err) != KindAuth {
		t.Fatalf("all refused: %v", err)
	}
}

// TestTryAgent: an "ask" login signs with the agent's keys when one runs,
// and goes on without it when none does.
func TestTryAgent(t *testing.T) {
	srv := server(t)
	kh := trusted(t, srv)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatal(err)
	}
	old := DialAgent
	t.Cleanup(func() { DialAgent = old })
	DialAgent = func() (io.ReadWriteCloser, error) {
		c1, c2 := net.Pipe()
		go agent.ServeAgent(kr, c2)
		return c1, nil
	}
	signers, _ := kr.Signers()
	srv.AuthorizedKey = signers[0].PublicKey()
	var logged string
	cfg := Config{Addr: srv.Addr, User: "demo", HostKey: kh.Callback(),
		Credentials: func(context.Context) (Credentials, error) {
			return Credentials{TryAgent: true, OnKey: func(id string) { logged = id }}, nil
		}}
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if logged != id(srv.AuthorizedKey) {
		t.Error("the agent's key that logged in was not reported")
	}
	DialAgent = func() (io.ReadWriteCloser, error) { return nil, errors.New("no agent") }
	if _, err := Dial(context.Background(), cfg); Classify(err) != KindNoCredentials {
		t.Errorf("no agent and no keys: %v", err)
	}
}
