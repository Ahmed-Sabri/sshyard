package theme

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// ErrNotITermColors is a file that is no .itermcolors color scheme.
var ErrNotITermColors = errors.New("theme: not an .itermcolors color scheme")

// MaxITermColors bounds the file read: a scheme is a few kilobytes.
const MaxITermColors = 256 << 10

// iTerm2's names of the colors a Scheme keeps.
var itermKeys = map[string]func(*Scheme) *string{
	"Foreground Color":  func(s *Scheme) *string { return &s.Foreground },
	"Background Color":  func(s *Scheme) *string { return &s.Background },
	"Cursor Color":      func(s *Scheme) *string { return &s.Cursor },
	"Cursor Text Color": func(s *Scheme) *string { return &s.CursorText },
	"Selection Color":   func(s *Scheme) *string { return &s.Selection },
}

func init() {
	for i := range 16 {
		itermKeys[fmt.Sprintf("Ansi %d Color", i)] = func(s *Scheme) *string { return &s.ANSI[i] }
	}
}

// ParseITermColors reads an iTerm2 color scheme (an .itermcolors property
// list, as the iTerm2-Color-Schemes collection publishes them) named
// name. The foreground, the background and the 16 colors are required.
func ParseITermColors(name string, data []byte) (Scheme, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(data) > MaxITermColors {
		return Scheme{}, ErrNotITermColors
	}
	s := Scheme{ID: CustomPrefix + name, Name: name}
	d := xml.NewDecoder(bytes.NewReader(data))
	// The top-level dict: a <key>, then its value, a color's <dict>.
	top, key := false, ""
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Scheme{}, ErrNotITermColors
		}
		t, ok := tok.(xml.StartElement)
		switch {
		case !ok || t.Name.Local == "plist":
		case !top && t.Name.Local == "dict":
			top = true
		case !top:
			return Scheme{}, ErrNotITermColors
		case t.Name.Local == "key":
			if err := d.DecodeElement(&key, &t); err != nil {
				return Scheme{}, ErrNotITermColors
			}
			key = strings.TrimSpace(key)
		case t.Name.Local == "dict":
			hex, err := itermColor(d)
			if err != nil {
				return Scheme{}, err
			}
			if field, ok := itermKeys[key]; ok {
				*field(&s) = hex
			}
			key = ""
		default:
			if err := d.Skip(); err != nil {
				return Scheme{}, ErrNotITermColors
			}
			key = ""
		}
	}
	if !s.Valid() {
		return Scheme{}, ErrNotITermColors
	}
	return s, nil
}

// itermColor reads a color's dict, after its start, to #RRGGBB.
func itermColor(d *xml.Decoder) (string, error) {
	var rgb [3]float64
	var seen [3]bool
	key := ""
	for {
		tok, err := d.Token()
		if err != nil {
			return "", ErrNotITermColors
		}
		switch t := tok.(type) {
		case xml.StartElement:
			var v string
			if err := d.DecodeElement(&v, &t); err != nil {
				return "", ErrNotITermColors
			}
			if t.Name.Local == "key" {
				key = strings.TrimSpace(v)
				continue
			}
			i := map[string]int{"Red Component": 0, "Green Component": 1, "Blue Component": 2}
			if n, ok := i[key]; ok && (t.Name.Local == "real" || t.Name.Local == "integer") {
				f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
				if err != nil || math.IsNaN(f) {
					return "", ErrNotITermColors
				}
				rgb[n], seen[n] = math.Min(math.Max(f, 0), 1), true
			}
			key = ""
		case xml.EndElement:
			if t.Name.Local != "dict" {
				continue
			}
			if !seen[0] || !seen[1] || !seen[2] {
				return "", ErrNotITermColors
			}
			b := func(f float64) int { return int(math.Round(f * 255)) }
			return fmt.Sprintf("#%02X%02X%02X", b(rgb[0]), b(rgb[1]), b(rgb[2])), nil
		}
	}
}
