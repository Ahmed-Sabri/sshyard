package app

import (
	"os"
	"os/user"
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/sshconfig"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// Quick connect opens a terminal to a host typed on the spot, as
// user@host:port or an ssh command, on a temporary host: kept in memory
// while a pane uses it, never in hosts.json, its password (typed now or
// when asked) only in a.typed, and both gone when its last pane closes.

// tempPrefix starts the ID of a temporary host; saved IDs are hex.
const tempPrefix = "tmp-"

func isTemp(id string) bool { return strings.HasPrefix(id, tempPrefix) }

// host is the saved or temporary host of id.
func (a *App) host(id string) (assets.Host, bool) {
	if isLocal(id) {
		return a.localHost(id), true
	}
	if h, ok := a.store.Get(id); ok {
		return h, true
	}
	h, ok := a.temps[id]
	return h, ok
}

// dropTemp forgets the temporary host id, and what was typed for it,
// once no pane was opened on it.
func (a *App) dropTemp(id string) {
	if id == "" {
		return
	}
	for _, c := range a.conns {
		if c.temp == id {
			return
		}
	}
	delete(a.temps, id)
	a.mu.Lock()
	delete(a.typed, "ask/"+id)
	delete(a.typedGen, "ask/"+id)
	a.mu.Unlock()
}

// localUser is who a host without a user logs in as, as with ssh.
func (a *App) localUser() string {
	if a.p.LocalUser != "" {
		return a.p.LocalUser
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		name := u.Username
		if i := strings.LastIndexAny(name, `\`); i >= 0 {
			name = name[i+1:] // DOMAIN\user on Windows
		}
		return name
	}
	for _, v := range []string{"USER", "USERNAME", "LOGNAME"} {
		if s := os.Getenv(v); s != "" {
			return s
		}
	}
	return "root"
}

// quickForm is the quick connect sheet's fields.
type quickForm struct {
	target   string
	auth     string // the label chosen, see quickChoices
	password string
	err      string
}

// quickChoices are how a quick connection logs in: a password typed
// now (or when asked), the agent, or a saved credential.
func (a *App) quickChoices(m locales.Messages) []authChoice {
	out := a.authChoices(m)
	out[0].label = m.QuickPassword()
	return out
}

// OpenQuickConnect shows the quick connect sheet.
func (a *App) OpenQuickConnect() {
	a.quick, a.quicking = &quickForm{}, true
}

// QuickConnect connects to what the sheet holds, on a new temporary host.
func (a *App) QuickConnect(m locales.Messages) bool {
	f := a.quick
	if f == nil {
		return false
	}
	f.err = ""
	if sh, ok, err := assets.ParseStreamURL(f.target); ok {
		if err != nil {
			f.err = m.QuickStreamInvalid()
			return false
		}
		return a.quickStream(m, f, sh)
	}
	hop, err := sshconfig.ParseQuick(f.target)
	if err != nil {
		f.err = m.QuickInvalid()
		if oe, ok := err.(*sshconfig.ErrQuickOption); ok {
			f.err = m.QuickOption(oe.Option)
		}
		return false
	}
	choices := a.quickChoices(m)
	ch := choices[0]
	for _, c := range choices {
		if c.label == f.auth {
			ch = c
		}
	}
	if hop.User == "" && ch.method == assets.AuthCredential {
		if cr, ok := a.store.Credential(ch.cred); ok && cr.User != "" {
			hop.User = cr.User
		}
	}
	if hop.User == "" {
		hop.User = a.localUser()
	}
	if hop.Port == 0 {
		hop.Port = 22
	}
	a.nextTemp++
	h := assets.Host{
		ID: tempPrefix + strconv.Itoa(a.nextTemp), Name: hop.Host, Address: hop.Host, Port: hop.Port, User: hop.User,
		Auth: ch.method, CredentialID: ch.cred, CwdFollow: true,
	}
	h.Normalize()
	if err := h.Validate(); err != nil {
		f.err = fieldError(m, err)
		return false
	}
	return a.quickOpen(m, f, h)
}

// quickOpen connects to the temporary host h, with the password typed
// in the sheet when it asks for one.
func (a *App) quickOpen(m locales.Messages, f *quickForm, h assets.Host) bool {
	a.temps[h.ID] = h
	if h.Auth == assets.AuthAsk && f.password != "" {
		a.mu.Lock()
		a.typed["ask/"+h.ID] = f.password
		a.typedGen["ask/"+h.ID]++
		a.mu.Unlock()
	}
	f.password = ""
	if _, err := a.Connect(h.ID, openAt{}); err != nil {
		delete(a.temps, h.ID)
		f.err = m.ErrGeneric(err.Error())
		return false
	}
	a.quick, a.quicking = nil, false
	return true
}

// quickStream connects to a Telnet or serial destination. It logs in
// with the password typed or the password credential chosen, by hand
// otherwise.
func (a *App) quickStream(m locales.Messages, f *quickForm, h assets.Host) bool {
	choices := a.quickChoices(m)
	ch := choices[0]
	for _, c := range choices {
		if c.label == f.auth {
			ch = c
		}
	}
	switch ch.method {
	case assets.AuthAsk:
		if f.password != "" {
			h.Auth = assets.AuthAsk
		}
	case assets.AuthCredential:
		cr, ok := a.store.Credential(ch.cred)
		if !ok || cr.Kind != assets.CredPassword {
			f.err = m.CredentialPasswordOnly()
			return false
		}
		h.Auth, h.CredentialID = assets.AuthCredential, cr.ID
		if h.User == "" {
			h.User = cr.User
		}
	}
	a.nextTemp++
	h.ID = tempPrefix + strconv.Itoa(a.nextTemp)
	h.Normalize()
	if err := h.Validate(); err != nil {
		f.err = fieldError(m, err)
		return false
	}
	return a.quickOpen(m, f, h)
}

// SaveTempAs opens the host editor on the temporary host of the pane id,
// to keep it; its panes then show the saved host.
func (a *App) SaveTempAs(id int) {
	cn := a.conns[id]
	if cn == nil || !isTemp(cn.host.ID) {
		return
	}
	h := cn.host
	a.openEditor(&h)
	a.form.isNew, a.form.promote = true, h.ID
}

// promote moves the panes of the temporary host tmp to the saved h, with
// the password typed for it when h asks for one too.
func (a *App) promote(tmp string, h assets.Host) {
	for _, c := range a.conns {
		if c.host.ID == tmp {
			c.host = h
		}
	}
	if h.Auth == assets.AuthAsk {
		a.mu.Lock()
		if pw := a.typed["ask/"+tmp]; pw != "" {
			a.typed["ask/"+h.ID] = pw
			a.typedGen["ask/"+h.ID]++
		}
		a.mu.Unlock()
	}
	delete(a.temps, tmp)
}

func (a *App) quickView(c *ui.Context, m locales.Messages) {
	f := a.quick
	if f == nil {
		return
	}
	tok := currentTokens(c)
	sheet(ui.Modal(c, &a.quicking, func() {
		ui.Column(c).Gap(18).Width(460).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				tile(c, icons.Quick, theme.Brand, 36)
				ui.Column(c).Gap(1).Shrink(1).Children(func() {
					sheetTitle(c, m.QuickConnect())
					ui.Text(c, m.QuickConnectHint()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
				})
			})
			choices := a.quickChoices(m)
			if f.auth == "" {
				f.auth = choices[0].label
			}
			ui.Form(c, func() {
				ui.Field(c, m.QuickTarget(), func() {
					ui.Column(c).Gap(4).FillWidth().Children(func() {
						in := formTextInput(c.Key("q:target"), &f.target).Placeholder("user@host:22").AutoFocus()
						a.quickHold.apply(c, f, in)
						if in.Submitted() {
							a.QuickConnect(m)
						}
						ui.Text(c, m.QuickStreamHint()).FontSize(theme.Caption).TextColor(tok.TextMuted)
					})
				})
				ui.Field(c, m.FieldAuth(), func() {
					var labels []string
					for _, ch := range choices {
						labels = append(labels, ch.label)
					}
					formSelect(c.Key("q:auth"), &f.auth, labels).Label(m.FieldAuth())
				})
				if f.auth == choices[0].label {
					ui.Field(c, m.FieldPassword(), func() {
						in := formTextInput(c.Key("q:pw"), &f.password).Password().Placeholder(m.QuickPasswordHint())
						if in.Submitted() {
							a.QuickConnect(m)
						}
					})
				}
			})
			ui.Text(c, m.QuickTempNote()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			if f.err != "" {
				ui.Text(c, f.err).FontSize(theme.Footnote).TextColor(tok.Danger)
			}
			ui.Box(c).Height(1).FillWidth().Background(tok.Line)
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					a.quicking = false
				}
				if pill(c, m.Connect(), true, false, strings.TrimSpace(f.target) == "") {
					a.QuickConnect(m)
				}
			})
		})
	}), tok)
	if !a.quicking {
		if f.password != "" {
			f.password = ""
		}
		a.quick = nil
	}
}
