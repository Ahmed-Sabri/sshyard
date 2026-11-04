package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/session"
	"golang.org/x/crypto/ssh"
)

// signUserCert certifies pub for user demo with a new CA, for an hour.
func signUserCert(t *testing.T, pub ssh.PublicKey) (line string, ca ssh.PublicKey) {
	t.Helper()
	_, caPriv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(caPriv)
	if err != nil {
		t.Fatal(err)
	}
	c := &ssh.Certificate{Key: pub, CertType: ssh.UserCert, KeyId: "ops-id", ValidPrincipals: []string{"demo"},
		ValidAfter: uint64(time.Now().Add(-time.Minute).Unix()), ValidBefore: uint64(time.Now().Add(time.Hour).Unix())}
	if err := c.SignCert(rand.Reader, signer); err != nil {
		t.Fatal(err)
	}
	return string(ssh.MarshalAuthorizedKey(c)), signer.PublicKey()
}

// TestCertificateLogin: a key loaded from a file brings the certificate
// beside it; a certificate of another key is refused; the saved
// credential logs in where only its certificate is trusted.
func TestCertificateLogin(t *testing.T) {
	srv, _ := keyServer(t) // no authorized_keys: the bare key is refused
	a, tt := newTestApp(t)
	tt.SetSize(1200, 2200)
	priv, pub := newKey(t)
	line, ca := signUserCert(t, pub)
	srv.TrustedCA = ca
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519")
	os.WriteFile(keyPath, openSSHKey(t, priv, ""), 0o600)
	os.WriteFile(keyPath+"-cert.pub", []byte(line), 0o644)

	a.openCredForm(nil, nil)
	f := a.credForm
	f.name, f.kind = "cert", 1
	data, err := readKeyFile(keyPath)
	a.loadKeyFile(en, f, keyPath, data, certBeside(keyPath), err)
	tt.Frame()
	if f.cert == "" || !tt.HasText("ssh-ed25519 certificate · ops-id") || !tt.HasText("Certificate") {
		t.Fatalf("certificate beside the key not loaded: %q", tt.Texts())
	}
	if !strings.Contains(strings.Join(tt.Texts(), "\n"), "Users: demo · valid until") {
		t.Errorf("no validity: %q", tt.Texts())
	}

	// Another key's certificate, dropped like a key, lands in the
	// certificate field and cannot be saved.
	_, other := newKey(t)
	otherLine, _ := signUserCert(t, other)
	f.setKey([]byte(otherLine), "other-cert.pub")
	tt.Frame()
	if f.keyData == nil || f.cert != strings.TrimSpace(otherLine) || !tt.HasText("The certificate is not for this private key.") {
		t.Fatalf("other certificate: %q", tt.Texts())
	}
	if a.SaveCredForm(en) || f.err != "The certificate is not for this private key." {
		t.Fatalf("saved a mismatched certificate: %q", f.err)
	}
	f.setKey([]byte("ssh-ed25519-cert-v01@openssh.com AAAA"), "")
	if f.certErr == nil || f.cert != "" {
		t.Error("a broken certificate was taken")
	}
	tt.Frame()
	if !tt.HasText("This is not an OpenSSH certificate.") {
		t.Errorf("broken certificate: %q", tt.Texts())
	}
	f.setCert([]byte(line))
	tt.Frame()
	mustClick(t, tt, "Remove certificate")
	if f.cert != "" {
		t.Fatal("certificate not removed")
	}
	f.setCert([]byte(line))
	if !a.SaveCredForm(en) {
		t.Fatal(f.err)
	}
	cr := a.Store().Credentials()[0]
	if cr.Certificate != strings.TrimSpace(line) {
		t.Fatalf("saved certificate %q", cr.Certificate)
	}
	a.openCredForm(&cr, nil)
	if a.credForm.cert != cr.Certificate || a.credForm.certInfo.KeyId != "ops-id" {
		t.Error("the editor does not show the saved certificate")
	}
	a.closeCredForm()

	h := addSrvHost(t, a, srv, "cert-host", assets.AuthCredential, cr.ID)
	id, err := a.Connect(h.ID, openAt{})
	if err != nil {
		t.Fatal(err)
	}
	waitConnected(t, a, tt, id)
	noSecrets(t, a.p.DataDir)
}

// TestHostKeyCertificate: a host's own key keeps its certificate on the
// host, and Normalize drops it with another way to log in.
func TestHostKeyCertificate(t *testing.T) {
	a, tt := newTestApp(t)
	priv, pub := newKey(t)
	line, _ := signUserCert(t, pub)
	a.openEditor(nil)
	f := a.form
	f.name, f.address, f.user, f.auth = "kc", "10.0.0.9", "demo", en.CredKindKey()
	f.secret.setKey(openSSHKey(t, priv, ""), "")
	f.secret.setKey([]byte(line), "")
	tt.Frame()
	if !a.SaveForm(en) {
		t.Fatalf("not saved: %q", f.err)
	}
	h := a.Store().List()[0]
	if h.Auth != assets.AuthKey || h.Certificate != strings.TrimSpace(line) {
		t.Fatalf("host %+v", h)
	}
	cr, err := a.hostCredentials(h, nil)
	if err != nil || string(cr.Certificate) != h.Certificate {
		t.Fatalf("credentials %v", err)
	}
	a.openEditor(&h)
	if a.form.secret.cert != h.Certificate {
		t.Error("the editor does not show the certificate")
	}
	h.Auth = assets.AuthAsk
	h.Normalize()
	if h.Certificate != "" {
		t.Error("a host asking for a password keeps a certificate")
	}
	if got := errText(en, h, fmt.Errorf("dial: %w", session.ErrCertificate)); !strings.Contains(got, "does not fit its private key") {
		t.Errorf("error text %q", got)
	}
}
