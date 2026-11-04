package app

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/netproxy"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

// statusText says where a session is, for its pane's header and banner.
func (a *App) statusText(m locales.Messages, h assets.Host, st session.Status, now time.Time) string {
	switch st.State {
	case session.Idle, session.Connecting:
		if st.Hop != "" {
			return m.StateConnectingTo(st.Hop)
		}
		return m.StateConnecting()
	case session.Connected:
		return m.StateConnected()
	case session.Reconnecting:
		secs := int(st.Retry.Sub(now).Round(time.Second) / time.Second)
		if secs < 0 {
			secs = 0
		}
		return m.StateReconnecting(strconv.Itoa(secs), strconv.Itoa(st.Attempt))
	case session.Exited:
		return m.StateExited()
	case session.Failed:
		return a.errText(m, h, st.Err)
	}
	if st.Err != nil {
		return a.errText(m, h, st.Err)
	}
	return m.StateDisconnected()
}

// failedHost is the host whose hop err failed at: h, or one of its jump
// hosts.
func (a *App) failedHost(h assets.Host, err error) assets.Host {
	var he *session.HopError
	if errors.As(err, &he) && he.Jump {
		if j, ok := a.store.Get(he.ID); ok {
			return j
		}
	}
	return h
}

// proxyOf is the proxy the first hop to h goes through.
func (a *App) proxyOf(h assets.Host) (assets.Proxy, bool) {
	id := h.ProxyID
	if id == "" {
		if path, _ := a.store.JumpPath(h.ID); len(path) > 0 {
			id = path[0].ProxyID
		}
	}
	return a.store.Proxy(id)
}

// errText explains a connection error to h in the user's language,
// naming the jump host or proxy it failed at.
func (a *App) errText(m locales.Messages, h assets.Host, err error) string {
	if errors.Is(err, netproxy.ErrClientLimit) || errors.Is(err, netproxy.ErrHandshakeTooLarge) {
		return errText(m, h, err)
	}
	switch session.Classify(err) {
	case session.KindProxyAuth, session.KindProxy:
		px, _ := a.proxyOf(h)
		name := px.Name
		if name == "" {
			name = px.Addr()
		}
		if session.Classify(err) == session.KindProxyAuth {
			return m.ErrProxyAuth(name)
		}
		return m.ErrProxy(name, rootCause(err))
	}
	if errors.Is(err, assets.ErrJumpLoop) {
		return m.ErrJumpLoop()
	}
	f := a.failedHost(h, err)
	text := errText(m, f, err)
	if via, ok := a.hopBefore(h, err); ok && session.Classify(err) == session.KindNetwork {
		text = m.ErrNetworkVia(f.Label(), via.Label(), netReason(m, err))
	}
	if f.ID != h.ID {
		return m.ErrAtJump(f.Label(), text)
	}
	return text
}

// hopBefore is the hop a failed one was dialled through, for an error on
// a jump chain past its first hop.
func (a *App) hopBefore(h assets.Host, err error) (assets.Host, bool) {
	var he *session.HopError
	if !errors.As(err, &he) || he.Index < 1 {
		return assets.Host{}, false
	}
	path, perr := a.store.JumpPath(h.ID)
	if perr != nil || he.Index-1 >= len(path) {
		return assets.Host{}, false
	}
	return path[he.Index-1], true
}

// netReason says why a network error happened in plain words, falling
// back to the system's text.
func netReason(m locales.Messages, err error) string {
	detail := rootCause(err)
	var oc *ssh.OpenChannelError
	if errors.As(err, &oc) {
		detail = oc.Message
	}
	low := strings.ToLower(detail)
	switch {
	case strings.Contains(low, "connection refused"), strings.Contains(low, "actively refused"):
		return m.NetRefused()
	case strings.Contains(low, "no route to host"), strings.Contains(low, "network is unreachable"), strings.Contains(low, "host is unreachable"):
		return m.NetUnreachable()
	case strings.Contains(low, "no such host"), strings.Contains(low, "server misbehaving"):
		return m.NetNoSuchHost()
	case strings.Contains(low, "i/o timeout"), strings.Contains(low, "timed out"):
		return m.NetTimedOut()
	case strings.Contains(low, "connection reset"), strings.Contains(low, "eof"), strings.Contains(low, "broken pipe"):
		return m.NetReset()
	case oc != nil:
		return m.NetRejected(detail)
	}
	return detail
}

// errText explains a connection error in the user's language.
func errText(m locales.Messages, h assets.Host, err error) string {
	if err == nil {
		return m.StateDisconnected()
	}
	if errors.Is(err, netproxy.ErrClientLimit) || errors.Is(err, netproxy.ErrHandshakeTooLarge) {
		return m.ErrGeneric(securityErrorText(m, err))
	}
	name := h.Label()
	switch session.Classify(err) {
	case session.KindAuth:
		return m.ErrAuth(h.User, name)
	case session.KindNetwork:
		return m.ErrNetwork(name, netReason(m, err))
	case session.KindTimeout:
		return m.ErrTimeout(name)
	case session.KindNoBanner:
		return m.ErrNoBanner(name)
	case session.KindHostChanged:
		return m.ErrHostKeyChanged(name)
	case session.KindHostUnknown:
		var u *session.UnknownHostError
		if errors.As(err, &u) {
			return m.HostKeyUnknown(name, u.Key.Type(), u.Fingerprint)
		}
	case session.KindPassphrase:
		return m.ErrPassphrase(name)
	case session.KindNoCredentials:
		if h.Auth == assets.AuthAsk {
			return m.ErrAskPassword(name)
		}
		return m.ErrNoCredentials(name)
	case session.KindAgent:
		if errors.Is(err, session.ErrAgentEmpty) {
			return m.ErrAgentEmpty()
		}
		return m.ErrAgentUnavailable()
	}
	return m.ErrGeneric(rootCause(err))
}

func rootCause(err error) string {
	for {
		u := errors.Unwrap(err)
		if u == nil {
			return err.Error()
		}
		err = u
	}
}

// credPrompt asks for a password or a key's passphrase that is missing or
// was refused, for a pane's host or one of its jump hosts (host).
// Typed values serve this run under key; saved hosts can opt into an
// encrypted host-owned password for future connections.
type credPrompt struct {
	conn         int
	host         assets.Host
	key          string
	passphrase   bool
	savePassword bool
	value        string
	err          string
}

// hostKeyAsk asks whether to trust a host's key seen for the first time.
type hostKeyAsk struct {
	conn int
	err  *session.UnknownHostError
	err0 error // the whole failure, which names the hop
}

// checkFailures opens a prompt for a session that failed for want of what
// the user can give: a password, a passphrase, trust in a host key.
func (a *App) checkFailures(m locales.Messages) {
	if a.prompt != nil || a.hostKey != nil {
		return
	}
	for _, id := range a.promptConns() {
		c := a.conns[id]
		if c == nil || c.s == nil {
			continue
		}
		st := c.s.Status()
		if st.State != session.Failed || st.Err == nil || st.Err == c.handled {
			continue
		}
		c.handled = st.Err
		switch session.Classify(st.Err) {
		case session.KindNoCredentials, session.KindAuth, session.KindPassphrase:
			kind := session.Classify(st.Err)
			h := a.failedHost(c.host, st.Err)
			key, pass := a.promptKey(h, kind)
			if key == "" {
				continue // the editor is where a missing key goes
			}
			p := &credPrompt{conn: id, host: h, key: key, passphrase: pass}
			a.mu.Lock()
			if gen, ok := c.tried[key]; ok && gen < a.typedGen[key] && a.typed[key] != "" {
				// It failed with what was typed before the latest
				// answer: try that one instead of asking again.
				a.mu.Unlock()
				if c.s != nil {
					c.s.Reconnect()
				}
				continue
			}
			if kind == session.KindAuth || errors.Is(st.Err, session.ErrPassphraseWrong) {
				p.err = errText(m, h, st.Err)
			}
			delete(a.typed, key)
			a.mu.Unlock()
			// The panes that need the same secret (on the host, or on
			// hosts sharing the credential) failed with it: one prompt
			// answers for all of them.
			for _, o := range a.sameSecretFailures(c, key) {
				o.handled = o.s.Status().Err
			}
			a.prompt, a.prompting = p, true
		case session.KindHostUnknown:
			var u *session.UnknownHostError
			if errors.As(st.Err, &u) {
				a.hostKey, a.askingKey = &hostKeyAsk{conn: id, err: u, err0: st.Err}, true
			}
		}
		return
	}
}

// SubmitPrompt retains the non-UI entry point used by connection flows.
func (a *App) SubmitPrompt() { a.submitPrompt(locales.In(a.settings.Language)) }

func (a *App) submitPrompt(m locales.Messages) {
	p := a.prompt
	if p == nil {
		return
	}
	c := a.conns[p.conn]
	if c == nil {
		a.prompt, a.prompting = nil, false
		return
	}
	if p.savePassword {
		if err := a.savePromptPassword(m, p); err != nil {
			p.err = m.ErrKeychainSave(secretStoreErrorText(m, err))
			return
		}
	}
	a.prompt, a.prompting = nil, false
	a.mu.Lock()
	a.typed[p.key] = p.value
	a.typedGen[p.key]++
	a.mu.Unlock()
	a.focusReq = c.id
	others := a.sameSecretFailures(c, p.key)
	if c.s != nil {
		c.s.Reconnect()
	}
	// The first to reconnect dials the connection the others then share.
	for _, o := range others {
		if o.s != nil {
			o.s.Reconnect()
		}
	}
}

// sameSecretFailures are the other panes that failed for want of the
// secret of key, as c did.
func (a *App) sameSecretFailures(c *conn, key string) []*conn {
	var out []*conn
	for _, id := range a.promptConns() {
		o := a.conns[id]
		if o == nil || o == c || o.s == nil {
			continue
		}
		st := o.s.Status()
		if st.State != session.Failed {
			continue
		}
		switch k := session.Classify(st.Err); k {
		case session.KindNoCredentials, session.KindAuth, session.KindPassphrase:
			if ok, _ := a.promptKey(a.failedHost(o.host, st.Err), k); ok == key {
				out = append(out, o)
			}
		}
	}
	return out
}

// TrustHostKey adds the key asked about to known_hosts and connects again.
func (a *App) TrustHostKey() error {
	k := a.hostKey
	a.hostKey, a.askingKey = nil, false
	if k == nil {
		return nil
	}
	if err := a.known.Add(k.err.Host, k.err.Key); err != nil {
		return err
	}
	if c := a.conns[k.conn]; c != nil && c.s != nil {
		a.focusReq = c.id
		c.s.Reconnect()
	}
	return nil
}

func (a *App) promptsView(c *ui.Context, m locales.Messages) {
	a.checkFailures(m)
	if p := a.prompt; p != nil {
		title := m.PasswordPromptTitle(a.pv(p.host.Target()))
		if p.passphrase {
			title = m.PassphrasePromptTitle(a.pv(p.host.Target()))
		}
		tok := currentTokens(c)
		sheet(ui.Modal(c, &a.prompting, func() {
			ui.Column(c).Gap(14).Width(380).Children(func() {
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					hostAvatar(c, tok, a.liveHost(p.host), 36)
					ui.Text(c, title).FontSize(theme.Title3).FontWeight(600).Shrink(1)
				})
				if p.err != "" {
					ui.Text(c, p.err).FontSize(theme.Footnote).TextColor(tok.Danger)
				}
				in := ui.TextInput(c.Key("secret"), &p.value).Password().AutoFocus().Label(title).FillWidth()
				a.promptHold.apply(c, [2]any{p, p.err}, in)
				hint := m.TypedNotSaved()
				if p.savePassword {
					hint = m.SaveHostPasswordHint()
				}
				ui.Text(c, hint).FontSize(theme.Caption).TextColor(tok.TextMuted)
				if a.canSavePromptPassword(p) {
					ui.Checkbox(c.Key("prompt:save-password"), &p.savePassword, m.SavePasswordCredential())
				}
				submit := in.Submitted()
				ui.Row(c).Gap(8).Margin(4, 0, 0, 0).Children(func() {
					ui.Spacer(c)
					if pill(c, m.Cancel(), false, false, false) {
						a.prompting = false
					}
					if pill(c, m.Connect(), true, false, false) {
						submit = true
					}
				})
				if submit {
					a.submitPrompt(m)
				}
			})
		}), tok)
		if !a.prompting {
			a.prompt = nil
		}
	}
	if k := a.hostKey; k != nil {
		name := k.err.Host
		if cn := a.conns[k.conn]; cn != nil {
			name = a.failedHost(cn.host, k.err0).Label()
		}
		switch ui.AlertDialog(c, &a.askingKey, m.HostKeyUnknownTitle(),
			m.HostKeyUnknown(name, k.err.Key.Type(), k.err.Fingerprint), m.Cancel(), m.TrustAndConnect()) {
		case 1:
			if err := a.TrustHostKey(); err != nil {
				c.AddToast(ui.Toast{Title: m.ErrGeneric(err.Error()), Type: "error"})
			}
		}
		if !a.askingKey {
			a.hostKey = nil
		}
	}
}

// hostID is the ID of the connection's host, for its tint.
func hostID(cn *conn) string {
	if cn == nil {
		return ""
	}
	return cn.host.ID
}
