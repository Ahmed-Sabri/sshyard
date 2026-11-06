package session

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"
)

// The tmux sessions of a server, as the tmux panel lists them and what it
// does to them, each through one exec of sh. Commands name a session by
// its ID ($3), never by its name, which tmux would match as a prefix.

// ErrNoTmux is a server without the tmux command; ErrTmuxUnknown is
// TmuxShellIdle not finding out whether the shell waits at its prompt.
var (
	ErrNoTmux      = errors.New("session: tmux is not installed")
	ErrTmuxUnknown = errors.New("session: the shell's state could not be read")
	errTmuxArg     = errors.New("session: bad tmux name or target")
)

// tmuxPrelude exits 127 without tmux.
const tmuxPrelude = "command -v tmux >/dev/null 2>&1 || exit 127\n"

// tmuxListScript prints tmux's version (@V), its sessions (@S), their
// windows (@W) and the clients attached (@C), fields split by tabs, a
// name last as it may hold one. $1 is the tag of the terminal asking
// (TagEnv): a client whose environment holds it is that terminal's, which
// the server tells (@P) where it has Linux's /proc. Without a tmux server
// it prints no session.
var tmuxListScript = strings.ReplaceAll(tmuxPrelude+`echo "@V $(tmux -V 2>/dev/null)"
[ -n "$1" ] && [ -r /proc/self/environ ] && echo @P
tmux -u list-sessions -F "@S #{session_id}{T}#{session_windows}{T}#{session_attached}{T}#{session_created}{T}#{session_activity}{T}#{session_path}{T}#{session_name}" 2>/dev/null || exit 0
tmux -u list-windows -a -F "@W #{session_id}{T}#{window_index}{T}#{window_active}{T}#{window_panes}{T}#{window_name}" 2>/dev/null
t="`+TagEnv+`=$1"
tmux -u list-clients -F "#{client_pid}{T}#{client_tty}{T}#{session_id}" 2>/dev/null | while IFS="{T}" read -r p y s; do
  m=0
  [ -n "$1" ] && tr "\0" "\n" < "/proc/$p/environ" 2>/dev/null | grep -qx "$t" && m=1
  printf "@C %s\t%s\t%s\n" "$y" "$s" "$m"
done`, "{T}", "\t")

// tmuxIdleScript says whether the shell tagged $1 waits at its prompt
// (idle) or runs a program in the foreground (busy), from Linux's /proc:
// the shell is the process holding the tag whose parent does not (sshd),
// a tmux server started from it aside, and it waits while its terminal's
// foreground process group is its own. It exits 1 when it cannot tell.
const tmuxIdleScript = `t="` + TagEnv + `=$1"; p=/proc
for f in $(grep -lzx "$t" $p/[0-9]*/environ 2>/dev/null); do
  d=${f%/environ}
  case $(cat "$d/comm" 2>/dev/null) in tmux*) continue;; esac
  q=$(sed "s/.*) //" "$d/stat" 2>/dev/null | cut -d" " -f2)
  grep -qzx "$t" "$p/$q/environ" 2>/dev/null && continue
  set -- $(sed "s/.*) //" "$d/stat" 2>/dev/null)
  [ -n "$6" ] || exit 1
  [ "$6" = "$3" ] && echo idle || echo busy
  exit 0
done
exit 1`

// tmuxNewScript starts the session $1, detached, in the folder $2 (the
// home folder for "", under it for "~/…"), and prints its ID.
const tmuxNewScript = tmuxPrelude + `case $2 in
""|"~") d=$HOME;;
"~/"*) d=$HOME/${2#??};;
*) d=$2;;
esac
exec tmux -u new-session -d -P -F "#{session_id}" -s "$1" -c "$d"`

// TmuxWindow is a window of a tmux session.
type TmuxWindow struct {
	Index  int
	Name   string
	Active bool
	Panes  int
}

// TmuxSession is a tmux session: Attached counts its clients, Path is
// the folder its new windows start in.
type TmuxSession struct {
	ID       string
	Name     string
	Windows  int
	Attached int
	Created  time.Time
	Activity time.Time
	Path     string
	Wins     []TmuxWindow
}

// TmuxClient is a terminal attached to a session: Mine is set for the
// terminal the list was read for.
type TmuxClient struct {
	TTY     string
	Session string
	Mine    bool
}

// TmuxInfo is what TmuxList read. Tells is set when the server tells the
// asking terminal's client from the others: without it none is Mine.
type TmuxInfo struct {
	Version  string
	Tells    bool
	Sessions []TmuxSession
	Clients  []TmuxClient
}

// Mine is the client of the terminal the list was read for.
func (t TmuxInfo) Mine() (TmuxClient, bool) {
	for _, c := range t.Clients {
		if c.Mine {
			return c, true
		}
	}
	return TmuxClient{}, false
}

// Session is the session id.
func (t TmuxInfo) Session(id string) (TmuxSession, bool) {
	for _, s := range t.Sessions {
		if s.ID == id {
			return s, true
		}
	}
	return TmuxSession{}, false
}

// ParseTmuxList reads the output of the list script. Lines it does not
// understand are skipped.
func ParseTmuxList(out []byte) TmuxInfo {
	var t TmuxInfo
	at := map[string]int{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	unix := func(s string) time.Time {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 {
			return time.Time{}
		}
		return time.Unix(n, 0)
	}
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		tag, rest, _ := strings.Cut(line, " ")
		switch tag {
		case "@V":
			t.Version = strings.TrimSpace(rest)
		case "@P":
			t.Tells = true
		case "@S":
			f := strings.SplitN(rest, "\t", 7)
			if len(f) < 7 || !tmuxIDOK(f[0]) {
				continue
			}
			wins, _ := strconv.Atoi(f[1])
			att, _ := strconv.Atoi(f[2])
			at[f[0]] = len(t.Sessions)
			t.Sessions = append(t.Sessions, TmuxSession{ID: f[0], Name: f[6], Windows: wins, Attached: att,
				Created: unix(f[3]), Activity: unix(f[4]), Path: f[5]})
		case "@W":
			f := strings.SplitN(rest, "\t", 5)
			i, ok := at[f[0]]
			if len(f) < 5 || !ok {
				continue
			}
			idx, err := strconv.Atoi(f[1])
			if err != nil {
				continue
			}
			panes, _ := strconv.Atoi(f[3])
			s := &t.Sessions[i]
			s.Wins = append(s.Wins, TmuxWindow{Index: idx, Name: f[4], Active: f[2] == "1", Panes: panes})
		case "@C":
			f := strings.Split(rest, "\t")
			if len(f) != 3 || !tmuxTTYOK(f[0]) {
				continue
			}
			t.Clients = append(t.Clients, TmuxClient{TTY: f[0], Session: f[1], Mine: f[2] == "1"})
		}
	}
	return t
}

var (
	tmuxID  = regexp.MustCompile(`^\$[0-9]+$`)
	tmuxTTY = regexp.MustCompile(`^/dev/[A-Za-z0-9._/-]+$`)
)

func tmuxIDOK(id string) bool   { return tmuxID.MatchString(id) }
func tmuxTTYOK(tty string) bool { return len(tty) <= 128 && tmuxTTY.MatchString(tty) }

// TmuxNameOK reports whether a session may be given name: 1 to 64
// characters, no control character, and neither a colon nor a period,
// which tmux would change to an underscore, nor a leading hyphen or
// space, which it would read as an option or keep unseen.
func TmuxNameOK(name string) bool {
	n := utf8.RuneCountInString(name)
	if n == 0 || n > 64 || !utf8.ValidString(name) || name != strings.TrimSpace(name) || name[0] == '-' {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == ':' || r == '.' {
			return false
		}
	}
	return true
}

// runTmux runs script under sh, args its $1 …, its stdout returned and
// the last line of its stderr in the error of a failure.
func runTmux(ctx context.Context, client *ssh.Client, script string, args ...string) ([]byte, error) {
	var out, errb bytes.Buffer
	cmd := "sh -c " + shellQuote(script) + " _"
	for _, a := range args {
		cmd += " " + shellQuote(a)
	}
	err := Exec(ctx, client, cmd, &out, &errb)
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		if exit.ExitStatus() == 127 {
			return nil, ErrNoTmux
		}
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return nil, errors.New(lastLine(msg))
		}
	}
	return out.Bytes(), err
}

// TmuxList reads the server's tmux sessions, for the terminal tagged tag
// ("" for none).
func TmuxList(ctx context.Context, client *ssh.Client, tag string) (TmuxInfo, error) {
	out, err := runTmux(ctx, client, tmuxListScript, tag)
	if err != nil {
		return TmuxInfo{}, err
	}
	return ParseTmuxList(out), nil
}

// TmuxNew starts a detached session named name in dir ("" the home
// folder) and returns its ID.
func TmuxNew(ctx context.Context, client *ssh.Client, name, dir string) (string, error) {
	if !TmuxNameOK(name) || strings.ContainsAny(dir, "\x00\n") {
		return "", errTmuxArg
	}
	out, err := runTmux(ctx, client, tmuxNewScript, name, dir)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(out))
	if !tmuxIDOK(id) {
		return "", errors.New("session: unreadable tmux output")
	}
	return id, nil
}

// TmuxRename names the session id name.
func TmuxRename(ctx context.Context, client *ssh.Client, id, name string) error {
	if !tmuxIDOK(id) || !TmuxNameOK(name) {
		return errTmuxArg
	}
	_, err := runTmux(ctx, client, tmuxPrelude+`exec tmux -u rename-session -t "$1" "$2"`, id, name)
	return err
}

// TmuxKill ends the session id and what runs in it.
func TmuxKill(ctx context.Context, client *ssh.Client, id string) error {
	if !tmuxIDOK(id) {
		return errTmuxArg
	}
	_, err := runTmux(ctx, client, tmuxPrelude+`exec tmux kill-session -t "$1"`, id)
	return err
}

// TmuxSwitch shows the session id on the client tty, which stays
// attached; window, from 0, selects one of its windows first, -1 none.
func TmuxSwitch(ctx context.Context, client *ssh.Client, tty, id string, window int) error {
	if !tmuxTTYOK(tty) || !tmuxIDOK(id) {
		return errTmuxArg
	}
	if err := TmuxSelectWindow(ctx, client, id, window); err != nil {
		return err
	}
	_, err := runTmux(ctx, client, tmuxPrelude+`exec tmux switch-client -c "$1" -t "$2"`, tty, id)
	return err
}

// TmuxSelectWindow makes window the current window of the session id;
// -1 leaves it.
func TmuxSelectWindow(ctx context.Context, client *ssh.Client, id string, window int) error {
	if !tmuxIDOK(id) {
		return errTmuxArg
	}
	if window < 0 {
		return nil
	}
	_, err := runTmux(ctx, client, tmuxPrelude+`exec tmux select-window -t "$1:$2"`, id, strconv.Itoa(window))
	return err
}

// TmuxDetach detaches the client tty from its session, which goes on.
func TmuxDetach(ctx context.Context, client *ssh.Client, tty string) error {
	if !tmuxTTYOK(tty) {
		return errTmuxArg
	}
	_, err := runTmux(ctx, client, tmuxPrelude+`exec tmux detach-client -t "$1"`, tty)
	return err
}

// TmuxShellIdle reports whether the shell tagged tag waits at its
// prompt, where a command typed into its terminal runs; ErrTmuxUnknown
// when the server cannot tell (no tag, no /proc).
func TmuxShellIdle(ctx context.Context, client *ssh.Client, tag string) (bool, error) {
	if tag == "" {
		return false, ErrTmuxUnknown
	}
	out, err := runTmux(ctx, client, tmuxIdleScript, tag)
	var exit *ssh.ExitError
	switch {
	case errors.As(err, &exit):
		return false, ErrTmuxUnknown
	case err != nil:
		return false, err
	}
	switch strings.TrimSpace(string(out)) {
	case "idle":
		return true, nil
	case "busy":
		return false, nil
	}
	return false, ErrTmuxUnknown
}

// TmuxAttachCommand is the command a shell is given to attach to the
// session id, "" for an ID that is not one. The ID is quoted, which any
// shell reads alike.
func TmuxAttachCommand(id string) string {
	if !tmuxIDOK(id) {
		return ""
	}
	return "tmux attach-session -t '" + id + "'"
}

// TmuxNewOrAttachCommand is the command a shell is given to attach to the
// session named name, created first when there is none, "" for a name
// that may not be one. tmux takes the name as it is, not as a prefix.
func TmuxNewOrAttachCommand(name string) string {
	if !TmuxNameOK(name) {
		return ""
	}
	return "tmux new-session -A -s " + shellQuote(name)
}
