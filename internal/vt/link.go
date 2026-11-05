package vt

import "strings"

// maxLinkRows bounds the wrapped rows LinkAt reads around a point.
const maxLinkRows = 32

// LinkAt returns the web address shown at p, an http or https URL, across
// lines that wrapped, with its first and last cells; "" when there is
// none. A space or text that is not ASCII ends it; trailing punctuation
// and a closing bracket it did not open are left out.
func (t *Terminal) LinkAt(p Pos) (string, Pos, Pos) {
	t.mu.Lock()
	defer t.mu.Unlock()
	first, last := t.firstLine(), t.firstLine()+t.historyLen()-1
	if p.Line < first || p.Line > last || p.Col < 0 {
		return "", p, p
	}
	start, end := p.Line, p.Line
	for start > first && p.Line-start < maxLinkRows && t.lineAt(start-1).Wrapped {
		start--
	}
	for end < last && end-p.Line < maxLinkRows && t.lineAt(end).Wrapped {
		end++
	}
	// The line as one text, one rune a cell, and the cell of each.
	var rs []rune
	var at []Pos
	here := -1
	for n := start; n <= end; n++ {
		l := t.lineAt(n)
		cells := l.Cells
		if n < end && l.Wrapped && l.widePad && len(cells) > 0 {
			cells = cells[:len(cells)-1]
		}
		for i, c := range cells {
			if c.Width == 0 {
				if n == p.Line && i == p.Col {
					here = len(rs) - 1 // the second half of a wide character
				}
				continue
			}
			if n == p.Line && i == p.Col {
				here = len(rs)
			}
			r := c.Rune
			if r == 0 || c.Comb != 0 {
				r = ' '
			}
			rs = append(rs, r)
			at = append(at, Pos{n, i})
		}
	}
	if here < 0 {
		return "", p, p
	}
	for from := 0; from < len(rs); {
		i := linkStart(rs, from)
		if i < 0 || i > here {
			break
		}
		j := linkEnd(rs, i)
		if here < j {
			return string(rs[i:j]), at[i], at[j-1]
		}
		from = max(j, i+1)
	}
	return "", p, p
}

// linkStart is the index of the first "http://" or "https://" in rs from
// from, at the start of a word, or -1.
func linkStart(rs []rune, from int) int {
	for i := from; i < len(rs); i++ {
		if rs[i] != 'h' && rs[i] != 'H' {
			continue
		}
		if i > 0 && isLinkRune(rs[i-1]) && !strings.ContainsRune("([<'\"=:", rs[i-1]) {
			continue
		}
		rest := strings.ToLower(string(rs[i:min(i+8, len(rs))]))
		switch {
		case strings.HasPrefix(rest, "https://") && i+8 < len(rs) && isLinkRune(rs[i+8]):
			return i
		case strings.HasPrefix(rest, "http://") && i+7 < len(rs) && isLinkRune(rs[i+7]):
			return i
		}
	}
	return -1
}

// linkEnd is the end of the link starting at i: the runes a URL holds,
// less trailing punctuation and closing brackets it did not open.
func linkEnd(rs []rune, i int) int {
	j := i
	for j < len(rs) && isLinkRune(rs[j]) {
		j++
	}
	for j > i {
		switch r := rs[j-1]; r {
		case '.', ',', ';', ':', '!', '?', '\'', '"', '*':
			j--
			continue
		case ')', ']', '}':
			open := map[rune]rune{')': '(', ']': '[', '}': '{'}[r]
			opened, closed := 0, 0
			for _, c := range rs[i:j] {
				switch c {
				case open:
					opened++
				case r:
					closed++
				}
			}
			if closed > opened {
				j--
				continue
			}
		}
		break
	}
	return j
}

// isLinkRune reports a rune a URL may hold as it is shown: the ASCII
// letters, digits and RFC 3986 punctuation.
func isLinkRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("-._~:/?#[]@!$&'()*+,;=%", r)
}
