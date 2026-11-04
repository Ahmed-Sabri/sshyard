package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func signCert(t *testing.T, pub ssh.PublicKey, kind uint32, before uint64) []byte {
	t.Helper()
	_, caPriv, _ := ed25519.GenerateKey(rand.Reader)
	ca, _ := ssh.NewSignerFromKey(caPriv)
	c := &ssh.Certificate{Key: pub, CertType: kind, KeyId: "alice@ops", ValidPrincipals: []string{"alice", "root"},
		ValidAfter: 1700000000, ValidBefore: before}
	if err := c.SignCert(rand.Reader, ca); err != nil {
		t.Fatal(err)
	}
	return []byte(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(c))) + " alice@laptop\n")
}

func TestParseCert(t *testing.T) {
	k, err := Generate("t", nil)
	if err != nil {
		t.Fatal(err)
	}
	data := signCert(t, k.Public, ssh.UserCert, 1800000000)
	if !IsCert(data) || IsCert([]byte(k.AuthorizedLine())) {
		t.Fatal("IsCert")
	}
	c, err := ParseCert(data)
	if err != nil {
		t.Fatal(err)
	}
	if c.KeyId != "alice@ops" || len(c.ValidPrincipals) != 2 || c.Type() != "ssh-ed25519-cert-v01@openssh.com" {
		t.Errorf("cert %+v", c.Certificate)
	}
	if !c.Matches(k.Fingerprint()) || !c.Matches("") || c.Matches("SHA256:other") {
		t.Error("Matches")
	}
	if !strings.HasSuffix(c.Line, " alice@laptop") {
		t.Errorf("line %q", c.Line)
	}
	if again, err := ParseCert([]byte(c.Line)); err != nil || again.KeyFingerprint() != k.Fingerprint() || again.CAFingerprint() != c.CAFingerprint() {
		t.Errorf("reparse %v", err)
	}
	if c.Forever() || !c.Expired(time.Unix(1800000000, 0)) || c.Expired(time.Unix(1799999999, 0)) ||
		!c.Early(time.Unix(1699999999, 0)) || c.Early(time.Unix(1700000000, 0)) {
		t.Error("validity")
	}
	if c, _ := ParseCert(signCert(t, k.Public, ssh.UserCert, ssh.CertTimeInfinity)); !c.Forever() || c.Expired(time.Now()) {
		t.Error("a certificate without a limit expired")
	}
	if _, err := ParseCert(signCert(t, k.Public, ssh.HostCert, 1800000000)); !errors.Is(err, ErrHostCert) {
		t.Errorf("host certificate: %v", err)
	}
	if _, err := ParseCert([]byte(k.AuthorizedLine())); !errors.Is(err, ErrNotACert) {
		t.Errorf("public key: %v", err)
	}
}
