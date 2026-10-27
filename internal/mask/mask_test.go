package mask

import "testing"

func TestIP(t *testing.T) {
	for in, want := range map[string]string{
		"12.32.43.55":    "1*.**.**.*5",
		"8.159.146.156":  "8.***.***.**6",
		"192.168.1.1":    "1**.***.*.1",
		"2001:db8::1":    "2***:***::1",
		"[2001:db8::42]": "[2***:***::*2]",
		"example.com":    "example.com",
		"web-01":         "web-01",
		"":               "",
	} {
		if got := IP(in); got != want {
			t.Errorf("IP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestText(t *testing.T) {
	for in, want := range map[string]string{
		"root@12.32.43.55":                           "root@1*.**.**.*5",
		"root@12.32.43.55:2222":                      "root@1*.**.**.*5:2222",
		"dial tcp 8.159.146.156:22: i/o timeout":     "dial tcp 8.***.***.**6:22: i/o timeout",
		"[2001:db8::1]:22":                           "[2***:***::1]:22",
		"via 10.0.0.1 → 10.0.0.2.":                   "via 1*.*.*.1 → 1*.*.*.2.",
		"fe80::1 and ::1":                            "f***::1 and ::1",
		"a 1.2.3.4 b":                                "a 1.*.*.4 b",
		"version 1.2.3, at 12:30:45":                 "version 1.2.3, at 12:30:45",
		"v1.2.3.4 build":                             "v1.2.3.4 build",
		"1.2.3.4.5":                                  "1.2.3.4.5",
		"example.com:22":                             "example.com:22",
		"deadbeef:cafe":                              "deadbeef:cafe",
		"no address here":                            "no address here",
		"eth0 inet 172.17.0.3/16 brd 172.17.255.255": "eth0 inet 1**.**.*.3/16 brd 1**.**.***.**5",
	} {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
}
