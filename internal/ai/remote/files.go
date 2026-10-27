package remote

import (
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
	c, err := sftpsvc.New(client)
	if err != nil {
		return nil, err
	}
	return &Files{c: c}, nil
}

// Close ends the SFTP session.
func (f *Files) Close() error { return f.c.Close() }

// Read returns the file and the version a later Write must pass.
func (f *Files) Read(path string) ([]byte, sftpsvc.Version, error) {
	return f.c.ReadFile(path)
}

// Write replaces path with data when it is still was.
func (f *Files) Write(path string, data []byte, was sftpsvc.Version) (sftpsvc.Version, error) {
	return f.c.WriteFile(path, data, was)
}
