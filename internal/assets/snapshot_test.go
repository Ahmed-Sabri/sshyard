package assets

import (
	"path/filepath"
	"testing"
)

// backupOf is a store with a credential, a proxy, a jump host, a host
// using all three and a forward on it, and the snapshot of it.
func backupOf(t *testing.T) (*Store, Snapshot) {
	t.Helper()
	s, _ := Open("")
	cred, err := s.AddCredential(Credential{ID: NewID(), Name: "deploy", Kind: CredPassword})
	if err != nil {
		t.Fatal(err)
	}
	px, err := s.AddProxy(Proxy{ID: NewID(), Name: "office", Kind: ProxySOCKS5, Address: "proxy.lan"})
	if err != nil {
		t.Fatal(err)
	}
	bastion, _ := s.Add(Host{Name: "bastion", Address: "10.0.0.1", User: "ops"})
	web, err := s.Add(Host{Name: "web", Address: "10.0.0.5", User: "deploy", Group: "Prod", Auth: AuthCredential, CredentialID: cred.ID, ProxyID: px.ID, JumpChain: []string{bastion.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddForward(Forward{ID: NewID(), Name: "db", Kind: ForwardLocal, HostID: web.ID, ListenPort: 5432, TargetAddr: "db", TargetPort: 5432}); err != nil {
		t.Fatal(err)
	}
	s.AddGroup("Empty")
	return s, s.Snapshot()
}

func TestRestoreIntoEmpty(t *testing.T) {
	_, snap := backupOf(t)
	path := filepath.Join(t.TempDir(), "hosts.json")
	s, _ := Open(path)
	if err := s.Restore(snap); err != nil {
		t.Fatal(err)
	}
	re, _ := Open(path)
	got := re.Snapshot()
	if len(got.Hosts) != 2 || len(got.Credentials) != 1 || len(got.Proxies) != 1 || len(got.Forwards) != 1 {
		t.Fatalf("restored %+v", got)
	}
	for _, h := range snap.Hosts {
		r, ok := re.Get(h.ID)
		if !ok || r.Name != h.Name || !r.Created.Equal(h.Created) || len(r.JumpChain) != len(h.JumpChain) {
			t.Fatalf("host %s restored as %+v", h.Name, r)
		}
	}
	if gs := re.Groups(); len(gs) != 2 || gs[0] != "Empty" || gs[1] != "Prod" {
		t.Fatalf("groups %v", gs)
	}
}

// TestRestoreMerges: entries of the same ID are replaced, others here
// stay, and a name another credential has here is made free.
func TestRestoreMerges(t *testing.T) {
	_, snap := backupOf(t)
	s, _ := Open("")
	mine, _ := s.Add(Host{Name: "mine", Address: "192.168.1.2", User: "me"})
	s.AddCredential(Credential{ID: NewID(), Name: "Deploy", Kind: CredKey})
	if err := s.Restore(snap); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(mine.ID); !ok {
		t.Fatal("a host not in the backup was removed")
	}
	cr, _ := s.Credential(snap.Credentials[0].ID)
	if cr.Name != "deploy (2)" {
		t.Fatalf("clashing name became %q", cr.Name)
	}
	// Restoring again replaces, it does not duplicate, and keeps the name.
	if err := s.Restore(snap); err != nil {
		t.Fatal(err)
	}
	if n := len(s.Credentials()); n != 2 {
		t.Fatalf("%d credentials after a second restore", n)
	}
	if cr, _ := s.Credential(snap.Credentials[0].ID); cr.Name != "deploy (2)" {
		t.Fatalf("second restore renamed to %q", cr.Name)
	}
	if s.Len() != 3 {
		t.Fatalf("%d hosts", s.Len())
	}
}

// TestRestoreAllOrNothing: a broken entry, or one referring to what is
// missing, changes nothing.
func TestRestoreAllOrNothing(t *testing.T) {
	_, snap := backupOf(t)
	for name, broken := range map[string]func(sn *Snapshot){
		"missing credential": func(sn *Snapshot) { sn.Credentials = nil },
		"missing jump host": func(sn *Snapshot) {
			for i, h := range sn.Hosts {
				if h.Name == "bastion" {
					sn.Hosts = append(sn.Hosts[:i:i], sn.Hosts[i+1:]...)
					break
				}
			}
		},
		"bad id":       func(sn *Snapshot) { sn.Proxies[0].ID = "../x" },
		"invalid host": func(sn *Snapshot) { sn.Hosts[0].Address = "" },
		"forward host": func(sn *Snapshot) { sn.Forwards[0].HostID = "gone" },
	} {
		t.Run(name, func(t *testing.T) {
			sn := Snapshot{Hosts: append([]Host(nil), snap.Hosts...), Credentials: append([]Credential(nil), snap.Credentials...), Proxies: append([]Proxy(nil), snap.Proxies...), Forwards: append([]Forward(nil), snap.Forwards...)}
			broken(&sn)
			s, _ := Open("")
			keep, _ := s.Add(Host{Name: "keep", Address: "h", User: "u"})
			if err := s.Restore(sn); err == nil {
				t.Fatal("restored")
			}
			got := s.Snapshot()
			if len(got.Hosts) != 1 || got.Hosts[0].ID != keep.ID || len(got.Credentials)+len(got.Proxies)+len(got.Forwards) != 0 {
				t.Fatalf("partly restored: %+v", got)
			}
		})
	}
}

func TestValidID(t *testing.T) {
	for id, want := range map[string]bool{NewID(): true, "abc-DEF_9": true, "": false, "a/b": false, "a#0": false, "é": false} {
		if ValidID(id) != want {
			t.Errorf("ValidID(%q) = %v", id, !want)
		}
	}
}
