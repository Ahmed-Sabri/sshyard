package keys

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"math/big"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/ssh"
)

// ppk is a parsed PuTTY key file (the format is in PuTTY's
// sshpubk.c and its documentation, appendix C).
type ppk struct {
	version    int
	alg        string
	encryption string
	comment    string
	public     []byte
	private    []byte
	mac        []byte
	header     map[string]string
}

func parsePPK(data []byte) (*ppk, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	k := &ppk{header: map[string]string{}}
	i := 0
	next := func() (string, string, bool) {
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
		if i >= len(lines) {
			return "", "", false
		}
		name, value, ok := strings.Cut(lines[i], ": ")
		i++
		return name, strings.TrimRight(value, " \t\r"), ok
	}
	blob := func(n string) ([]byte, error) {
		// Compare against the lines left, never i+count: a huge count
		// would overflow the sum and slice past the end.
		count, err := strconv.Atoi(n)
		if err != nil || count < 0 || count > len(lines)-i {
			return nil, ErrNotAKey
		}
		var b strings.Builder
		for _, l := range lines[i : i+count] {
			b.WriteString(strings.TrimSpace(l))
		}
		i += count
		out, err := base64.StdEncoding.DecodeString(b.String())
		if err != nil {
			return nil, ErrNotAKey
		}
		return out, nil
	}
	name, value, ok := next()
	switch {
	case !ok:
		return nil, ErrNotAKey
	case name == "PuTTY-User-Key-File-2":
		k.version = 2
	case name == "PuTTY-User-Key-File-3":
		k.version = 3
	case strings.HasPrefix(name, "PuTTY-User-Key-File-"):
		return nil, &UnsupportedError{"PuTTY key version " + strings.TrimPrefix(name, "PuTTY-User-Key-File-")}
	default:
		return nil, ErrNotAKey
	}
	k.alg = value
	for {
		name, value, ok := next()
		if !ok {
			break
		}
		var err error
		switch name {
		case "Public-Lines":
			k.public, err = blob(value)
		case "Private-Lines":
			k.private, err = blob(value)
		case "Private-MAC":
			k.mac, err = hex.DecodeString(value)
		default:
			k.header[name] = value
		}
		if err != nil {
			return nil, ErrNotAKey
		}
	}
	k.encryption, k.comment = k.header["Encryption"], k.header["Comment"]
	if k.public == nil || k.private == nil || k.mac == nil {
		return nil, ErrNotAKey
	}
	if k.encryption != "none" && k.encryption != "aes256-cbc" {
		return nil, &UnsupportedError{"PuTTY encryption " + k.encryption}
	}
	return k, nil
}

// keys derives the cipher key, IV and MAC key from the passphrase.
func (k *ppk) keys(passphrase []byte) (cipherKey, iv, macKey []byte, err error) {
	if k.version == 2 {
		mk := sha1.Sum(append([]byte("putty-private-key-file-mac-key"), passphrase...))
		macKey = mk[:]
		if k.encryption == "none" {
			return nil, nil, macKey, nil
		}
		a := sha1.Sum(append([]byte{0, 0, 0, 0}, passphrase...))
		b := sha1.Sum(append([]byte{0, 0, 0, 1}, passphrase...))
		return append(a[:], b[:12]...), make([]byte, aes.BlockSize), macKey, nil
	}
	if k.encryption == "none" {
		return nil, nil, []byte{}, nil
	}
	num := func(name string) (uint32, error) {
		n, err := strconv.ParseUint(k.header[name], 10, 32)
		if err != nil {
			return 0, ErrNotAKey
		}
		return uint32(n), nil
	}
	mem, err1 := num("Argon2-Memory")
	passes, err2 := num("Argon2-Passes")
	par, err3 := num("Argon2-Parallelism")
	salt, err4 := hex.DecodeString(k.header["Argon2-Salt"])
	if err := errors.Join(err1, err2, err3, err4); err != nil || par == 0 || par > 255 || passes == 0 {
		return nil, nil, nil, ErrNotAKey
	}
	if mem > 4<<20 { // 4 GiB: more is no real key
		return nil, nil, nil, &UnsupportedError{"Argon2 memory size"}
	}
	var out []byte
	switch k.header["Key-Derivation"] {
	case "Argon2id":
		out = argon2.IDKey(passphrase, salt, passes, mem, uint8(par), 80)
	case "Argon2i":
		out = argon2.Key(passphrase, salt, passes, mem, uint8(par), 80)
	default:
		return nil, nil, nil, &UnsupportedError{"PuTTY key derivation " + k.header["Key-Derivation"] + " (save the key with Argon2id in PuTTYgen)"}
	}
	return out[:32], out[32:48], out[48:80], nil
}

func putString(b *bytes.Buffer, s []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(s)))
	b.Write(n[:])
	b.Write(s)
}

func (k *ppk) macOf(macKey, private []byte) []byte {
	var h hash.Hash
	if k.version == 2 {
		h = hmac.New(sha1.New, macKey)
	} else {
		h = hmac.New(sha256.New, macKey)
	}
	var b bytes.Buffer
	putString(&b, []byte(k.alg))
	putString(&b, []byte(k.encryption))
	putString(&b, []byte(k.comment))
	putString(&b, k.public)
	putString(&b, private)
	h.Write(b.Bytes())
	return h.Sum(nil)
}

func importPPK(data, passphrase []byte) (Key, error) {
	k, err := parsePPK(data)
	if err != nil {
		return Key{}, err
	}
	encrypted := k.encryption != "none"
	if !encrypted {
		passphrase = nil
	} else if len(passphrase) == 0 {
		pub, _ := ssh.ParsePublicKey(k.public)
		return Key{Public: pub, Encrypted: true, Comment: k.comment}, ErrPassphraseNeeded
	}
	ck, iv, mk, err := k.keys(passphrase)
	if err != nil {
		return Key{}, err
	}
	private := k.private
	if encrypted {
		if len(private) == 0 || len(private)%aes.BlockSize != 0 {
			return Key{}, ErrCorrupt
		}
		blk, _ := aes.NewCipher(ck)
		private = bytes.Clone(private)
		cipher.NewCBCDecrypter(blk, iv).CryptBlocks(private, private)
	}
	if !hmac.Equal(k.macOf(mk, private), k.mac) {
		if encrypted {
			return Key{}, ErrPassphraseWrong
		}
		return Key{}, ErrCorrupt
	}
	priv, err := privateKey(k.alg, k.public, private)
	if err != nil {
		return Key{}, err
	}
	out, err := Encode(priv, k.comment, passphrase)
	if err != nil {
		return Key{}, err
	}
	if want, err := ssh.ParsePublicKey(k.public); err != nil || !bytes.Equal(want.Marshal(), out.Public.Marshal()) {
		return Key{}, ErrCorrupt
	}
	return out, nil
}

// reader reads SSH wire strings and mpints.
type reader struct {
	b   []byte
	err bool
}

func (r *reader) str() []byte {
	if len(r.b) < 4 {
		r.err = true
		return nil
	}
	n := binary.BigEndian.Uint32(r.b)
	if uint64(n) > uint64(len(r.b)-4) {
		r.err = true
		return nil
	}
	end := 4 + int(n) // in int: 4+n in uint32 wraps near 4 GiB
	s := r.b[4:end]
	r.b = r.b[end:]
	return s
}

func (r *reader) mpint() *big.Int {
	s := r.str()
	if len(s) > 0 && s[0]&0x80 != 0 {
		r.err = true // negative: no key part is
	}
	return new(big.Int).SetBytes(s)
}

func privateKey(alg string, public, private []byte) (any, error) {
	pub, priv := &reader{b: public}, &reader{b: private}
	if string(pub.str()) != alg {
		return nil, ErrCorrupt
	}
	switch alg {
	case ssh.KeyAlgoRSA:
		e, n := pub.mpint(), pub.mpint()
		d, p, q := priv.mpint(), priv.mpint(), priv.mpint()
		if pub.err || priv.err || !e.IsInt64() || e.Int64() > 1<<31-1 {
			return nil, ErrCorrupt
		}
		key := &rsa.PrivateKey{PublicKey: rsa.PublicKey{N: n, E: int(e.Int64())}, D: d, Primes: []*big.Int{p, q}}
		if key.Validate() != nil {
			return nil, ErrCorrupt
		}
		key.Precompute()
		return key, nil
	case ssh.KeyAlgoED25519:
		a := pub.str()
		seed := priv.str()
		if pub.err || priv.err || len(seed) != ed25519.SeedSize {
			return nil, ErrCorrupt
		}
		key := ed25519.NewKeyFromSeed(seed)
		if !bytes.Equal(key.Public().(ed25519.PublicKey), a) {
			return nil, ErrCorrupt
		}
		return key, nil
	case ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521:
		curve := map[string]elliptic.Curve{"nistp256": elliptic.P256(), "nistp384": elliptic.P384(), "nistp521": elliptic.P521()}[string(pub.str())]
		pub.str() // the point, checked against the converted key's
		d := priv.mpint()
		if curve == nil || pub.err || priv.err || d.Sign() <= 0 || d.Cmp(curve.Params().N) >= 0 {
			return nil, ErrCorrupt
		}
		key, err := ecdsa.ParseRawPrivateKey(curve, d.FillBytes(make([]byte, (curve.Params().BitSize+7)/8)))
		if err != nil {
			return nil, ErrCorrupt
		}
		return key, nil
	}
	return nil, &UnsupportedError{"key type " + alg}
}
