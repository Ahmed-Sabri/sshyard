package app

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// hostForm is the host editor's fields. A host holds no secret: it logs
// in with a credential, the SSH agent, or a password asked each time.
type hostForm struct {
	isNew               bool
	orig                assets.Host
	name, address, port string
	user, group         string
	// icon and color are the host's look as chosen (Host.Icon and
	// Host.Color), "" for those picked from the address; emoji is the
	// icon field's text
	icon, color, emoji string
	// auth is the label chosen in the auth select (see authChoices),
	// "" until the editor first shows
	auth      string
	favorite  bool
	cwdFollow bool
	// metricsIdx is 0 follow, 1 on, 2 off.
	metricsIdx int
	// proxy is the name chosen in the proxy select ("" none); jumps the
	// jump chain's host IDs, first hop first; jumpPick the add select
	proxy    string
	jumps    []string
	jumpPick string
	jumpList ui.ListState
	netOpen  bool
	// osc52 is the override's label once Advanced is open; osc52Code is
	// the saved policy ("", allow, ask, deny), so a save before that
	// keeps it. advOpen shows Advanced.
	osc52, osc52Code string
	advOpen          bool
	err              string
	// AI permission override. aiIdx 0 inherits. fullOK is set once the
	// full-access sheet has been confirmed for this edit.
	aiIdx, aiPrev int
	roots, sens   string
	aiOpen        bool
	fullOK        bool
	// promote is the temporary host being saved, whose panes then show
	// the new host
	promote string
}

// authChoice is one option of the editor's auth select.
type authChoice struct {
	label  string
	method assets.AuthMethod
	cred   string
}

// authChoices are the editor's ways to log in: asking each time, the
// SSH agent, then every credential.
func (a *App) authChoices(m locales.Messages) []authChoice {
	out := []authChoice{{m.AuthAsk(), assets.AuthAsk, ""}, {m.AuthAgent(), assets.AuthAgent, ""}}
	for _, cr := range a.store.Credentials() {
		kind := m.CredKindPassword()
		if cr.Kind == assets.CredKey {
			kind = m.CredKindKey()
		}
		out = append(out, authChoice{cr.Name + " · " + kind, assets.AuthCredential, cr.ID})
	}
	return out
}

func (a *App) authLabel(m locales.Messages, h assets.Host) string {
	for _, ch := range a.authChoices(m) {
		if ch.method == h.Auth && ch.cred == h.CredentialID {
			return ch.label
		}
	}
	return m.AuthAsk()
}

func (a *App) openEditor(h *assets.Host) {
	f := &hostForm{isNew: h == nil, port: "22", cwdFollow: true}
	if h != nil {
		f.orig = *h
		f.name, f.address, f.port = h.Name, h.Address, strconv.Itoa(h.Port)
		f.user, f.group, f.favorite, f.cwdFollow = h.User, h.Group, h.Favorite, h.CwdFollow
		f.icon, f.color = h.Icon, h.Color
		if !strings.HasPrefix(h.Icon, assets.IconPrefix) {
			f.emoji = h.Icon
		}
		f.metricsIdx = metricsIndex(h.Metrics)
		f.jumps = slices.Clone(h.JumpChain)
		if p, ok := a.store.Proxy(h.ProxyID); ok {
			f.proxy = p.Name
		}
		f.netOpen = f.proxy != "" || len(f.jumps) > 0
		f.osc52Code = h.OSC52
		f.advOpen = h.OSC52 != ""
		f.aiIdx = hostModeIndex(h.AIPolicy.Mode)
		f.aiPrev = f.aiIdx
		f.roots = strings.Join(h.AIPolicy.WritableRoots, "\n")
		f.sens = strings.Join(h.AIPolicy.Sensitive, "\n")
		f.aiOpen = h.AIPolicy.Mode != "" || f.roots != "" || f.sens != ""
	}
	a.form, a.editing = f, true
}

func osc52Choices(m locales.Messages) (labels, codes []string) {
	return []string{m.Osc52Follow(), m.Osc52Allow(), m.Osc52Ask(), m.Osc52Deny()},
		[]string{"", settings.OSC52Allow, settings.OSC52Ask, settings.OSC52Deny}
}

// syncOSC52Label makes the select's label match the saved code, and
// remembers a label the user already picked so a language change keeps it.
func syncOSC52Label(m locales.Messages, f *hostForm) []string {
	labels, codes := osc52Choices(m)
	if i := slices.Index(labels, f.osc52); i >= 0 {
		f.osc52Code = codes[i]
		return labels
	}
	f.osc52 = labels[0]
	for i, code := range codes {
		if code == f.osc52Code {
			f.osc52 = labels[i]
		}
	}
	return labels
}

// osc52CodeFrom is the policy to save: the label when Advanced has shown
// it, otherwise the code the host already had.
func osc52CodeFrom(m locales.Messages, f *hostForm) string {
	labels, codes := osc52Choices(m)
	if i := slices.Index(labels, f.osc52); i >= 0 {
		return codes[i]
	}
	return f.osc52Code
}

// fieldError is a host field's problem in the user's language.
func fieldError(m locales.Messages, err error) string {
	var fe *assets.FieldError
	if !errors.As(err, &fe) {
		return m.ErrGeneric(err.Error())
	}
	label := map[string]string{"name": m.FieldName(), "address": m.FieldHost(), "port": m.FieldPort(),
		"user": m.FieldUser(), "auth": m.FieldAuth(), "credential": m.FieldAuth()}[fe.Field]
	if fe.Field == "port" {
		return m.InvalidPort()
	}
	if fe.Reason == "taken" {
		return m.NameTaken()
	}
	return m.RequiredField(label)
}

// SaveForm validates the editor and saves the host.
func (a *App) SaveForm(m locales.Messages) bool {
	f := a.form
	if f == nil {
		return false
	}
	port, err := strconv.Atoi(strings.TrimSpace(f.port))
	if err != nil || port < 1 || port > 65535 {
		f.err = m.InvalidPort()
		return false
	}
	h := f.orig
	h.Name, h.Address, h.Port, h.User, h.Group = f.name, f.address, port, f.user, f.group
	h.Favorite, h.CwdFollow, h.Metrics = f.favorite, f.cwdFollow, metricsMode(f.metricsIdx)
	h.Icon, h.Color = f.icon, f.color
	h.Auth, h.CredentialID = assets.AuthAsk, ""
	for _, ch := range a.authChoices(m) {
		if ch.label == f.auth {
			h.Auth, h.CredentialID = ch.method, ch.cred
		}
	}
	h.ProxyID = ""
	labels, ids := a.proxyChoices(m)
	for i, l := range labels {
		if l == f.proxy && i > 0 {
			h.ProxyID = ids[i]
		}
	}
	h.JumpChain = slices.Clone(f.jumps)
	h.OSC52 = osc52CodeFrom(m, f)
	mode := ""
	if f.aiIdx >= 0 && f.aiIdx < 4 {
		mode = []string{"", "ask", "review", "full"}[f.aiIdx]
	}
	if mode == "full" && f.orig.AIPolicy.Mode != "full" && !f.fullOK {
		a.openFull("host", 0)
		return false
	}
	h.AIPolicy.Mode = mode
	h.AIPolicy.WritableRoots = linesOf(f.roots)
	h.AIPolicy.Sensitive = linesOf(f.sens)
	h.Normalize()
	if err := h.Validate(); err != nil {
		f.err = fieldError(m, err)
		return false
	}
	if f.isNew {
		h, err = a.store.Add(h)
	} else {
		h, err = a.store.Update(h)
	}
	if errors.Is(err, assets.ErrJumpLoop) {
		f.err, f.netOpen = m.ErrJumpLoop(), true
		return false
	}
	if err != nil {
		f.err = fieldError(m, err)
		return false
	}
	if f.promote != "" {
		a.promote(f.promote, h)
	}
	a.selected = h.ID
	a.form, a.editing = nil, false
	return true
}

// DeleteHost removes a host, unless other hosts jump through it or
// forwards use it (an *assets.InUseError naming them).
func (a *App) DeleteHost(id string) error {
	if err := a.store.Delete(id); err != nil {
		return err
	}
	a.mu.Lock()
	delete(a.typed, "ask/"+id)
	a.mu.Unlock()
	if a.selectedHost() == "" {
		a.selected = ""
	}
	return nil
}

// inUseText says what still uses an entry that cannot be deleted.
func (a *App) inUseText(m locales.Messages, err error) string {
	var iu *assets.InUseError
	var kd *keychainDeleteError
	if errors.As(err, &kd) {
		return m.ErrKeychainDelete(kd.err.Error())
	}
	if !errors.As(err, &iu) {
		return m.ErrGeneric(err.Error())
	}
	var names []string
	for _, id := range iu.Hosts {
		if h, ok := a.store.Get(id); ok {
			id = h.Label()
		}
		names = append(names, id)
	}
	for _, id := range iu.Forwards {
		if f, ok := a.store.Forward(id); ok {
			id = f.Label()
		}
		names = append(names, id)
	}
	return m.InUse(strings.Join(names, ", "))
}

func (a *App) editorView(c *ui.Context, m locales.Messages) {
	if a.form != nil {
		f := a.form
		tok := currentTokens(c)
		sheet(ui.Modal(c, &a.editing, func() {
			// The form scrolls between the title and the buttons when the
			// window is too short for it.
			ui.Column(c).Gap(18).Width(480).Shrink(1).MinHeight(0).Children(func() {
				title := m.EditHost()
				switch {
				case f.promote != "":
					title = m.SaveAsHost()
				case f.isNew:
					title = m.NewHost()
				}
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					name := f.name
					if name == "" {
						name = f.address
					}
					hostAvatar(c, tok, f.look(), 36)
					ui.Column(c).Gap(1).Shrink(1).Children(func() {
						sheetTitle(c, title)
						if name != "" {
							ui.Text(c, name).FontSize(theme.Footnote).TextColor(tok.TextMuted).SingleLine()
						}
					})
				})
				ui.Scroll(c.Key("f:scroll")).Shrink(1).MinHeight(0).FillWidth().Children(func() {
					ui.Form(c, func() {
						ui.Field(c, m.FieldName(), func() {
							in := ui.TextInput(c.Key("f:name"), &f.name).Placeholder("web-01").AutoFocus()
							a.editorHold.apply(c, f, in)
						})
						ui.Field(c, m.FieldHost(), func() { ui.TextInput(c.Key("f:addr"), &f.address).Placeholder("example.com") })
						ui.Field(c, m.FieldPort(), func() { ui.TextInput(c.Key("f:port"), &f.port).Width(90) })
						ui.Field(c, m.FieldUser(), func() { ui.TextInput(c.Key("f:user"), &f.user).Placeholder("root") })
						ui.Field(c, m.FieldGroup(), func() { ui.TextInput(c.Key("f:group"), &f.group) })
						ui.Field(c, m.FieldAppearance(), func() { a.lookFields(c, m, tok, f) })
						ui.Field(c, m.FieldAuth(), func() {
							if f.auth == "" {
								f.auth = a.authLabel(m, f.orig)
							}
							var labels []string
							for _, ch := range a.authChoices(m) {
								labels = append(labels, ch.label)
							}
							labels = append(labels, m.NewCredentialChoice())
							prev := f.auth
							if ui.Select(c.Key("f:auth"), &f.auth, labels).Label(m.FieldAuth()).Changed() && f.auth == m.NewCredentialChoice() {
								// The credential sheet replaces the editor, then
								// comes back with the new credential chosen.
								f.auth = prev
								a.openCredForm(nil, f)
								a.editing = false
							}
						})
						ui.Field(c, "", func() {
							ui.Collapsible(c.Key("f:net"), m.Network(), &f.netOpen, func() {
								a.networkFields(c, m, tok, f)
							})
						})
						ui.Field(c, m.MetricsBar(), func() {
							ui.Column(c).Gap(4).Children(func() {
								ui.Segmented(c.Key("f:met"), &f.metricsIdx, m.MetricsFollow(), m.MetricsHostOn(), m.MetricsHostOff()).Label(m.MetricsBar()).Disabled(!a.settings.MetricsEnabled)
								if !a.settings.MetricsEnabled {
									ui.Text(c, m.MetricsDisabledGlobally()).FontSize(theme.Caption).TextColor(tok.TextMuted)
								}
							})
						})
						ui.Field(c, "", func() {
							ui.Collapsible(c.Key("f:adv"), m.Advanced(), &f.advOpen, func() {
								labels := syncOSC52Label(m, f)
								ui.Column(c).Gap(6).FillWidth().Margin(8, 0, 0, 0).Children(func() {
									ui.Text(c, m.Osc52Title()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
									ui.Select(c.Key("f:osc52"), &f.osc52, labels).Label(m.Osc52Title()).FillWidth()
								})
							})
						})
						ui.Field(c, "", func() {
							a.aiHostFields(c, m, tok, f)
						})
						ui.Field(c, "", func() {
							ui.Column(c).Gap(6).Children(func() {
								ui.Checkbox(c.Key("f:cwd"), &f.cwdFollow, m.CwdFollow())
								ui.Text(c, m.HostCwdFollowHint()).FontSize(theme.Caption).TextColor(tok.TextMuted)
								ui.Checkbox(c.Key("f:fav"), &f.favorite, m.Favorite())
							})
						})
					})
				})
				if f.err != "" {
					ui.Text(c, f.err).FontSize(theme.Footnote).TextColor(tok.Danger)
				}
				ui.Box(c).Height(1).FillWidth().Background(tok.Line)
				ui.Row(c).Gap(8).Children(func() {
					if !f.isNew && pill(c, m.Delete(), false, true, false) {
						a.deleteID, a.deleting = f.orig.ID, true
						a.editing = false
					}
					ui.Spacer(c)
					if pill(c, m.Cancel(), false, false, false) {
						a.editing = false
					}
					if pill(c, m.Save(), true, false, false) {
						a.SaveForm(m)
					}
				})
			})
		}), tok)
		if !a.editing && (a.credForm == nil || a.credForm.back != f) && (a.proxyForm == nil || a.proxyForm.back != f) {
			a.form = nil
		}
	}
	if a.deleting {
		name := a.deleteID
		if h, ok := a.store.Get(a.deleteID); ok {
			name = h.Label()
		}
		switch ui.AlertDialog(c, &a.deleting, m.DeleteHost(), m.DeleteHostConfirm(name), m.Cancel(), m.Delete()) {
		case 1:
			if err := a.DeleteHost(a.deleteID); err != nil {
				c.AddToast(ui.Toast{Title: a.inUseText(m, err), Type: "error"})
			}
		}
	}
}

// networkFields are the editor's network section: the proxy of the first
// hop, and the jump hosts dialed through in order, which drag reorders.
func (a *App) networkFields(c *ui.Context, m locales.Messages, tok theme.Tokens, f *hostForm) {
	ui.Column(c).Gap(10).FillWidth().Margin(8, 0, 0, 0).Children(func() {
		labels, _ := a.proxyChoices(m)
		if f.proxy == "" {
			f.proxy = labels[0]
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.FieldProxy()).FontSize(theme.Footnote).TextColor(tok.TextMuted).Width(92)
			prev := f.proxy
			all := append(slices.Clone(labels), m.NewProxyChoice())
			if ui.Select(c.Key("f:proxy"), &f.proxy, all).Label(m.FieldProxy()).Grow(1).Changed() && f.proxy == m.NewProxyChoice() {
				// The proxy sheet replaces the editor, then comes back
				// with the new proxy chosen.
				f.proxy = prev
				a.openProxyForm(nil, f)
				a.editing = false
			}
		})
		ui.Text(c, m.FieldJumpHosts()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
		if len(f.jumps) == 0 {
			ui.Text(c, m.NoJumpHosts()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
		} else {
			f.jumpList.Key = func(i int) any { return f.jumps[i] }
			f.jumpList.Label = func(i int) string { return a.hostLabel(f.jumps[i]) }
			f.jumpList.Reorder = func(rows []int, to int) { f.jumps = moveRows(f.jumps, rows, to) }
			ui.List(c.Key("f:jumps"), &f.jumpList, len(f.jumps), func(i int) {
				id := f.jumps[i]
				ui.Row(c.Key("jump:"+id)).Height(34).Gap(8).Padding(0, 4, 0, 8).AlignItems(ui.Center).FillWidth().Children(func() {
					ui.Text(c, strconv.Itoa(i+1)).FontSize(theme.Caption).TextColor(tok.TextMuted).Width(14)
					tile(c, icons.JumpHost, tok.Tint(id), 20)
					ui.Text(c, a.hostLabel(id)).FontSize(theme.Body).SingleLine().Grow(1).Shrink(1)
					if iconButton(c, "jump:rm:"+id, icons.Close, m.RemoveJumpHost()) {
						f.jumps = slices.DeleteFunc(slices.Clone(f.jumps), func(s string) bool { return s == id })
					}
				})
			}).Height(float32(34*min(len(f.jumps), 5))).FillWidth().Radius(theme.RadiusControl).Border(1, tok.Line).Background(tok.Fill)
		}
		var pick []string
		var pickIDs []string
		for _, h := range a.store.List() {
			if h.ID != f.orig.ID && !slices.Contains(f.jumps, h.ID) {
				pick, pickIDs = append(pick, h.Label()), append(pickIDs, h.ID)
			}
		}
		if len(pick) > 0 {
			f.jumpPick = m.AddJumpHost()
			if ui.Select(c.Key("f:jumpadd"), &f.jumpPick, append([]string{m.AddJumpHost()}, pick...)).Label(m.AddJumpHost()).FillWidth().Changed() {
				if i := slices.Index(pick, f.jumpPick); i >= 0 {
					f.jumps = append(slices.Clone(f.jumps), pickIDs[i])
				}
				f.jumpPick = m.AddJumpHost()
			}
		}
	})
}

// hostLabel is the label of the host of id, or id when it is gone.
func (a *App) hostLabel(id string) string {
	if h, ok := a.store.Get(id); ok {
		return h.Label()
	}
	return id
}

// moveRows moves the rows of s (in order) to before index to.
func moveRows[T any](s []T, rows []int, to int) []T {
	var moved, rest []T
	before := 0
	for i, v := range s {
		if slices.Contains(rows, i) {
			moved = append(moved, v)
			if i < to {
				before++
			}
		} else {
			rest = append(rest, v)
		}
	}
	to -= before
	return slices.Concat(rest[:to], moved, rest[to:])
}
