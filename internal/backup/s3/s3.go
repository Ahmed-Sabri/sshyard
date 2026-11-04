// Package s3 keeps backups in a bucket of Amazon S3 or of a service that
// speaks its API (Cloudflare R2, MinIO, Alibaba Cloud OSS, Tencent Cloud
// COS and others), signing each request with AWS Signature Version 4.
package s3

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Licoy/sshyard/internal/backup"
)

// ErrEndpoint reports an endpoint that is not an http or https URL with a
// host (and no user info, query or fragment), a bad region or bucket name.
var ErrEndpoint = errors.New("s3: endpoint must be an http or https URL with a host, with a valid region and bucket")

// errName reports a backup name that is not a plain file name.
var errName = errors.New("s3: bad backup name")

// maxPages bounds the pages of a listing.
const maxPages = 100

// defaultClient is the HTTP client of a Bucket without one.
var defaultClient = backup.Client(nil)

// Bucket is a folder of an S3 bucket that keeps backups.
type Bucket struct {
	// Endpoint is "" for AWS (https://s3.<region>.amazonaws.com), else the
	// URL of an S3-compatible service.
	Endpoint string
	// Region is the bucket's region; "" is us-east-1.
	Region string
	Bucket string
	// Prefix is the folder inside the bucket, as "sshyard/"; "" is none.
	Prefix string
	// PathStyle puts the bucket in the path (as MinIO wants) instead of the
	// host name.
	PathStyle bool
	AccessKey string
	SecretKey string
	// HTTP is the client requests go through; nil is backup.Client(nil).
	HTTP *http.Client
	now  func() time.Time
}

var _ backup.Remote = (*Bucket)(nil)

func (b *Bucket) region() (string, error) {
	r := strings.TrimSpace(b.Region)
	if r == "" {
		return "us-east-1", nil
	}
	for _, c := range r {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return "", ErrEndpoint
		}
	}
	return r, nil
}

// prefix is Prefix with no leading "/" and one trailing "/", "" for none.
func (b *Bucket) prefix() string {
	p := strings.Trim(strings.TrimSpace(b.Prefix), "/")
	if p == "" {
		return ""
	}
	return p + "/"
}

// bucketOK reports whether name can be the bucket: in a host name for a
// virtual-hosted bucket, in the path otherwise.
func bucketOK(name string, pathStyle bool) bool {
	if name == "" || len(name) > 255 || name[0] == '.' || name[len(name)-1] == '.' {
		return false
	}
	for _, c := range name {
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.'
		if pathStyle {
			ok = ok || c >= 'A' && c <= 'Z' || c == '_'
		}
		if !ok {
			return false
		}
	}
	return true
}

// url is where key is, the bucket itself for "".
func (b *Bucket) url(key string) (*url.URL, error) {
	region, err := b.region()
	if err != nil {
		return nil, err
	}
	if !bucketOK(b.Bucket, b.PathStyle) {
		return nil, ErrEndpoint
	}
	u := &url.URL{Scheme: "https", Host: "s3." + region + ".amazonaws.com"}
	if ep := strings.TrimSpace(b.Endpoint); ep != "" {
		e, err := url.Parse(ep)
		if err != nil || (e.Scheme != "https" && e.Scheme != "http") || e.Hostname() == "" || e.User != nil ||
			e.Opaque != "" || e.RawQuery != "" || e.ForceQuery || e.Fragment != "" {
			return nil, ErrEndpoint
		}
		u = &url.URL{Scheme: e.Scheme, Host: e.Host, Path: strings.TrimRight(e.Path, "/")}
	}
	if b.PathStyle {
		u.Path += "/" + b.Bucket
		if key != "" {
			u.Path += "/" + key
		}
	} else {
		u.Host = b.Bucket + "." + u.Host
		u.Path += "/" + key
	}
	u.RawPath = uriEncode(u.Path, false)
	return u, nil
}

// object is where the backup name is.
func (b *Bucket) object(name string) (*url.URL, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return nil, errName
	}
	return b.url(b.prefix() + name)
}

// do sends a signed request for u, with body when it has one, and returns
// a response of a 2xx status; any other status is an error.
func (b *Bucket) do(ctx context.Context, method string, u *url.URL, body []byte, contentType string) (*http.Response, error) {
	region, err := b.region()
	if err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	payload := emptyHash
	if body != nil {
		payload = hexSHA(body)
	}
	now := time.Now
	if b.now != nil {
		now = b.now
	}
	sign(req, payload, region, b.AccessKey, b.SecretKey, now())
	hc := b.HTTP
	if hc == nil {
		hc = defaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return resp, nil
	}
	defer resp.Body.Close()
	return nil, responseError(resp)
}

// The S3 error codes that say more than their HTTP status.
var (
	authCodes     = map[string]bool{"AccessDenied": true, "InvalidAccessKeyId": true, "SignatureDoesNotMatch": true}
	notFoundCodes = map[string]bool{"NoSuchBucket": true, "NoSuchKey": true}
)

// codeError is a StatusError whose S3 code makes it ErrAuth or ErrNotFound
// too, whatever its status.
type codeError struct {
	*backup.StatusError
	is error
}

func (e codeError) Unwrap() []error { return []error{e.StatusError, e.is} }

// responseError is the error of a response that is not a success: its
// status, and the code and message of the S3 error it holds.
func responseError(resp *http.Response) error {
	var x struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = xml.Unmarshal(b, &x)
	se := &backup.StatusError{Status: resp.StatusCode, Code: strings.TrimSpace(x.Code), Message: strings.TrimSpace(x.Message)}
	switch {
	case authCodes[se.Code]:
		return codeError{se, backup.ErrAuth}
	case notFoundCodes[se.Code]:
		return codeError{se, backup.ErrNotFound}
	}
	return se
}

// listResult is what ListObjectsV2 answers.
type listResult struct {
	Contents []struct {
		Key  string `xml:"Key"`
		Size int64  `xml:"Size"`
	} `xml:"Contents"`
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
}

// list asks ListObjectsV2 for one page of the keys right under the
// prefix, from token.
func (b *Bucket) list(ctx context.Context, token string, maxKeys string) (listResult, error) {
	var out listResult
	u, err := b.url("")
	if err != nil {
		return out, err
	}
	q := url.Values{"list-type": {"2"}, "prefix": {b.prefix()}, "delimiter": {"/"}}
	if token != "" {
		q.Set("continuation-token", token)
	}
	if maxKeys != "" {
		q.Set("max-keys", maxKeys)
	}
	u.RawQuery = canonicalQuery(q)
	resp, err := b.do(ctx, http.MethodGet, u, nil, "")
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	body, err := backup.ReadAll(resp.Body)
	if err != nil {
		return out, err
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return out, err
	}
	return out, nil
}

// Check lists the prefix, which needs the bucket and the credentials.
func (b *Bucket) Check(ctx context.Context) error {
	_, err := b.list(ctx, "", "1")
	return err
}

// List is the backups right under the prefix, newest first.
func (b *Bucket) List(ctx context.Context) ([]backup.Object, error) {
	var objs []backup.Object
	prefix, token := b.prefix(), ""
	for range maxPages {
		page, err := b.list(ctx, token, "")
		if err != nil {
			return nil, err
		}
		for _, c := range page.Contents {
			name, ok := strings.CutPrefix(c.Key, prefix)
			if !ok || strings.Contains(name, "/") {
				continue
			}
			if o, ok := backup.ObjectOf(name, c.Size); ok {
				objs = append(objs, o)
			}
		}
		if !page.IsTruncated || page.NextContinuationToken == "" {
			break
		}
		token = page.NextContinuationToken
	}
	backup.SortNewest(objs)
	return objs, nil
}

// Get reads the backup name, up to backup.MaxSize.
func (b *Bucket) Get(ctx context.Context, name string) ([]byte, error) {
	u, err := b.object(name)
	if err != nil {
		return nil, err
	}
	resp, err := b.do(ctx, http.MethodGet, u, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return backup.ReadAll(resp.Body)
}

// Put stores data as the backup name.
func (b *Bucket) Put(ctx context.Context, name string, data []byte) error {
	u, err := b.object(name)
	if err != nil {
		return err
	}
	if data == nil {
		data = []byte{}
	}
	resp, err := b.do(ctx, http.MethodPut, u, data, "application/octet-stream")
	if err != nil {
		return err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.Body.Close()
}

// Delete removes the backup name.
func (b *Bucket) Delete(ctx context.Context, name string) error {
	u, err := b.object(name)
	if err != nil {
		return err
	}
	resp, err := b.do(ctx, http.MethodDelete, u, nil, "")
	if err != nil {
		return err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.Body.Close()
}
