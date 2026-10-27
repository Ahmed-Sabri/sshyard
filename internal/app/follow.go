package app

import (
	"context"
	"net"
	"path"
	"strings"
	"time"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

// cwdSnippet makes bash and zsh report their directory (OSC 7) at every
// prompt, for shells that do not already.
const cwdSnippet = `# bash: ~/.bashrc
PROMPT_COMMAND='printf "\033]7;file://%s%s\033\\" "$HOSTNAME" "$PWD"'"${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
# zsh: ~/.zshrc
_sshyard_cwd() { printf '\033]7;file://%s%s\033\\' "$HOST" "$PWD" }
precmd_functions+=(_sshyard_cwd)`

// noReportAfter is how long a terminal is up without saying where it is
// before a following Files pane offers the snippet.
const noReportAfter = 3 * time.Second

// following reports whether the Files pane cn follows the terminal: its
// host allows it and the pane did not stop.
func (a *App) following(cn *conn) bool {
	h, ok := a.host(cn.host.ID)
	if !ok {
		h = cn.host
	}
	return cn.kind == layout.Files && h.CwdFollow && !cn.fs.noFollow
}

// followDir is the folder a shell reported, cleaned ("/a/b/../c/" is
// "/a/c"), or "" if it is not an absolute path (nothing to list).
func followDir(d string) string {
	if !path.IsAbs(d) || strings.ContainsRune(d, 0) {
		return ""
	}
	return path.Clean(d)
}

// followCwd sends each following Files pane to the directory the
// terminal of its tab, on the same host, last reported (OSC 7). With
// several such terminals, the one that reported last leads.
func (a *App) followCwd(now time.Time) {
	for _, t := range a.ws.Tabs {
		items := t.Items()
		for _, it := range items {
			tc := a.conns[it.ID]
			if tc == nil || tc.kind != layout.Terminal || tc.s == nil {
				continue
			}
			if tc.s.Status().State != session.Connected {
				tc.up = time.Time{}
			} else if tc.up.IsZero() {
				tc.up = now
			}
			if d := followDir(tc.s.Term.Cwd()); d != "" && d != tc.cwd && a.cwdOnHost(tc, tc.s.Term.CwdHost()) {
				a.cwdSeq++
				tc.cwd, tc.cwdSeq = d, a.cwdSeq
			}
		}
		for _, it := range items {
			fc := a.conns[it.ID]
			if fc == nil || !a.following(fc) {
				continue
			}
			lead := a.leadTerminal(items, fc)
			if lead == nil || lead.cwd == "" {
				continue
			}
			s := &fc.fs.remote
			if s.client == nil {
				// Not connected yet: it lists there when it is.
				s.dir = lead.cwd
				continue
			}
			if fc.fs.followed != lead.cwd {
				fc.fs.followed = lead.cwd
				if s.dir != lead.cwd {
					a.listRemote(s, lead.cwd)
				}
			}
		}
	}
}

// leadTerminal is the terminal of items on fc's host that reported its
// directory last, else any terminal on it, else nil.
func (a *App) leadTerminal(items []layout.Item, fc *conn) *conn {
	var lead *conn
	for _, it := range items {
		tc := a.conns[it.ID]
		if tc == nil || tc.kind != layout.Terminal || tc.host.ID != fc.host.ID {
			continue
		}
		if lead == nil || tc.cwdSeq > lead.cwdSeq {
			lead = tc
		}
	}
	return lead
}

// followButton is the Files pane header's follow switch, shown when the
// host lets its Files panes follow the terminal.
func (a *App) followButton(c *ui.Context, t *layout.Tab, n *layout.Node, cn *conn, m locales.Messages) {
	h, ok := a.host(cn.host.ID)
	if !ok {
		h = cn.host
	}
	if !h.CwdFollow {
		return
	}
	on := !cn.fs.noFollow
	if (IconButton{Icon: icons.Follow, On: icons.FollowOn, Label: m.FollowTerminal(), Active: on, Toggle: true, Size: Small}).Clicked(c, "follow") {
		t.Focus = n.ID
		cn.fs.noFollow = on
		cn.fs.followed = ""
	}
}

// noReportView offers the snippet when the pane follows a terminal that
// has been up a while without saying where it is.
func (a *App) noReportView(c *ui.Context, t *layout.Tab, cn *conn, tok theme.Tokens, m locales.Messages) {
	if !a.following(cn) {
		return
	}
	lead := a.leadTerminal(t.Items(), cn)
	if lead == nil || lead.cwd != "" || lead.up.IsZero() {
		return
	}
	if wait := noReportAfter - c.Now().Sub(lead.up); wait > 0 {
		c.After(wait)
		return
	}
	ui.Column(c.Key("noreport")).FillWidth().Gap(6).Padding(8).Radius(theme.RadiusCard - 2).Background(tok.Accent.Alpha(0.08)).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, icons.Follow.SVG).Size(14, 14).TextColor(tok.Accent)
			ui.Text(c, m.FollowNoReport()).FontSize(theme.Footnote).Grow(1).Shrink(1)
			if (IconButton{Icon: icons.CopyPath, Label: m.CopySnippet(), Size: Small}).Clicked(c, "copysnippet") {
				c.WriteClipboard(cwdSnippet)
			}
		})
		code := ui.Text(c, cwdSnippet).FontSize(theme.Caption).TextColor(tok.TextMuted).Selectable()
		if a.fonts.term != "" {
			code.Font(a.fonts.term)
		}
	})
}

// nameState is how far asking a terminal's server its hostname went.
type nameState int

const (
	nameUnasked nameState = iota
	nameAsking
	nameKnown
	nameFailed // pinned: name is the first host reported
)

// hostnameTimeout is how long asking the server its hostname may take.
var hostnameTimeout = 10 * time.Second

// cwdOnHost says a directory report naming host is from the terminal's
// own server, not one reached from it (ssh to another host inside the
// terminal): Files panes then follow it. A report naming no host, or
// localhost, counts as the server's own, as does one naming the host's
// address in its settings. Otherwise it is matched against the server's
// hostname, asked once per connection by running "hostname" on it (while
// it is asked, the report waits); should that fail, the first host the
// terminal reports is taken as the server's. Short and full names match
// ("web1" and "web1.example.com").
func (a *App) cwdOnHost(tc *conn, host string) bool {
	if host == "" || strings.EqualFold(host, "localhost") || sameHostName(host, tc.host.Address) {
		return true
	}
	connects := tc.s.Status().Connects
	if tc.nameConn != connects {
		tc.nameConn, tc.name, tc.nameAsk = connects, "", nameUnasked
	}
	switch tc.nameAsk {
	case nameUnasked:
		tc.nameAsk = nameAsking
		a.askHostname(tc, connects)
		return false
	case nameAsking:
		return false
	case nameFailed:
		if tc.name == "" {
			tc.name = host
		}
	}
	return sameHostName(host, tc.name)
}

// askHostname runs "hostname" on the terminal's connection.
func (a *App) askHostname(tc *conn, connects int) {
	client := tc.s.Client()
	go func() {
		name, err := "", error(context.Canceled)
		if client != nil {
			var sess *ssh.Session
			if sess, err = client.NewSession(); err == nil {
				t := time.AfterFunc(hostnameTimeout, func() { sess.Close() })
				var out []byte
				out, err = sess.Output("hostname")
				t.Stop()
				sess.Close()
				name = strings.TrimSpace(string(out))
			}
		}
		if err == nil && (name == "" || strings.ContainsAny(name, " \t\r\n/")) {
			err = context.Canceled
		}
		a.post(func() {
			if tc.nameConn != connects || tc.nameAsk != nameAsking {
				return
			}
			if err != nil {
				tc.nameAsk = nameFailed
				return
			}
			tc.name, tc.nameAsk = name, nameKnown
		})
	}()
}

// sameHostName says a and b name the same host, in any case, a short
// name matching a full one that starts with it.
func sameHostName(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if strings.EqualFold(a, b) {
		return true
	}
	if net.ParseIP(a) != nil || net.ParseIP(b) != nil {
		return false
	}
	sa, _, fa := strings.Cut(a, ".")
	sb, _, fb := strings.Cut(b, ".")
	return fa != fb && strings.EqualFold(sa, sb)
}
