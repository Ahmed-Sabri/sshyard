package sshconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestParse(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".ssh", "config")
	write(t, cfg, `# defaults first
Include conf.d/*.conf
Host jump
  HostName 127.0.0.1
  Port 2222
  User demo

Host app
  HostName=127.0.0.1
  Port 2223
  ProxyJump jump
  IdentityFile ~/.ssh/id_app
  LocalForward 8080 localhost:80
  ProxyCommand nc %h %p

Host *.corp
  User corp

Host "db" !nope
  HostName db.internal
  IdentityFile %d/.ssh/id_%r

Match host foo
  User nobody

Host *
  User fallback
  Port 22
  ServerAliveInterval 30
  IdentityFile ~/.ssh/id_default
`)
	write(t, filepath.Join(home, ".ssh", "conf.d", "a.conf"), "Host web\n  HostName web.example.com\n  User www\n")
	c, err := Parse(cfg, Env{Home: home, LocalUser: "me"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range c.Entries {
		names = append(names, e.Alias)
	}
	if want := []string{"web", "jump", "app", "*.corp", "db"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("entries %v, want %v", names, want)
	}
	byName := map[string]Entry{}
	for _, e := range c.Entries {
		byName[e.Alias] = e
	}
	ssh := filepath.Join(home, ".ssh")
	if e := byName["web"]; e.HostName != "web.example.com" || e.User != "www" || e.Port != 22 ||
		!reflect.DeepEqual(e.IdentityFiles, []string{filepath.Join(ssh, "id_default")}) {
		t.Errorf("web %+v", e)
	}
	if e := byName["web"]; e.File != filepath.Join(ssh, "conf.d", "a.conf") || e.Line != 1 {
		t.Errorf("web place %s:%d", e.File, e.Line)
	}
	if e := byName["jump"]; e.HostName != "127.0.0.1" || e.Port != 2222 || e.User != "demo" || e.ProxyJump != nil {
		t.Errorf("jump %+v", e)
	}
	e := byName["app"]
	if e.Port != 2223 || e.User != "fallback" || !reflect.DeepEqual(e.ProxyJump, []Hop{{Host: "jump"}}) {
		t.Errorf("app %+v", e)
	}
	if !reflect.DeepEqual(e.IdentityFiles, []string{filepath.Join(ssh, "id_app"), filepath.Join(ssh, "id_default")}) {
		t.Errorf("app keys %v", e.IdentityFiles)
	}
	if want := []string{"LocalForward", "ProxyCommand", "ServerAliveInterval"}; !reflect.DeepEqual(e.Unsupported, want) {
		t.Errorf("app unsupported %v, want %v", e.Unsupported, want)
	}
	if e := byName["*.corp"]; !e.Pattern || e.Line != 16 {
		t.Errorf("*.corp %+v", e)
	}
	if e := byName["db"]; e.HostName != "db.internal" || e.IdentityFiles[0] != filepath.Join(ssh, "id_fallback") {
		t.Errorf("db %+v", e)
	}
	// A name on no Host line still gets the defaults and the patterns.
	if e := c.Resolve("x.corp"); e.User != "corp" || e.HostName != "x.corp" {
		t.Errorf("x.corp %+v", e)
	}
	if e := c.Resolve("nope"); e.HostName != "nope" {
		t.Errorf("negated %+v", e)
	}
}

func TestParseMissing(t *testing.T) {
	if _, err := Parse(filepath.Join(t.TempDir(), "config"), Env{}); !os.IsNotExist(err) {
		t.Fatalf("err %v", err)
	}
}

func TestIncludeLoop(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".ssh", "config")
	write(t, cfg, "Include config\n")
	if _, err := Parse(cfg, Env{Home: home}); err == nil {
		t.Fatal("a looping include parsed")
	}
}

func TestIncludeInHostBlock(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".ssh", "config")
	write(t, cfg, "Host a\n  Include extra\n  Port 2200\nHost b\n  HostName bee\n")
	write(t, filepath.Join(home, ".ssh", "extra"), "User inc\n")
	c, err := Parse(cfg, Env{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if e := c.Resolve("a"); e.User != "inc" || e.Port != 2200 {
		t.Errorf("a %+v", e)
	}
	if e := c.Resolve("b"); e.User != "" || e.Port != 0 || e.HostName != "bee" {
		t.Errorf("b %+v", e)
	}
}

func TestSplit(t *testing.T) {
	for in, want := range map[string][]string{
		"Host a b":                {"Host", "a", "b"},
		"  HostName=example  ":    {"HostName", "example"},
		"User = bob # comment":    {"User", "bob"},
		`IdentityFile "~/my key"`: {"IdentityFile", "~/my key"},
		"# only a comment":        nil,
	} {
		k, args, err := split(in)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		if k != "" {
			got = append([]string{k}, args...)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("split(%q) = %q, want %q", in, got, want)
		}
	}
	if _, _, err := split(`User "open`); err == nil {
		t.Error("an open quote split")
	}
}

func TestParseJump(t *testing.T) {
	got := ParseJump("bastion, ops@10.0.0.1:2200,ssh://[::1]:22")
	want := []Hop{{Host: "bastion"}, {User: "ops", Host: "10.0.0.1", Port: 2200}, {Host: "::1", Port: 22}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	if got[2].String() != "[::1]:22" || got[1].String() != "ops@10.0.0.1:2200" {
		t.Errorf("strings %s %s", got[2], got[1])
	}
}

func TestParseQuick(t *testing.T) {
	for in, want := range map[string]Hop{
		"demo@127.0.0.1:2222":         {User: "demo", Host: "127.0.0.1", Port: 2222},
		"example.com":                 {Host: "example.com"},
		"ssh -p 2222 user@host":       {User: "user", Host: "host", Port: 2222},
		"ssh user@host -p2222":        {User: "user", Host: "host", Port: 2222},
		"ssh -A -l root -C box":       {User: "root", Host: "box"},
		"ssh://u@h:2200":              {User: "u", Host: "h", Port: 2200},
		"root@fe80::1":                {User: "root", Host: "fe80::1"},
		"[::1]:2022":                  {Host: "::1", Port: 2022},
		"ssh -vvv -p 22 alice@server": {User: "alice", Host: "server", Port: 22},
	} {
		got, err := ParseQuick(in)
		if err != nil || got != want {
			t.Errorf("ParseQuick(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "ssh", "ssh -p", "ssh -p 0 h", "ssh -i key h", "a b", "user@:22"} {
		if _, err := ParseQuick(in); err == nil {
			t.Errorf("ParseQuick(%q) took it", in)
		}
	}
}
