// Package mask hides IP addresses in what the app shows while privacy
// mode is on, as for a screen share or a recording: "12.32.43.55" is
// "1*.**.**.*5". Only the first character of the address and the last
// one stay, so that the user can still tell two hosts apart at a glance;
// the shape (the dots, the colons, the number of digits) stays too.
package mask

import (
	"net/netip"
	"strings"
)

// IP masks s when it is an IP address, v4 or v6 (bracketed or not), and
// returns it as it is otherwise.
func IP(s string) string {
	if _, err := netip.ParseAddr(strings.Trim(s, "[]")); err != nil {
		return s
	}
	return hide(s)
}

// Text masks every IP address in s: an address alone, user@address,
// address:port, [v6]:port, an address in an error message. An address
// glued to a word ("v1.2.3.4") is not one.
func Text(s string) string {
	if !strings.ContainsAny(s, ".:") {
		return s
	}
	var out strings.Builder
	for i := 0; i < len(s); {
		if i == 0 || !isAlnum(s[i-1]) && s[i-1] != '.' {
			if n := addrAt(s[i:]); n > 0 {
				out.WriteString(hide(s[i : i+n]))
				i += n
				continue
			}
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// addrAt is the length of the address t starts with, 0 for none.
func addrAt(t string) int {
	// v6: hex digits and colons (a v4 tail too), two colons at least
	r := strings.TrimRight(t[:run(t, func(c byte) bool { return isHex(c) || c == ':' || c == '.' })], ":.")
	if strings.Count(r, ":") >= 2 && ends(t, len(r)) {
		if ip, err := netip.ParseAddr(r); err == nil && ip.Is6() {
			return len(r)
		}
	}
	// v4: four parts exactly, so a version ("1.2.3") is left alone
	r = strings.TrimRight(t[:run(t, func(c byte) bool { return c >= '0' && c <= '9' || c == '.' })], ".")
	if strings.Count(r, ".") == 3 && ends(t, len(r)) {
		if ip, err := netip.ParseAddr(r); err == nil && ip.Is4() {
			return len(r)
		}
	}
	return 0
}

// run is the length of the prefix of t whose bytes are ok.
func run(t string, ok func(byte) bool) int {
	n := 0
	for n < len(t) && ok(t[n]) {
		n++
	}
	return n
}

// ends says t's word ends at n: no letter or digit follows.
func ends(t string, n int) bool { return n == len(t) || !isAlnum(t[n]) }

// hide keeps the first and the last hex digit of an address and its
// separators, and stars every other digit.
func hide(s string) string {
	b := []byte(s)
	first, last := -1, -1
	for i, c := range b {
		if isHex(c) {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	for i, c := range b {
		if isHex(c) && i != first && i != last {
			b[i] = '*'
		}
	}
	return string(b)
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}
