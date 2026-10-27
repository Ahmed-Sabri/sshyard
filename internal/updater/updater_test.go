package updater

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	good := map[string]string{"1.2.3": "1.2.3", "v0.1.0": "0.1.0", "1.0.0-beta.2+exp.sha": "1.0.0-beta.2", " 10.20.30 ": "10.20.30"}
	for in, want := range good {
		v, err := ParseVersion(in)
		if err != nil || v.String() != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %s", in, v, err, want)
		}
	}
	for _, in := range []string{"", "1.2", "1.2.3.4", "a.b.c", "01.2.3", "1.2.3-", "1.2.3-a..b", "-1.2.3"} {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) succeeded", in)
		}
	}
}

func TestCompare(t *testing.T) {
	// Ascending by semver precedence (semver.org §11).
	order := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0", "10.0.0"}
	for i := range order {
		for j := range order {
			want := cmpInt(i, j)
			if got := Compare(order[i], order[j]); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	if Compare("v1.2.3", "1.2.3+build") != 0 {
		t.Error("v prefix and build metadata must be ignored")
	}
	if Compare("garbage", "0.0.1") != -1 || Compare("0.0.1", "garbage") != 1 {
		t.Error("unparsable versions sort first")
	}
}

func TestManifest(t *testing.T) {
	const doc = `{"version":"0.2.0","notes":"- Faster","date":"2026-10-01T10:00:00Z",
	 "url":"https://github.com/Licoy/sshyard/releases/download/v0.2.0/SSHYard-0.2.0-linux-amd64.tar.gz",
	 "size":1234,"signature":"c2ln","deltas":[{"from":"0.1.0","url":"https://example.com/d.delta","size":10,"signature":"eA=="}]}`
	m, err := ParseManifest(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "0.2.0" || m.Size != 1234 || len(m.Deltas) != 1 || m.Deltas[0].From != "0.1.0" {
		t.Errorf("parsed %+v", m)
	}
	if !m.Newer("0.1.0") || m.Newer("0.2.0") || m.Newer("1.0.0") {
		t.Error("Newer")
	}
	bad := []string{
		`{}`,
		`{"version":"0.2.0","url":"https://x/y","signature":""}`,
		`{"version":"two","url":"https://x/y","signature":"s"}`,
		`{"version":"0.2.0","url":"http://evil.example/y","signature":"s"}`,
		`{"version":"0.2.0","url":"https://x/y","signature":"s","date":"yesterday"}`,
		`{"version":"0.2.0","url":"https://x/y","signature":"s","size":-1}`,
		`not json`,
	}
	for _, b := range bad {
		if _, err := ParseManifest(strings.NewReader(b)); err == nil {
			t.Errorf("ParseManifest(%s) succeeded", b)
		}
	}
	if _, err := ParseManifest(strings.NewReader(`{"version":"0.2.0","url":"http://127.0.0.1:8080/a","signature":"s"}`)); err != nil {
		t.Errorf("loopback http is allowed for local testing: %v", err)
	}
}

func TestManifestURL(t *testing.T) {
	if got := ManifestURL(Repo, "darwin", "arm64"); got != "https://github.com/Licoy/sshyard/releases/latest/download/update-darwin-arm64.json" {
		t.Error(got)
	}
	if ManifestName("windows", "amd64") != "update-windows-amd64.json" {
		t.Error("ManifestName")
	}
}

type fakeRelease struct {
	v   string
	err error
}

func (r *fakeRelease) Version() string { return r.v }
func (r *fakeRelease) Notes() string   { return "notes" }
func (r *fakeRelease) Install(_ context.Context, p func(int64, int64)) error {
	p(50, 100)
	p(100, 100)
	return r.err
}

type fakeBackend struct {
	enabled    bool
	rel        Release
	err        error
	relaunched bool
}

func (b *fakeBackend) Enabled() bool                          { return b.enabled }
func (b *fakeBackend) Check(context.Context) (Release, error) { return b.rel, b.err }
func (b *fakeBackend) Relaunch()                              { b.relaunched = true }

func TestService(t *testing.T) {
	ctx := context.Background()
	changes := 0
	b := &fakeBackend{enabled: true}
	s := New(b, func() { changes++ })
	if st := s.Check(ctx); st.State != UpToDate {
		t.Fatalf("no release: %v", st.State)
	}
	b.rel = &fakeRelease{v: "9.9.9"}
	if st := s.Check(ctx); st.State != Available || st.Version != "9.9.9" {
		t.Fatalf("release: %+v", st)
	}
	s.Relaunch()
	if b.relaunched {
		t.Fatal("relaunched before installing")
	}
	if st := s.Install(ctx); st.State != Ready || st.Percent() != 100 {
		t.Fatalf("install: %+v", st)
	}
	s.Relaunch()
	if !b.relaunched || changes == 0 {
		t.Error("relaunch / onChange")
	}

	b2 := &fakeBackend{enabled: true, rel: &fakeRelease{v: "2.0.0", err: errors.New("bad signature")}}
	s2 := New(b2, nil)
	s2.Check(ctx)
	if st := s2.Install(ctx); st.State != Failed || st.Err == nil {
		t.Errorf("failed install: %+v", st)
	}
	b3 := &fakeBackend{enabled: true, err: errors.New("offline")}
	if st := New(b3, nil).Check(ctx); st.State != Failed {
		t.Errorf("failed check: %+v", st)
	}
	if st := New(&fakeBackend{}, nil).Check(ctx); st.State != Disabled || !errors.Is(st.Err, ErrDisabled) {
		t.Errorf("disabled: %+v", st)
	}
}
