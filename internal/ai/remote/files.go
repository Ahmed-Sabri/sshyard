package remote

import (
	"context"

	"github.com/Licoy/sshyard/internal/sftpsvc"
	"golang.org/x/crypto/ssh"
)

// Files is the SFTP session a write tool uses. Write takes the Version
// Read returned and refuses with sftpsvc.ErrChanged when the file's
// size, mtime, or content sum changed. A zero Version overwrites; pass
// the Version from Read.
type Files struct {
	c *sftpsvc.Client
}

// OpenFiles starts SFTP on the host's pooled connection. Closing Files
// does not close the connection.
func OpenFiles(client *ssh.Client) (*Files, error) {
	return OpenFilesContext(context.Background(), client)
}

// OpenFilesContext cancels subsystem negotiation without closing shared SSH.
func OpenFilesContext(ctx context.Context, client *ssh.Client) (*Files, error) {
	c, err := sftpsvc.NewContext(ctx, client)
	if err != nil {
		return nil, err
	}
	return &Files{c: c}, nil
}

// Close ends the SFTP session.
func (f *Files) Close() error { return f.c.Close() }

// Read returns the file and the version a later Write must pass.
func (f *Files) Read(path string) ([]byte, sftpsvc.Version, error) {
	return f.ReadContext(context.Background(), path, sftpsvc.DefaultReadLimit)
}

// Write replaces path with data when it is still was.
func (f *Files) Write(path string, data []byte, was sftpsvc.Version) (sftpsvc.Version, error) {
	p, err := f.ResolvePath(context.Background(), path)
	if err != nil {
		return sftpsvc.Version{}, err
	}
	return f.c.WriteFileNoLinks(p, data, was)
}

// ResolvePath checks every component before policy authorization.
func (f *Files) ResolvePath(ctx context.Context, p string) (string, error) {
	return f.c.ResolvePath(ctx, p)
}

// ReadContext bounds allocations and interrupts only this read's SFTP session.
func (f *Files) ReadContext(ctx context.Context, p string, maxBytes int64) ([]byte, sftpsvc.Version, error) {
	p, err := f.ResolvePath(ctx, p)
	if err != nil {
		return nil, sftpsvc.Version{}, err
	}
	return f.c.ReadFileNoLinksContext(ctx, p, maxBytes)
}
