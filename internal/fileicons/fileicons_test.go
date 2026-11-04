package fileicons

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestLookup(t *testing.T) {
	tests := []struct {
		name string
		dir  bool
		want string
	}{
		{"README.MD", false, "readme"}, {"readme.TXT", false, "readme"},
		{"package.json", false, "nodejs"}, {"GO.MOD", false, "go-mod"},
		{".gitignore", false, "git"}, {".env", false, "settings"},
		{".ENV.LOCAL", false, "settings"}, {".unknown", false, "file"},
		{"types.d.ts", false, "typescript-def"}, {"types.d.mts", false, "typescript-def"},
		{"app.TS", false, "typescript"}, {"app.tsx", false, "react_ts"},
		{"archive.tar.gz", false, "zip"}, {"archive.tbz2", false, "zip"},
		{"archive.tbz", false, "zip"}, {"archive.txz", false, "zip"},
		{"photo.JPEG", false, "image"}, {"server.crt", false, "certificate"},
		{"main.swift", false, "swift"}, {"notes.txt", false, "document"},
		{"some.unknown", false, "file"}, {"", false, "file"},
		{"SRC", true, "folder-src"}, {"tests", true, "folder-test"},
		{".git", true, "folder-git"}, {".github", true, "folder-github"},
		{"node_modules", true, "folder-node"}, {"main.go", true, "folder"},
		{"package.json", true, "folder"}, {"", true, "folder"},
	}
	for _, tt := range tests {
		t.Run(tt.name+fmt.Sprint(tt.dir), func(t *testing.T) {
			got := Lookup(tt.name, tt.dir)
			if got == nil || got.Name != tt.want {
				t.Fatalf("Lookup(%q, %t) = %+v, want %s", tt.name, tt.dir, got, tt.want)
			}
		})
	}
}

func TestBundledArtwork(t *testing.T) {
	// This allowlist covers the actual unchanged upstream SVG vocabulary, so
	// unsupported text/images/patterns/markers/filters cannot be silently ignored.
	allowed := map[string]bool{"svg": true, "path": true, "g": true, "defs": true, "linearGradient": true, "stop": true}
	entries, err := files.ReadDir("svg")
	if err != nil {
		t.Fatal(err)
	}
	available := make(map[string]bool, len(entries))
	for _, entry := range entries {
		available[entry.Name()] = true
	}
	var hashes strings.Builder
	for _, entry := range entries {
		data, err := files.ReadFile("svg/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&hashes, "%x  internal/fileicons/svg/%s\n", sha256.Sum256(data), entry.Name())
		svg, err := ui.ParseSVG(data)
		if err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		w, h := svg.Size()
		if w <= 0 || h <= 0 {
			t.Fatalf("%s: invalid size %v x %v", entry.Name(), w, h)
		}
		decoder := xml.NewDecoder(bytes.NewReader(data))
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if node, ok := token.(xml.StartElement); ok && !allowed[node.Name.Local] {
				t.Fatalf("%s uses unsupported SVG element %s", entry.Name(), node.Name.Local)
			}
		}
	}
	sums, err := os.ReadFile("../../resources/licenses/material-icon-theme/SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	if hashes.String() != string(sums) {
		t.Fatal("bundled SVG bytes differ from upstream checksums")
	}
	for name, icon := range icons {
		if icon.Light == nil || icon.Dark == nil {
			t.Fatalf("%s lacks a theme variant", name)
		}
		if available[name+"_light.svg"] && icon.Light == icon.Dark {
			t.Fatalf("%s does not use its upstream light variant", name)
		}
	}
}

func TestCompoundExtensionOrder(t *testing.T) {
	// d.ts and ts lead to visibly different upstream artwork.
	if Lookup("a.d.ts", false) == Lookup("a.ts", false) {
		t.Fatal("longest suffix must win")
	}
	if Lookup("package.json", false) == Lookup("a.json", false) {
		t.Fatal("exact filename must win")
	}
}
