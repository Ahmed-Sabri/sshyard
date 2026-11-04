package history

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/keychain"
)

type unreadableSealer struct {
	Sealer
	seals int
}

func (s *unreadableSealer) Unseal(string, []byte, []byte) ([]byte, error) {
	return nil, keychain.ErrLocked
}
func (s *unreadableSealer) Seal(purpose string, plain, aad []byte) ([]byte, error) {
	s.seals++
	return s.Sealer.Seal(purpose, plain, aad)
}

func TestSavePreservesExistingFilesWhenIndexCannotBeRead(t *testing.T) {
	for _, failure := range []string{"damaged", "locked"} {
		t.Run(failure, func(t *testing.T) {
			st, _ := store(t)
			c := conv("host", "original", time.Now())
			if err := st.Save(c); err != nil {
				t.Fatal(err)
			}
			dir := st.hostDir(c.Host)
			indexPath, conversationPath := filepath.Join(dir, "index"), filepath.Join(dir, c.ID)
			want := keychain.ErrCorrupt
			var blocked *unreadableSealer
			if failure == "damaged" {
				if err := os.WriteFile(indexPath, []byte("damaged ciphertext"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				blocked = &unreadableSealer{Sealer: st.s}
				st.s = blocked
				want = keychain.ErrLocked
			}
			beforeIndex, err := os.ReadFile(indexPath)
			if err != nil {
				t.Fatal(err)
			}
			beforeConversation, err := os.ReadFile(conversationPath)
			if err != nil {
				t.Fatal(err)
			}
			c.Title = "changed"
			if err := st.Save(c); !errors.Is(err, want) {
				t.Fatalf("save failure: %v", err)
			}
			for path, before := range map[string][]byte{indexPath: beforeIndex, conversationPath: beforeConversation} {
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(after, before) {
					t.Fatalf("save replaced existing encrypted file %s: %v", filepath.Base(path), err)
				}
			}
			if blocked != nil && blocked.seals != 0 {
				t.Fatal("save encrypted data before reading locked index")
			}
			fresh := conv(c.Host, "new conversation", time.Now())
			if err := st.Save(fresh); !errors.Is(err, want) {
				t.Fatalf("new conversation failure: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, fresh.ID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed save created orphan conversation: %v", err)
			}
		})
	}
}
