//go:build !windows

package session

import (
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestLocalEnvUnixLocale(t *testing.T) {
	for _, tc := range []struct {
		name          string
		parent, shell []string
		want          [3]string // LANG, LC_CTYPE, LC_ALL before the macOS default.
	}{
		{"missing", nil, nil, [3]string{}},
		{"all empty", []string{"LANG=", "LC_CTYPE=", "LC_ALL="}, nil, [3]string{}},
		{"other category", []string{"LC_MESSAGES=C"}, nil, [3]string{}},
		{"parent LANG", []string{"LANG=ja_JP.UTF-8"}, nil, [3]string{"ja_JP.UTF-8", "", ""}},
		{"parent LC_CTYPE", []string{"LC_CTYPE=C"}, nil, [3]string{"", "C", ""}},
		{"parent LC_ALL", []string{"LC_ALL=C"}, nil, [3]string{"", "", "C"}},
		{"shell LANG", nil, []string{"LANG=C"}, [3]string{"C", "", ""}},
		{"shell LC_CTYPE", nil, []string{"LC_CTYPE=zh_CN.GBK"}, [3]string{"", "zh_CN.GBK", ""}},
		{"shell LC_ALL", nil, []string{"LC_ALL=en_US.UTF-8"}, [3]string{"", "", "en_US.UTF-8"}},
		{"shell overrides", []string{"LC_CTYPE=C"}, []string{"LC_CTYPE=ja_JP.UTF-8"}, [3]string{"", "ja_JP.UTF-8", ""}},
		{"clear LANG", []string{"LANG=C"}, []string{"LANG="}, [3]string{}},
		{"clear LC_CTYPE", []string{"LC_CTYPE=C"}, []string{"LC_CTYPE="}, [3]string{}},
		{"clear LC_ALL", []string{"LC_ALL=C"}, []string{"LC_ALL="}, [3]string{}},
		{"clear all", []string{"LANG=C", "LC_CTYPE=C", "LC_ALL=C"}, []string{"LANG=", "LC_CTYPE=", "LC_ALL="}, [3]string{}},
		{"duplicate clear", nil, []string{"LANG=C", "LANG=", "LC_ALL=C", "LC_ALL=", "LC_CTYPE=C", "LC_CTYPE="}, [3]string{}},
		{"duplicate explicit", nil, []string{"LC_CTYPE=", "LC_CTYPE=C"}, [3]string{"", "C", ""}},
		{"remaining choice", []string{"LANG=C", "LC_CTYPE=C"}, []string{"LC_CTYPE="}, [3]string{"C", "", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearLocalTestLocale(t)
			t.Setenv("TERM", "parent-term")
			for _, entry := range tc.parent {
				key, value, _ := strings.Cut(entry, "=")
				t.Setenv(key, value)
			}
			// Spare capacity catches writes beyond Shell.Env's length too.
			backing := append(slices.Clone(tc.shell), "TERM=shell-term", "UNUSED=sentinel")
			sh := Shell{Env: backing[:len(backing)-1]}
			before, shellBefore := os.Environ(), slices.Clone(backing)
			got := localEnv(sh, "xterm-256color")
			if !slices.Equal(before, os.Environ()) || !slices.Equal(backing, shellBefore) {
				t.Fatal("localEnv changed parent environment or Shell.Env backing slice")
			}
			if runtime.GOOS != "darwin" {
				want := append(append(slices.Clone(before), termEnv("xterm-256color")...), sh.Env...)
				if !slices.Equal(got, want) {
					t.Fatal("non-Darwin Unix environment construction changed")
				}
				return
			}
			want := tc.want
			if want == [3]string{} {
				want[1] = "en_US.UTF-8"
			}
			checkLocalLocaleEnv(t, got, want)
		})
	}
}

func checkLocalLocaleEnv(t *testing.T, env []string, locale [3]string) {
	t.Helper()
	values := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if _, duplicate := values[key]; duplicate {
			t.Fatal("duplicate environment key after Darwin merge")
		}
		values[key] = value
	}
	for key, want := range map[string]string{
		"LANG": locale[0], "LC_CTYPE": locale[1], "LC_ALL": locale[2],
		"TERM": "shell-term", "COLORTERM": "truecolor", "TERM_PROGRAM": "SSHYard",
	} {
		if values[key] != want {
			t.Errorf("%s=%q, want %q", key, values[key], want)
		}
	}
}

func clearLocalTestLocale(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "LANG" || strings.HasPrefix(key, "LC_") {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
}
