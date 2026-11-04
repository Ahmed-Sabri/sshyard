// Package webdav keeps backups in a folder on a WebDAV server, through
// gowebdav.
package webdav

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/Licoy/sshyard/internal/backup"
	"github.com/studio-b12/gowebdav"
)

// ErrURL reports a server URL that is not http(s) with a host, or that
// carries user info, a query or a fragment.
var ErrURL = errors.New("webdav: URL must be http or https with a host")

// Folder is a folder of a WebDAV server that keeps backups.
type Folder struct {
	// URL is the server's WebDAV root, as
	// https://cloud.example.com/remote.php/dav/files/me/.
	URL string
	// Dir is the folder under URL, "" for URL itself.
	Dir            string
	User, Password string
	// HTTP gives the transport and the time limit; nil is backup.Client(nil).
	HTTP *http.Client
}

var _ backup.Remote = (*Folder)(nil)

// CheckURL checks a server URL as Folder takes it.
func CheckURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, ErrURL
	}
	return u, nil
}

// client is a gowebdav client for one call: its requests carry ctx, and
// none leaves the server's scheme, host and port, a redirect included.
func (f *Folder) client(ctx context.Context) (*gowebdav.Client, error) {
	u, err := CheckURL(f.URL)
	if err != nil {
		return nil, err
	}
	hc := f.HTTP
	if hc == nil {
		hc = backup.Client(nil)
	}
	rt := hc.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	c := gowebdav.NewClient(u.String(), f.User, f.Password)
	c.SetTransport(scoped{ctx: ctx, rt: rt, scheme: u.Scheme, host: u.Host})
	timeout := hc.Timeout
	if timeout == 0 {
		timeout = backup.Timeout
	}
	c.SetTimeout(timeout)
	return c, nil
}

// scoped sends a call's requests with its context, to its server only.
type scoped struct {
	ctx          context.Context
	rt           http.RoundTripper
	scheme, host string
}

func (s scoped) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != s.scheme || !strings.EqualFold(r.URL.Host, s.host) {
		if r.Body != nil {
			r.Body.Close()
		}
		return nil, fmt.Errorf("webdav: refusing request to %s://%s", r.URL.Scheme, r.URL.Host)
	}
	return s.rt.RoundTrip(r.WithContext(s.ctx))
}

// dir is the folder's path on the server, from its root.
func (f *Folder) dir() string { return path.Clean("/" + strings.TrimSpace(f.Dir)) }

// file is the path of a backup named name, which must be a plain name.
func (f *Folder) file(name string) (string, error) {
	if _, _, ok := backup.Parse(name); !ok {
		return "", fmt.Errorf("webdav: not a backup name: %q", name)
	}
	return path.Join(f.dir(), name), nil
}

// statusErr makes gowebdav's status errors backup.StatusError.
func statusErr(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		var se gowebdav.StatusError
		if errors.As(pe.Err, &se) {
			return &backup.StatusError{Status: se.Status}
		}
	}
	return err
}

// Check lists the folder, or the server's root while the folder is not
// made yet.
func (f *Folder) Check(ctx context.Context) error {
	c, err := f.client(ctx)
	if err != nil {
		return err
	}
	_, err = c.ReadDir(f.dir())
	if gowebdav.IsErrNotFound(err) {
		_, err = c.ReadDir("/")
	}
	return statusErr(err)
}

// List is the backups in the folder, none while it is not made.
func (f *Folder) List(ctx context.Context) ([]backup.Object, error) {
	c, err := f.client(ctx)
	if err != nil {
		return nil, err
	}
	fis, err := c.ReadDir(f.dir())
	if gowebdav.IsErrNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, statusErr(err)
	}
	var out []backup.Object
	for _, fi := range fis {
		if o, ok := backup.ObjectOf(fi.Name(), fi.Size()); ok && !fi.IsDir() {
			out = append(out, o)
		}
	}
	backup.SortNewest(out)
	return out, nil
}

// Get reads the backup name.
func (f *Folder) Get(ctx context.Context, name string) ([]byte, error) {
	p, err := f.file(name)
	if err != nil {
		return nil, err
	}
	c, err := f.client(ctx)
	if err != nil {
		return nil, err
	}
	rc, err := c.ReadStream(p)
	if err != nil {
		return nil, statusErr(err)
	}
	defer rc.Close()
	return backup.ReadAll(rc)
}

// Put writes the backup name, making the folder when it is missing.
func (f *Folder) Put(ctx context.Context, name string, data []byte) error {
	p, err := f.file(name)
	if err != nil {
		return err
	}
	c, err := f.client(ctx)
	if err != nil {
		return err
	}
	return statusErr(c.Write(p, data, 0o600))
}

// Delete removes the backup name.
func (f *Folder) Delete(ctx context.Context, name string) error {
	p, err := f.file(name)
	if err != nil {
		return err
	}
	c, err := f.client(ctx)
	if err != nil {
		return err
	}
	return statusErr(c.Remove(p))
}
