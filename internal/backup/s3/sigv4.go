package s3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// emptyHash is the SHA-256 of no payload, as hex.
const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// amzTime is how Signature Version 4 writes a time.
const amzTime = "20060102T150405Z"

func hexSHA(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hmacSHA(key []byte, s string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return m.Sum(nil)
}

// uriEncode escapes s as Signature Version 4 does: every byte but the
// unreserved ones, keeping "/" unless slash.
func uriEncode(s string, slash bool) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !slash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&15])
		}
	}
	return b.String()
}

// canonicalQuery is q escaped and sorted, as signed and as sent.
func canonicalQuery(q url.Values) string {
	pairs := make([]string, 0, len(q))
	for k, vs := range q {
		for _, v := range vs {
			pairs = append(pairs, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	slices.Sort(pairs)
	return strings.Join(pairs, "&")
}

// signature is the Signature Version 4 signature of a request to S3 in
// region: its method, unescaped path, query, host and headers (every one
// is signed), with the payload's hash, made at amzDate by secret. signed
// is the names of the headers signed.
func signature(method, path string, query url.Values, host string, h http.Header, payload, region, secret, amzDate string) (sig, signed string) {
	values := map[string]string{"host": host}
	for k, vs := range h {
		name := strings.ToLower(k)
		if name == "authorization" {
			continue
		}
		clean := make([]string, len(vs))
		for i, v := range vs {
			clean[i] = strings.Join(strings.Fields(v), " ")
		}
		values[name] = strings.Join(clean, ",")
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	var headers strings.Builder
	for _, name := range names {
		headers.WriteString(name + ":" + values[name] + "\n")
	}
	signed = strings.Join(names, ";")
	if path == "" {
		path = "/"
	}
	creq := strings.Join([]string{method, uriEncode(path, false), canonicalQuery(query), headers.String(), signed, payload}, "\n")
	day := amzDate[:8]
	scope := day + "/" + region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hexSHA([]byte(creq))
	key := hmacSHA([]byte("AWS4"+secret), day)
	key = hmacSHA(key, region)
	key = hmacSHA(key, "s3")
	key = hmacSHA(key, "aws4_request")
	return hex.EncodeToString(hmacSHA(key, toSign)), signed
}

// sign signs req, whose payload hashes to payload, for region at t: it
// sets X-Amz-Date, X-Amz-Content-Sha256 and Authorization. The headers
// set before are signed with them.
func sign(req *http.Request, payload, region, access, secret string, t time.Time) {
	amzDate := t.UTC().Format(amzTime)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payload)
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	sig, signed := signature(req.Method, req.URL.Path, req.URL.Query(), host, req.Header, payload, region, secret, amzDate)
	scope := amzDate[:8] + "/" + region + "/s3/aws4_request"
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+access+"/"+scope+",SignedHeaders="+signed+",Signature="+sig)
}
