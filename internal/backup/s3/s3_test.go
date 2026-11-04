package s3

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/backup"
)

// The example credentials of the AWS documentation.
const (
	exampleAccess = "AKIAIOSFODNN7EXAMPLE"
	exampleSecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

var exampleTime = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

// authPart is the field of an Authorization header after its name, as
// "Signature=".
func authPart(t *testing.T, auth, name string) string {
	t.Helper()
	for _, f := range strings.Split(strings.TrimPrefix(auth, "AWS4-HMAC-SHA256 "), ",") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(f), name); ok {
			return v
		}
	}
	t.Fatalf("no %s in %q", name, auth)
	return ""
}

// TestSignatureExamples signs the S3 examples of the AWS documentation on
// header authentication with a single-chunk payload.
func TestSignatureExamples(t *testing.T) {
	for _, tc := range []struct {
		name, method, url, body string
		headers                 map[string]string
		signed, sig             string
	}{
		{
			name: "get object", method: "GET", url: "https://examplebucket.s3.amazonaws.com/test.txt",
			headers: map[string]string{"Range": "bytes=0-9"},
			signed:  "host;range;x-amz-content-sha256;x-amz-date",
			sig:     "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41",
		},
		{
			name: "put object", method: "PUT", url: "https://examplebucket.s3.amazonaws.com/test$file.text", body: "Welcome to Amazon S3.",
			headers: map[string]string{"Date": "Fri, 24 May 2013 00:00:00 GMT", "X-Amz-Storage-Class": "REDUCED_REDUNDANCY"},
			signed:  "date;host;x-amz-content-sha256;x-amz-date;x-amz-storage-class",
			sig:     "98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd",
		},
		{
			name: "list objects", method: "GET", url: "https://examplebucket.s3.amazonaws.com/?max-keys=2&prefix=J",
			signed: "host;x-amz-content-sha256;x-amz-date",
			sig:    "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, tc.url, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			payload := emptyHash
			if tc.body != "" {
				payload = hexSHA([]byte(tc.body))
			}
			sign(req, payload, "us-east-1", exampleAccess, exampleSecret, exampleTime)
			auth := req.Header.Get("Authorization")
			if got := authPart(t, auth, "Credential="); got != exampleAccess+"/20130524/us-east-1/s3/aws4_request" {
				t.Errorf("credential %q", got)
			}
			if got := authPart(t, auth, "SignedHeaders="); got != tc.signed {
				t.Errorf("signed headers %q, want %q", got, tc.signed)
			}
			if got := authPart(t, auth, "Signature="); got != tc.sig {
				t.Errorf("signature %s, want %s", got, tc.sig)
			}
			if req.Header.Get("X-Amz-Date") != "20130524T000000Z" || req.Header.Get("X-Amz-Content-Sha256") != payload {
				t.Errorf("headers %v", req.Header)
			}
		})
	}
	if got := hexSHA([]byte("Welcome to Amazon S3.")); got != "44ce7dd67c959e0d3524ffac1771dfbba87d2b6b4b4e99e42034a8b803f8b072" {
		t.Fatalf("payload hash %s", got)
	}
}

func TestURLs(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    Bucket
		key  string
		want string
	}{
		{"aws default", Bucket{Bucket: "backups"}, "sshyard/a", "https://backups.s3.us-east-1.amazonaws.com/sshyard/a"},
		{"aws region", Bucket{Region: "eu-west-1", Bucket: "backups"}, "", "https://backups.s3.eu-west-1.amazonaws.com/"},
		{"aws path style", Bucket{Region: "eu-west-1", Bucket: "backups", PathStyle: true}, "k", "https://s3.eu-west-1.amazonaws.com/backups/k"},
		{"virtual hosted", Bucket{Endpoint: "https://oss-cn-hangzhou.aliyuncs.com/", Bucket: "mine"}, "p/k", "https://mine.oss-cn-hangzhou.aliyuncs.com/p/k"},
		{"path style", Bucket{Endpoint: "http://127.0.0.1:9000/base/", Bucket: "Mine_1", PathStyle: true}, "p/k", "http://127.0.0.1:9000/base/Mine_1/p/k"},
		{"path style bucket", Bucket{Endpoint: "http://127.0.0.1:9000", Bucket: "mine", PathStyle: true}, "", "http://127.0.0.1:9000/mine"},
		{"escaped", Bucket{Endpoint: "https://s3.example.com", Bucket: "mine", PathStyle: true}, "my box/a$b", "https://s3.example.com/mine/my%20box/a%24b"},
	} {
		u, err := tc.b.url(tc.key)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if u.String() != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, u, tc.want)
		}
	}
}

func TestBadEndpoint(t *testing.T) {
	for _, b := range []Bucket{
		{Endpoint: "ftp://s3.example.com", Bucket: "b"},
		{Endpoint: "https://", Bucket: "b"},
		{Endpoint: "s3.example.com", Bucket: "b"},
		{Endpoint: "https://me:secret@s3.example.com", Bucket: "b"},
		{Endpoint: "https://s3.example.com?x=1", Bucket: "b"},
		{Endpoint: "https://s3.example.com#f", Bucket: "b"},
		{Bucket: ""},
		{Bucket: "a/b"},
		{Bucket: "Upper"}, // not in a host name
		{Bucket: "b", Region: "us east"},
		{Bucket: "b", Region: "x/y"},
	} {
		if _, err := b.url(""); !errors.Is(err, ErrEndpoint) {
			t.Errorf("%+v: %v", b, err)
		}
		if err := b.Check(context.Background()); !errors.Is(err, ErrEndpoint) {
			t.Errorf("%+v: Check %v", b, err)
		}
	}
	b := Bucket{Bucket: "b"}
	for _, name := range []string{"", "..", "a/b", `a\b`} {
		if _, err := b.Get(context.Background(), name); !errors.Is(err, errName) {
			t.Errorf("Get(%q): %v", name, err)
		}
		if err := b.Put(context.Background(), name, nil); !errors.Is(err, errName) {
			t.Errorf("Put(%q): %v", name, err)
		}
		if err := b.Delete(context.Background(), name); !errors.Is(err, errName) {
			t.Errorf("Delete(%q): %v", name, err)
		}
	}
}

// fakeS3 is an S3 of one path-style bucket in memory, that checks every
// request's signature as S3 would, from what it received.
type fakeS3 struct {
	t      *testing.T
	bucket string
	page   int // keys per listing page
	mu     sync.Mutex
	keys   map[string][]byte
	lists  int
}

func (f *fakeS3) fail(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>`+code+`</Code><Message>fake `+code+`</Message></Error>`)
}

func (f *fakeS3) verify(r *http.Request, body []byte) bool {
	auth := r.Header.Get("Authorization")
	amz, payload := r.Header.Get("X-Amz-Date"), r.Header.Get("X-Amz-Content-Sha256")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential="+exampleAccess+"/") || amz == "" || payload != hexSHA(body) {
		f.t.Errorf("%s %s: auth %q date %q payload %q", r.Method, r.URL, auth, amz, payload)
		return false
	}
	signed := strings.Split(authPart(f.t, auth, "SignedHeaders="), ";")
	h := http.Header{}
	for _, name := range signed {
		if name != "host" {
			h[http.CanonicalHeaderKey(name)] = r.Header.Values(name)
		}
	}
	sig, _ := signature(r.Method, r.URL.Path, r.URL.Query(), r.Host, h, payload, "auto", exampleSecret, amz)
	if sig != authPart(f.t, auth, "Signature=") {
		f.t.Errorf("%s %s: signature does not match", r.Method, r.URL)
		return false
	}
	return true
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if !f.verify(r, body) {
		f.fail(w, http.StatusForbidden, "SignatureDoesNotMatch")
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/"+f.bucket)
	if !ok || rest != "" && rest[0] != '/' {
		f.fail(w, http.StatusNotFound, "NoSuchBucket")
		return
	}
	key := strings.TrimPrefix(rest, "/")
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case key == "" && r.Method == http.MethodGet:
		f.list(w, r)
	case r.Method == http.MethodPut:
		if r.Header.Get("Content-Type") != "application/octet-stream" {
			f.t.Errorf("put content type %q", r.Header.Get("Content-Type"))
		}
		f.keys[key] = body
	case r.Method == http.MethodGet:
		b, ok := f.keys[key]
		if !ok {
			f.fail(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		w.Write(b)
	case r.Method == http.MethodDelete:
		delete(f.keys, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		f.fail(w, http.StatusMethodNotAllowed, "MethodNotAllowed")
	}
}

// list answers ListObjectsV2 with prefix, delimiter "/" and pages of
// f.page keys, the continuation token the index of the next key.
func (f *fakeS3) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("list-type") != "2" || q.Get("delimiter") != "/" {
		f.t.Errorf("list query %v", q)
	}
	f.lists++
	prefix := q.Get("prefix")
	var keys []string
	for k := range f.keys {
		if rest, ok := strings.CutPrefix(k, prefix); ok && !strings.Contains(rest, "/") {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	from, _ := strconv.Atoi(q.Get("continuation-token"))
	n := f.page
	if mk, err := strconv.Atoi(q.Get("max-keys")); err == nil && mk < n {
		n = mk
	}
	type content struct {
		Key  string
		Size int
	}
	out := struct {
		XMLName               xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListBucketResult"`
		Contents              []content
		IsTruncated           bool
		NextContinuationToken string `xml:",omitempty"`
	}{}
	for i := from; i < len(keys) && i < from+n; i++ {
		out.Contents = append(out.Contents, content{keys[i], len(f.keys[keys[i]])})
	}
	if from+n < len(keys) {
		out.IsTruncated, out.NextContinuationToken = true, strconv.Itoa(from+n)
	}
	b, _ := xml.Marshal(out)
	w.Write(b)
}

func newFake(t *testing.T) (*fakeS3, *Bucket) {
	f := &fakeS3{t: t, bucket: "backups", page: 2, keys: map[string][]byte{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	b := &Bucket{Endpoint: srv.URL, Region: "auto", Bucket: "backups", Prefix: "/my box/sshyard", PathStyle: true,
		AccessKey: exampleAccess, SecretKey: exampleSecret, HTTP: srv.Client()}
	return f, b
}

func TestRoundTrip(t *testing.T) {
	f, b := newFake(t)
	ctx := context.Background()
	if err := b.Check(ctx); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	var names []string
	for i := range 3 {
		name := backup.Name(base.Add(time.Duration(i)*time.Hour), "ab12cd34")
		names = append(names, name)
		if err := b.Put(ctx, name, []byte("sealed "+strconv.Itoa(i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := f.keys["my box/sshyard/"+names[0]]; !ok {
		t.Fatalf("keys %q", slices.Collect(maps.Keys(f.keys)))
	}
	// What is not a backup, deeper or outside the prefix is not listed.
	f.keys["my box/sshyard/notes.txt"] = []byte("x")
	f.keys["my box/sshyard/old/"+backup.Name(base, "ab12cd34")] = []byte("x")
	f.keys["elsewhere/"+backup.Name(base, "ab12cd34")] = []byte("x")
	f.lists = 0
	objs, err := b.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f.lists < 2 {
		t.Fatalf("listed in %d pages, want several", f.lists)
	}
	if len(objs) != 3 || objs[0].Name != names[2] || objs[2].Name != names[0] || objs[0].Size != int64(len("sealed 2")) || objs[0].Device != "ab12cd34" {
		t.Fatalf("listed %+v", objs)
	}
	got, err := b.Get(ctx, names[1])
	if err != nil || string(got) != "sealed 1" {
		t.Fatalf("get %q, %v", got, err)
	}
	if err := b.Delete(ctx, names[1]); err != nil {
		t.Fatal(err)
	}
	if objs, _ := b.List(ctx); len(objs) != 2 {
		t.Fatalf("after delete %+v", objs)
	}
	if _, err := b.Get(ctx, names[1]); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("get deleted: %v", err)
	}
	other := *b
	other.Bucket = "missing"
	if err := other.Check(ctx); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("missing bucket: %v", err)
	}
}

func TestErrors(t *testing.T) {
	var status int
	var code string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, `<Error><Code>`+code+`</Code><Message>Something about `+code+`</Message><RequestId>1</RequestId></Error>`)
	}))
	defer srv.Close()
	b := &Bucket{Endpoint: srv.URL, Bucket: "b", PathStyle: true, AccessKey: exampleAccess, SecretKey: exampleSecret, HTTP: srv.Client()}
	for _, tc := range []struct {
		status int
		code   string
		is     error
	}{
		{http.StatusForbidden, "AccessDenied", backup.ErrAuth},
		{http.StatusNotFound, "NoSuchBucket", backup.ErrNotFound},
		{http.StatusBadRequest, "InvalidAccessKeyId", backup.ErrAuth},
		{http.StatusMovedPermanently, "NoSuchKey", backup.ErrNotFound},
	} {
		status, code = tc.status, tc.code
		err := b.Check(context.Background())
		if !errors.Is(err, tc.is) {
			t.Errorf("%d %s: %v is not %v", tc.status, tc.code, err, tc.is)
		}
		var se *backup.StatusError
		if !errors.As(err, &se) || se.Status != tc.status || se.Code != tc.code {
			t.Errorf("%d %s: %#v", tc.status, tc.code, err)
		}
		if msg := err.Error(); !strings.Contains(msg, tc.code) || !strings.Contains(msg, "Something about") || strings.Contains(msg, exampleSecret) {
			t.Errorf("error text %q", msg)
		}
	}
	status, code = http.StatusInternalServerError, "InternalError"
	if err := b.Check(context.Background()); errors.Is(err, backup.ErrAuth) || errors.Is(err, backup.ErrNotFound) {
		t.Errorf("500 is %v", err)
	}
}
