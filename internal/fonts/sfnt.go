package fonts

import (
	"encoding/binary"
	"errors"
	"unicode/utf16"
)

var errFont = errors.New("fonts: not an OpenType font")

// ParseFont reads the family of each font in a TrueType or OpenType font,
// or collection: its typographic family name (name ID 16) or else its
// family name (1), English first; monospaced when its post table says
// isFixedPitch or its OS/2 PANOSE proportion is monospaced.
func ParseFont(data []byte) ([]Family, error) {
	if len(data) < 12 {
		return nil, errFont
	}
	if string(data[:4]) == "ttcf" {
		n := int(binary.BigEndian.Uint32(data[8:12]))
		if n <= 0 || n > 1024 || len(data) < 12+4*n {
			return nil, errFont
		}
		var out []Family
		for i := range n {
			off := int(binary.BigEndian.Uint32(data[12+4*i:]))
			if f, err := parseFace(data, off); err == nil {
				out = append(out, f)
			}
		}
		if len(out) == 0 {
			return nil, errFont
		}
		return out, nil
	}
	f, err := parseFace(data, 0)
	if err != nil {
		return nil, err
	}
	return []Family{f}, nil
}

func parseFace(data []byte, off int) (Family, error) {
	if off < 0 || off+12 > len(data) {
		return Family{}, errFont
	}
	switch binary.BigEndian.Uint32(data[off:]) {
	case 0x00010000, 0x4F54544F /* OTTO */, 0x74727565 /* true */ :
	default:
		return Family{}, errFont
	}
	n := int(binary.BigEndian.Uint16(data[off+4:]))
	tables := map[string][]byte{}
	for i := range n {
		r := off + 12 + 16*i
		if r+16 > len(data) {
			return Family{}, errFont
		}
		tag := string(data[r : r+4])
		to := int(binary.BigEndian.Uint32(data[r+8:]))
		tl := int(binary.BigEndian.Uint32(data[r+12:]))
		if to < 0 || tl < 0 || to+tl > len(data) {
			continue
		}
		tables[tag] = data[to : to+tl]
	}
	name := familyName(tables["name"])
	if name == "" {
		return Family{}, errFont
	}
	mono := false
	if p := tables["post"]; len(p) >= 16 && binary.BigEndian.Uint32(p[12:]) != 0 {
		mono = true
	}
	// PANOSE starts at byte 32 of OS/2; for Latin text (family kind 2),
	// proportion (its fourth byte) 9 is monospaced.
	if o := tables["OS/2"]; len(o) >= 42 && o[32] == 2 && o[35] == 9 {
		mono = true
	}
	return Family{Name: name, Mono: mono}, nil
}

// familyName picks the family from a name table.
func familyName(t []byte) string {
	if len(t) < 6 {
		return ""
	}
	count := int(binary.BigEndian.Uint16(t[2:]))
	strOff := int(binary.BigEndian.Uint16(t[4:]))
	best, bestScore := "", -1
	for i := range count {
		r := 6 + 12*i
		if r+12 > len(t) {
			break
		}
		platform := binary.BigEndian.Uint16(t[r:])
		encoding := binary.BigEndian.Uint16(t[r+2:])
		lang := binary.BigEndian.Uint16(t[r+4:])
		id := binary.BigEndian.Uint16(t[r+6:])
		ln := int(binary.BigEndian.Uint16(t[r+8:]))
		o := strOff + int(binary.BigEndian.Uint16(t[r+10:]))
		if id != 1 && id != 16 || o+ln > len(t) {
			continue
		}
		raw := t[o : o+ln]
		var s string
		score := 0
		switch {
		case platform == 3 || platform == 0: // Windows or Unicode: UTF-16BE
			if platform == 3 && encoding != 1 && encoding != 10 && encoding != 0 {
				continue
			}
			u := make([]uint16, len(raw)/2)
			for j := range u {
				u[j] = binary.BigEndian.Uint16(raw[2*j:])
			}
			s = string(utf16.Decode(u))
			score = 2
			if platform == 3 && lang == 0x0409 {
				score = 4 // English (United States)
			}
		case platform == 1 && encoding == 0: // Mac Roman, ASCII in practice
			s = string(raw)
			score = 1
			if lang == 0 {
				score = 3
			}
		default:
			continue
		}
		if id == 16 {
			score += 10 // the typographic family groups every weight
		}
		if s != "" && score > bestScore {
			best, bestScore = s, score
		}
	}
	return best
}
