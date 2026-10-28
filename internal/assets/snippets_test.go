package assets

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSnippetValidation(t *testing.T) {
	for _, tc := range []struct{ content, reason string }{
		{" \r\n\t", "required"}, {string([]byte{255}), "invalid"}, {strings.Repeat("a", (64<<10)+1), "tooLong"},
		{"a\x00b", "invalid"}, {"a\x1bb", "invalid"}, {"a\x7fb", "invalid"}, {" a\r\n\tb ", ""}, {strings.Repeat("a", 64<<10), ""},
	} {
		err := ValidateSnippetContent(tc.content)
		if tc.reason == "" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != "snippetContent" || fe.Reason != tc.reason {
			t.Fatalf("reason %s: %v", tc.reason, err)
		}
	}
	for _, tc := range []struct {
		n             Snippet
		field, reason string
	}{
		{Snippet{}, "snippetName", "required"},
		{Snippet{Name: strings.Repeat("界", 81)}, "snippetName", "tooLong"},
		{Snippet{Name: "a", Group: strings.Repeat("界", 81)}, "snippetGroup", "tooLong"},
		{Snippet{Name: "a\nb"}, "snippetName", "invalid"},
		{Snippet{Name: "a", Group: "a\tb"}, "snippetGroup", "invalid"},
		{Snippet{Name: string([]byte{255})}, "snippetName", "invalid"},
	} {
		var fe *FieldError
		if err := tc.n.Validate(); !errors.As(err, &fe) || fe.Field != tc.field || fe.Reason != tc.reason {
			t.Fatalf("%+v: %v", tc.n, err)
		}
	}
}

func TestSnippetStorePersistenceAndSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.json")
	// An existing version 2 file remains readable without snippets or migration.
	if err := os.WriteFile(path, []byte(`{"version":2,"hosts":[{"id":"old-host","name":"old","address":"example.test","user":"user","auth":"ask","port":22}],"groups":["existing"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if SchemaVersion != 2 || len(s.Discarded()) != 0 || len(s.Groups()) != 1 || s.Len() != 1 {
		t.Fatal("version 2 data discarded")
	}
	n, err := s.AddSnippet(Snippet{ID: "n", Name: " name ", Group: " group ", SendEnter: true})
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "name" || n.Group != "group" {
		t.Fatal("metadata not normalized")
	}
	if _, err := s.AddSnippet(n); !errors.Is(err, ErrExists) {
		t.Fatal(err)
	}
	if _, err := s.SaveSnippet(Snippet{ID: "missing", Name: "name"}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	for _, n := range []Snippet{{ID: "z", Name: "z"}, {ID: "a", Name: "A"}, {ID: "g", Name: "a", Group: "GROUP"}} {
		if _, err := s.AddSnippet(n); err != nil {
			t.Fatal(err)
		}
	}
	got := s.Snippets()
	if got[0].ID != "a" || got[1].ID != "z" || got[2].ID != "g" || got[3].ID != "n" {
		t.Fatalf("sort: %+v", got)
	}
	got[0].Name = "mutated"
	if n, _ := s.Snippet("a"); n.Name != "A" {
		t.Fatal("list aliases store")
	}
	snap := s.Snapshot()
	snap.Snippets[0].Name = "other"
	if n, _ := s.Snippet("n"); n.Name != "name" {
		t.Fatal("snapshot aliases store")
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Snippets(), s2.Snippets()) {
		t.Fatal("metadata round trip")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(file["snippets"]), "content") {
		t.Fatal("body field in metadata")
	}
	if err := s.Restore(Snapshot{Snippets: []Snippet{{ID: "n", Name: "replaced"}, {ID: "new", Name: "new"}}}); err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	for _, bad := range []Snippet{{ID: "../bad", Name: "bad"}, {ID: "bad", Name: ""}} {
		if err := s.Restore(Snapshot{Snippets: []Snippet{{ID: "n", Name: "should roll back"}, bad}}); err == nil {
			t.Fatal("invalid snapshot accepted")
		}
		if !reflect.DeepEqual(before, s.Snapshot()) {
			t.Fatal("restore failed to roll back")
		}
	}
	if err := s.DeleteSnippet("new"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Snippet("new"); ok {
		t.Fatal("not deleted")
	}
}

func TestSnippetMetadataWriteRollback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.SaveSnippet(Snippet{Name: "original"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	n.Name = "changed"
	for _, fn := range []func() error{
		func() error { _, err := s.SaveSnippet(n); return err },
		func() error { _, err := s.AddSnippet(Snippet{ID: "new", Name: "new"}); return err },
		func() error { return s.DeleteSnippet(n.ID) },
		func() error { return s.Restore(Snapshot{Snippets: []Snippet{{ID: "restore", Name: "restore"}}}) },
	} {
		if err := fn(); err == nil {
			t.Fatal("write unexpectedly succeeded")
		}
		if !reflect.DeepEqual(before, s.Snapshot()) {
			t.Fatal("metadata not rolled back")
		}
	}
}
