package assets

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func checkGroupOrder(t *testing.T, s *Store, want ...string) {
	t.Helper()
	if got := s.Groups(); !slices.Equal(got, want) {
		t.Fatalf("group order %q, want %q", got, want)
	}
}

func TestGroupOrderPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Implicit groups keep their old alphabetical order until a move.
	for _, g := range []string{"C", "A", "B"} {
		if _, err := s.Add(Host{Address: g, User: "u", Group: g}); err != nil {
			t.Fatal(err)
		}
	}
	checkGroupOrder(t, s, "A", "B", "C")
	for _, tc := range []struct {
		from, to string
		after    bool
		want     []string
	}{
		{"C", "A", false, []string{"C", "A", "B"}},
		{"C", "B", true, []string{"A", "B", "C"}},
		{"B", "C", true, []string{"A", "C", "B"}},
		{"A", "A", false, []string{"A", "C", "B"}},
	} {
		if err := s.MoveGroup(tc.from, tc.to, tc.after); err != nil {
			t.Fatal(err)
		}
		checkGroupOrder(t, s, tc.want...)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	checkGroupOrder(t, s, "A", "C", "B")
	if err := s.RenameGroup("C", "Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGroup("0-empty"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(Host{Address: "new", User: "u", Group: "0-host"}); err != nil {
		t.Fatal(err)
	}
	checkGroupOrder(t, s, "A", "Z", "B", "0-empty", "0-host")
	if err := s.DeleteGroup("Z"); err != nil {
		t.Fatal(err)
	}
	checkGroupOrder(t, s, "A", "B", "0-empty", "0-host")
	for _, pair := range [][2]string{{"missing", "A"}, {"A", "missing"}} {
		if err := s.MoveGroup(pair[0], pair[1], false); !errors.Is(err, ErrNotFound) {
			t.Fatalf("invalid move: %v", err)
		}
	}
}

func TestGroupOrderImplicitAndSnapshot(t *testing.T) {
	s, _ := Open("")
	for _, g := range []string{"A", "B"} {
		if _, err := s.Add(Host{Address: g, User: "u", Group: g}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MoveGroup("B", "A", false); err != nil {
		t.Fatal(err)
	}
	for _, h := range s.InGroup("B") {
		if err := s.SetGroup(h.ID, ""); err != nil {
			t.Fatal(err)
		}
	}
	checkGroupOrder(t, s, "A") // Dragging did not make B a permanent empty group.
	if err := s.AddGroup("C"); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveGroup("C", "A", false); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	restored, _ := Open(filepath.Join(t.TempDir(), "restored.json"))
	if err := restored.AddGroup("local"); err != nil {
		t.Fatal(err)
	}
	if err := restored.Restore(snap); err != nil {
		t.Fatal(err)
	}
	checkGroupOrder(t, restored, "C", "A", "local")
	reopened, err := Open(restored.path)
	if err != nil {
		t.Fatal(err)
	}
	checkGroupOrder(t, reopened, "C", "A", "local")
	snap.GroupOrder[0] = "changed"
	checkGroupOrder(t, s, "C", "A")
	before := restored.Snapshot()
	for _, order := range [][]string{{"A", "A"}, {"unknown"}} {
		if err := restored.Restore(Snapshot{Groups: []string{"new"}, GroupOrder: order}); err == nil {
			t.Fatal("invalid order accepted")
		}
		if !reflect.DeepEqual(before, restored.Snapshot()) {
			t.Fatal("invalid restore changed the store")
		}
	}
}

func TestGroupOrderWriteRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.json")
	s, _ := Open(path)
	for _, g := range []string{"A", "B"} {
		if err := s.AddGroup(g); err != nil {
			t.Fatal(err)
		}
	}
	before := s.Snapshot()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	for _, fn := range []func() error{
		func() error { return s.MoveGroup("B", "A", false) },
		func() error { return s.RenameGroup("A", "Z") },
		func() error { return s.DeleteGroup("A") },
		func() error { return s.AddGroup("C") },
		func() error { return s.Restore(Snapshot{GroupOrder: []string{"B", "A"}}) },
	} {
		if err := fn(); err == nil {
			t.Fatal("write unexpectedly succeeded")
		}
		if !reflect.DeepEqual(before, s.Snapshot()) {
			t.Fatal("failed write changed the store")
		}
	}
}
