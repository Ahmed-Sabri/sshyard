package fonts

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"unicode/utf16"
)

func TestParseFCList(t *testing.T) {
	out := "DejaVu Sans Mono,DejaVu Sans Mono Book\t100\n" +
		"DejaVu Sans\t\n" +
		"Noto Sans CJK SC,Noto Sans CJK SC Regular\t90\n" +
		"Odd\\, Name\t\r\n" +
		"\t100\n" +
		"JetBrains Mono\t100\n"
	got := ParseFCList(out)
	want := []Family{
		{"DejaVu Sans Mono", true}, {"DejaVu Sans", false},
		{"Noto Sans CJK SC", true}, {"Odd, Name", false}, {"JetBrains Mono", true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestNormalizeAndTerminalOrder(t *testing.T) {
	in := []Family{{"menlo", false}, {"Arial", false}, {".SF NS", false}, {"Menlo", true}, {" ", true}, {"Courier New", true}, {"arial", false}}
	got := Normalize(in)
	want := []Family{{"Arial", false}, {"Courier New", true}, {"menlo", true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Normalize %v, want %v", got, want)
	}
	if got := ForTerminal(want, false); !reflect.DeepEqual(got, []Family{{"Courier New", true}, {"menlo", true}, {"Arial", false}}) {
		t.Errorf("ForTerminal %v", got)
	}
	if got := ForTerminal(want, true); len(got) != 2 || got[0].Name != "Courier New" {
		t.Errorf("monospaced only %v", got)
	}
	if !Has(want, "MENLO") || Has(want, "Helvetica") {
		t.Error("Has")
	}
}

func TestDirs(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := Dirs("darwin", env(nil), "/Users/me"); !reflect.DeepEqual(got, []string{"/System/Library/Fonts", "/Library/Fonts", "/Network/Library/Fonts", filepath.Join("/Users/me", "Library", "Fonts")}) {
		t.Errorf("darwin %v", got)
	}
	if got := Dirs("windows", env(map[string]string{"WINDIR": `D:\Win`, "LOCALAPPDATA": `C:\Users\me\AppData\Local`}), ""); !reflect.DeepEqual(got, []string{`D:\Win\Fonts`, `C:\Users\me\AppData\Local\Microsoft\Windows\Fonts`}) {
		t.Errorf("windows %v", got)
	}
	if got := Dirs("windows", env(nil), ""); !reflect.DeepEqual(got, []string{`C:\Windows\Fonts`}) {
		t.Errorf("windows default %v", got)
	}
	got := Dirs("linux", env(nil), "/home/me")
	want := []string{filepath.Join("/home/me", ".local", "share", "fonts"), filepath.Join("/home/me", ".fonts"), filepath.Join("/usr/local/share", "fonts"), filepath.Join("/usr/share", "fonts")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("linux %v", got)
	}
}

// font builds an OpenType font of a name and a post table.
func font(names map[uint16]string, fixed bool) []byte {
	// name table: format 0, Windows Unicode BMP English records
	var recs, strs bytes.Buffer
	ids := []uint16{1, 16}
	n := 0
	for _, id := range ids {
		s, ok := names[id]
		if !ok {
			continue
		}
		u := utf16.Encode([]rune(s))
		raw := make([]byte, 2*len(u))
		for i, c := range u {
			binary.BigEndian.PutUint16(raw[2*i:], c)
		}
		binary.Write(&recs, binary.BigEndian, []uint16{3, 1, 0x0409, id, uint16(len(raw)), uint16(strs.Len())})
		strs.Write(raw)
		n++
	}
	var name bytes.Buffer
	binary.Write(&name, binary.BigEndian, []uint16{0, uint16(n), uint16(6 + 12*n)})
	name.Write(recs.Bytes())
	name.Write(strs.Bytes())
	post := make([]byte, 32)
	binary.BigEndian.PutUint32(post, 0x00030000)
	if fixed {
		binary.BigEndian.PutUint32(post[12:], 1)
	}
	tables := []struct {
		tag  string
		data []byte
	}{{"name", name.Bytes()}, {"post", post}}
	var out bytes.Buffer
	binary.Write(&out, binary.BigEndian, uint32(0x00010000))
	binary.Write(&out, binary.BigEndian, []uint16{uint16(len(tables)), 0, 0, 0})
	off := 12 + 16*len(tables)
	for _, tb := range tables {
		out.WriteString(tb.tag)
		binary.Write(&out, binary.BigEndian, []uint32{0, uint32(off), uint32(len(tb.data))})
		off += len(tb.data)
	}
	for _, tb := range tables {
		out.Write(tb.data)
	}
	return out.Bytes()
}

func TestParseFont(t *testing.T) {
	got, err := ParseFont(font(map[uint16]string{1: "Fira Code Retina", 16: "Fira Code"}, true))
	if err != nil || !reflect.DeepEqual(got, []Family{{"Fira Code", true}}) {
		t.Errorf("typographic family: %v %v", got, err)
	}
	got, err = ParseFont(font(map[uint16]string{1: "思源黑体"}, false))
	if err != nil || !reflect.DeepEqual(got, []Family{{"思源黑体", false}}) {
		t.Errorf("family: %v %v", got, err)
	}
	if _, err := ParseFont([]byte("not a font at all")); err == nil {
		t.Error("garbage parsed")
	}
	if _, err := ParseFont(font(nil, false)); err == nil {
		t.Error("a font without a name parsed")
	}
}

func TestParseCollection(t *testing.T) {
	a := font(map[uint16]string{1: "Sarasa Mono SC"}, true)
	b := font(map[uint16]string{1: "Sarasa Gothic SC"}, false)
	// A collection whose fonts' offsets are from its start: the fonts'
	// table offsets must move with them.
	shift := func(f []byte, by int) []byte {
		f = bytes.Clone(f)
		n := int(binary.BigEndian.Uint16(f[4:]))
		for i := range n {
			r := 12 + 16*i + 8
			binary.BigEndian.PutUint32(f[r:], binary.BigEndian.Uint32(f[r:])+uint32(by))
		}
		return f
	}
	head := 12 + 8
	var ttc bytes.Buffer
	ttc.WriteString("ttcf")
	binary.Write(&ttc, binary.BigEndian, []uint32{0x00010000, 2, uint32(head), uint32(head + len(a))})
	ttc.Write(shift(a, head))
	ttc.Write(shift(b, head+len(a)))
	got, err := ParseFont(ttc.Bytes())
	if err != nil || !reflect.DeepEqual(got, []Family{{"Sarasa Mono SC", true}, {"Sarasa Gothic SC", false}}) {
		t.Errorf("collection: %v %v", got, err)
	}
}

func TestScanDirs(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "truetype", "x")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "Mono.TTF"), font(map[uint16]string{1: "Test Mono"}, true), 0o644)
	os.WriteFile(filepath.Join(dir, "Sans.otf"), font(map[uint16]string{1: "Test Sans"}, false), 0o644)
	os.WriteFile(filepath.Join(dir, "broken.ttf"), []byte("nope"), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("x"), 0o644)
	got := Normalize(ScanDirs([]string{dir, filepath.Join(dir, "missing")}))
	if !reflect.DeepEqual(got, []Family{{"Test Mono", true}, {"Test Sans", false}}) {
		t.Errorf("%v", got)
	}
}

func TestListFallsBackToScan(t *testing.T) {
	old := engine
	defer func() { engine = old }()
	engine = func() []Family { return []Family{{"B", false}, {"A", true}} }
	if got := List(); !reflect.DeepEqual(got, []Family{{"A", true}, {"B", false}}) {
		t.Errorf("engine list %v", got)
	}
	engine = func() []Family { return nil }
	got := List()
	t.Logf("%s: %d families scanned", runtime.GOOS, len(got))
	if runtime.GOOS == "linux" && len(got) == 0 {
		if _, err := os.Stat("/usr/share/fonts"); err == nil {
			t.Error("no fonts scanned though /usr/share/fonts exists")
		}
	}
}
