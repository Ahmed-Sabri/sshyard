package keys

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

var (
	// ErrNotACert is data that is no OpenSSH certificate.
	ErrNotACert = errors.New("keys: not an OpenSSH certificate")
	// ErrHostCert is a host certificate given where a user one is wanted.
	ErrHostCert = errors.New("keys: a host certificate, not a user certificate")
	// ErrCertMismatch is a certificate of another key than the private key.
	ErrCertMismatch = errors.New("keys: the certificate is not for this private key")
)

// Cert is an OpenSSH user certificate (a key-cert.pub file): a public key
// signed by a certificate authority, offered with its private key.
type Cert struct {
	*ssh.Certificate
	// Line is the certificate as one authorized_keys line, as kept.
	Line string
}

// IsCert reports whether data looks like an OpenSSH certificate.
func IsCert(data []byte) bool {
	f := strings.Fields(string(bytes.TrimSpace(data)))
	return len(f) > 0 && strings.Contains(f[0], "-cert-v01@openssh.com")
}

// ParseCert reads a user certificate, as written by ssh-keygen -s.
func ParseCert(data []byte) (Cert, error) {
	pub, comment, _, _, err := ssh.ParseAuthorizedKey(bytes.TrimSpace(data))
	if err != nil {
		return Cert{}, ErrNotACert
	}
	c, ok := pub.(*ssh.Certificate)
	if !ok {
		return Cert{}, ErrNotACert
	}
	if c.CertType != ssh.UserCert {
		return Cert{}, ErrHostCert
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(c)))
	if comment = strings.TrimSpace(comment); comment != "" && !strings.ContainsAny(comment, "\r\n") {
		line += " " + comment
	}
	return Cert{Certificate: c, Line: line}, nil
}

// KeyFingerprint is the SHA256 fingerprint of the certified key, the one
// its private key must have.
func (c Cert) KeyFingerprint() string { return ssh.FingerprintSHA256(c.Key) }

// CAFingerprint is the SHA256 fingerprint of the authority that signed it.
func (c Cert) CAFingerprint() string { return ssh.FingerprintSHA256(c.SignatureKey) }

// Matches reports whether the certificate is for the key with this
// fingerprint; an unknown fingerprint ("") matches.
func (c Cert) Matches(fingerprint string) bool {
	return fingerprint == "" || fingerprint == c.KeyFingerprint()
}

// Forever reports a certificate valid without a time limit.
func (c Cert) Forever() bool { return c.ValidBefore == ssh.CertTimeInfinity }

// After and Before bound when the certificate is valid.
func (c Cert) After() time.Time  { return certTime(c.ValidAfter) }
func (c Cert) Before() time.Time { return certTime(c.ValidBefore) }

// Expired reports a certificate no longer valid at now.
func (c Cert) Expired(now time.Time) bool { return !c.Forever() && !now.Before(c.Before()) }

// Early reports a certificate not valid yet at now.
func (c Cert) Early(now time.Time) bool { return now.Before(c.After()) }

func certTime(t uint64) time.Time {
	if t > math.MaxInt64 {
		t = math.MaxInt64
	}
	return time.Unix(int64(t), 0)
}
