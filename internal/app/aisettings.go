package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Licoy/sshyard/internal/ai/openai"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// providerForm edits one built-in provider. The API key is never written
// into settings; an empty key on edit keeps the keychain entry.
type providerForm struct {
	id                string
	name, base, model string
	key, headers      string
	temp, tokens      float64
	isNew             bool
	err, warn, test   string
	testing           bool
}

func (a *App) aiSettings(c *ui.Context, m locales.Messages, tok theme.Tokens, s *settings.Settings, changed *bool) {
	settingsSection(c, tok, icons.Robot, tok.TintAt(3), m.AiSettings(), func(g *settingsGroup) {
		g.row(m.AiDefaultAgent(), "", func() {
			opts := []string{m.AiBuiltin()}
			cur := m.AiBuiltin()
			if s.AIDefaultAgent != "" && s.AIDefaultAgent != "builtin" {
				opts = append(opts, s.AIDefaultAgent)
				cur = s.AIDefaultAgent
			}
			if ui.Select(c.Key("aiagentdef"), &cur, opts).Label(m.AiDefaultAgent()).Width(180).Changed() {
				if cur == m.AiBuiltin() {
					s.AIDefaultAgent = "builtin"
				} else {
					s.AIDefaultAgent = cur
				}
				*changed = true
			}
		})
		g.row(m.AiDefaultMode(), "", func() {
			a.aiModeIdx = modeIndex(s.AIDefaultMode)
			if ui.Segmented(c.Key("aimodedef"), &a.aiModeIdx, m.AiModeAsk(), m.AiModeReview(), m.AiModeFull()).Label(m.AiDefaultMode()).Changed() {
				mode := []string{"ask", "review", "full"}[a.aiModeIdx]
				if mode == "full" && a.settings.AIDefaultMode != "full" {
					a.openFull("settings", 0)
				} else {
					s.AIDefaultMode = mode
					*changed = true
				}
			}
		})
		g.row(m.AiReviewProvider(), "", func() {
			opts := []string{m.AiReviewNone()}
			ids := []string{""}
			cur := m.AiReviewNone()
			for _, p := range s.AIProviders {
				label := providerLabel(p)
				opts = append(opts, label)
				ids = append(ids, p.ID)
				if p.ID == s.AIReviewProvider {
					cur = label
				}
			}
			if ui.Select(c.Key("aireview"), &cur, opts).Label(m.AiReviewProvider()).Width(180).Changed() {
				s.AIReviewProvider = ""
				for i, label := range opts {
					if label == cur {
						s.AIReviewProvider = ids[i]
					}
				}
				*changed = true
			}
		})
		g.row(m.AiContextLines(), "", func() {
			if ui.NumberInput(c.Key("ailines"), &a.aiLines, 50, 2000, 10).Width(76).Changed() {
				s.AIContextLines = int(a.aiLines)
				*changed = true
			}
		})
		g.row(m.AiAuditDays(), "", func() {
			if ui.NumberInput(c.Key("aidays"), &a.aiDays, 1, 3650, 1).Width(76).Changed() {
				s.AIAuditRetentionDays = int(a.aiDays)
				*changed = true
			}
		})
		g.row(m.AiRedact(), m.AiRedactNote(), func() {
			on := s.AIRedact
			if ui.Switch(c.Key("airedact"), &on).Label(m.AiRedact()).Changed() {
				s.AIRedact = on
				*changed = true
			}
		})
		g.divider()
		if len(s.AIConsentHosts) == 0 {
			ui.Row(c).MinHeight(44).Padding(8, 14).Children(func() {
				ui.Text(c, m.AiConsentEmpty()).FontSize(theme.Body).TextColor(tok.TextMuted)
			})
		}
		for _, id := range s.AIConsentHosts {
			id := id
			label := id
			if h, ok := a.host(id); ok {
				label = h.Label()
			}
			g.row(label, "", func() {
				if iconButton(c, "consent:"+id, icons.Delete, m.AiForgetConsent()) {
					a.forgetConsent(id)
				}
			})
		}
		g.divider()
		if len(s.AIProviders) == 0 {
			ui.Row(c).MinHeight(44).Padding(8, 14).Children(func() {
				ui.Text(c, m.AiNoProvider()).FontSize(theme.Body).TextColor(tok.TextMuted)
			})
		}
		for _, p := range s.AIProviders {
			p := p
			g.row(providerLabel(p), p.BaseURL, func() {
				if iconButton(c, "prov:edit:"+p.ID, icons.Edit, m.Edit()) {
					pp := p
					a.openProvider(&pp)
				}
				if iconButton(c, "prov:del:"+p.ID, icons.Delete, m.Delete()) {
					a.provDeleteID, a.provDeleting = p.ID, true
				}
			})
		}
		g.divider()
		ui.Row(c).Padding(8, 14).Children(func() {
			if pill(c, m.AiAddProvider(), true, false, false) {
				a.openProvider(nil)
			}
		})
	})
}

// forgetConsent drops one host's first-use confirmation and saves it.
// A save error leaves the in-memory list unchanged and shows a toast.
func (a *App) forgetConsent(hostID string) {
	old := a.settings
	s := a.settings
	next := make([]string, 0, len(s.AIConsentHosts))
	for _, id := range s.AIConsentHosts {
		if id != hostID {
			next = append(next, id)
		}
	}
	s.AIConsentHosts = next
	if err := a.ApplySettings(s); err != nil {
		a.settings = old
		a.toast(func(m locales.Messages) ui.Toast {
			return ui.Toast{Title: m.ErrGeneric(err.Error()), Type: "error"}
		})
	}
}

func providerLabel(p settings.AIProvider) string {
	if p.Name != "" && p.Model != "" {
		return p.Name + " · " + p.Model
	}
	if p.Name != "" {
		return p.Name
	}
	if p.Model != "" {
		return p.Model
	}
	return p.ID
}

func (a *App) openProvider(p *settings.AIProvider) {
	f := &providerForm{isNew: p == nil}
	if p != nil {
		f.id, f.name, f.base, f.model = p.ID, p.Name, p.BaseURL, p.Model
		f.temp, f.tokens = p.Temperature, float64(p.MaxTokens)
		var b strings.Builder
		for k, v := range p.Headers {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
		f.headers = b.String()
	}
	a.prov, a.provOpen = f, true
}

func (a *App) providerSheet(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	if a.prov == nil {
		return
	}
	f := a.prov
	title := m.AiAddProvider()
	if !f.isNew {
		title = m.AiEditProvider()
	}
	sheet(ui.Modal(c.Key("aiprov"), &a.provOpen, func() {
		ui.Column(c).Gap(12).Width(440).Children(func() {
			sheetTitle(c, title)
			ui.Field(c, m.FieldName(), func() {
				ui.TextInput(c.Key("ap:name"), &f.name).Placeholder("openai")
			})
			ui.Field(c, m.AiProviderURL(), func() {
				ui.TextInput(c.Key("ap:url"), &f.base).Placeholder("https://api.example.test/v1")
			})
			ui.Field(c, m.AiProviderModel(), func() {
				ui.TextInput(c.Key("ap:model"), &f.model).Placeholder("fake-model")
			})
			ui.Row(c).Gap(12).Children(func() {
				ui.Field(c, m.AiProviderTemp(), func() {
					ui.NumberInput(c.Key("ap:temp"), &f.temp, 0, 2, 0.1).Width(90)
				})
				ui.Field(c, m.AiProviderTokens(), func() {
					ui.NumberInput(c.Key("ap:tok"), &f.tokens, 0, 200000, 1).Width(110)
				})
			})
			ui.Field(c, m.AiAPIKey(), func() {
				ui.TextInput(c.Key("ap:key"), &f.key).Password().Placeholder(m.AiAPIKeyNote())
			})
			ui.Text(c, m.AiAPIKeyNote()).FontSize(theme.Caption).TextColor(tok.TextMuted)
			ui.Field(c, m.AiHeaders(), func() {
				ui.TextArea(c.Key("ap:hdr"), &f.headers).FillWidth().Height(72).Placeholder("X-Title: SSHYard")
			})
			ui.Text(c, m.AiHeadersNote()).FontSize(theme.Caption).TextColor(tok.TextMuted)
			if f.warn != "" {
				ui.Text(c, f.warn).FontSize(theme.Footnote).TextColor(tok.Warning)
			}
			if f.err != "" {
				ui.Text(c, f.err).FontSize(theme.Footnote).TextColor(tok.Danger)
			}
			if f.test == "ok" {
				ui.Text(c, m.AiTestOK()).FontSize(theme.Footnote).TextColor(tok.Success)
			} else if f.test == "key" {
				ui.Text(c, m.AiNoKey()).FontSize(theme.Footnote).TextColor(tok.Danger)
			} else if f.test != "" {
				ui.Text(c, m.AiTestFail(f.test)).FontSize(theme.Footnote).TextColor(tok.Danger)
			}
			ui.Row(c).Gap(8).Children(func() {
				if pill(c, m.AiTestConnection(), false, false, f.testing) {
					a.testProvider()
				}
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					a.provOpen = false
				}
				if pill(c, m.Save(), true, false, false) {
					a.saveProvider(m)
				}
			})
		})
	}), tok)
	if !a.provOpen {
		a.prov = nil
	}
}

func (a *App) providerDelete(c *ui.Context, m locales.Messages) {
	if !a.provDeleting {
		return
	}
	name := a.provDeleteID
	if p := a.providerByID(a.provDeleteID); p != nil && p.Name != "" {
		name = p.Name
	}
	switch ui.AlertDialog(c, &a.provDeleting, m.Delete(), m.AiDeleteProviderConfirm(name), m.Cancel(), m.Delete()) {
	case 1:
		if err := a.deleteProvider(a.provDeleteID); err != nil {
			c.AddToast(ui.Toast{Title: m.ErrGeneric(err.Error()), Type: "error"})
		}
	}
}

func (a *App) saveProvider(m locales.Messages) bool {
	f := a.prov
	if f == nil {
		return false
	}
	f.err, f.warn = "", ""
	name, base, model := strings.TrimSpace(f.name), strings.TrimSpace(f.base), strings.TrimSpace(f.model)
	if name == "" || base == "" || model == "" {
		f.err = m.RequiredField(m.FieldName())
		return false
	}
	warn, err := settings.CheckBaseURL(base)
	if err != nil {
		f.err = m.AiBadURL()
		return false
	}
	if warn != nil {
		f.warn = m.AiHTTPWarn()
	}
	hdr, err := parseHeaders(f.headers)
	if err != nil {
		f.err = m.AiBadHeader()
		return false
	}
	id := f.id
	if id == "" {
		id = newProviderID()
	}
	if key := strings.TrimSpace(f.key); key != "" {
		if err := a.kc.Set(keychain.AIKey(id), []byte(key)); err != nil {
			f.err = m.ErrGeneric(err.Error())
			return false
		}
	} else if f.isNew {
		f.err = m.AiNoKey()
		return false
	}
	p := settings.AIProvider{ID: id, Name: name, BaseURL: base, Model: model, Headers: hdr, Temperature: f.temp, MaxTokens: int(f.tokens)}
	s := a.settings
	next := append([]settings.AIProvider{}, s.AIProviders...)
	found := false
	for i := range next {
		if next[i].ID == id {
			next[i] = p
			found = true
		}
	}
	if !found {
		next = append(next, p)
	}
	s.AIProviders = next
	if err := a.ApplySettings(s); err != nil {
		f.err = m.ErrGeneric(err.Error())
		return false
	}
	f.key = ""
	f.id = id
	// A plain-HTTP warning stays on the open sheet; the profile is saved.
	if warn == nil {
		a.provOpen = false
	}
	return true
}

func (a *App) deleteProvider(id string) error {
	if err := a.kc.Delete(keychain.AIKey(id)); err != nil && !errors.Is(err, keychain.ErrNotFound) {
		return err
	}
	s := a.settings
	next := make([]settings.AIProvider, 0, len(s.AIProviders))
	for _, p := range s.AIProviders {
		if p.ID != id {
			next = append(next, p)
		}
	}
	s.AIProviders = next
	if s.AIReviewProvider == id {
		s.AIReviewProvider = ""
	}
	for _, c := range a.conns {
		if c.ai != nil && c.ai.provider == id {
			c.ai.provider = ""
			a.saveLayout()
		}
	}
	return a.ApplySettings(s)
}

func (a *App) testProvider() {
	f := a.prov
	if f == nil || f.testing {
		return
	}
	key := strings.TrimSpace(f.key)
	if key == "" && f.id != "" {
		if b, err := a.kc.Get(keychain.AIKey(f.id)); err == nil {
			key = string(b)
		}
	}
	if key == "" {
		f.test = "key"
		return
	}
	cl := &openai.Client{BaseURL: strings.TrimSpace(f.base), APIKey: key, Model: strings.TrimSpace(f.model)}
	f.testing = true
	f.test = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := cl.Test(ctx)
		a.post(func() {
			if a.prov == nil {
				return
			}
			a.prov.testing = false
			if err != nil {
				a.prov.test = strings.ReplaceAll(err.Error(), key, "[redacted]")
				return
			}
			a.prov.test = "ok"
		})
	}()
}

func parseHeaders(s string) (map[string]string, error) {
	h := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return nil, settings.ErrHeader
		}
		h[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if len(h) == 0 {
		return nil, nil
	}
	if err := settings.CheckHeaders(h); err != nil {
		return nil, err
	}
	return h, nil
}

func newProviderID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "p" + strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return "p" + hex.EncodeToString(b[:])
}

func linesOf(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.ContainsRune(line, 0) {
			out = append(out, line)
		}
	}
	return out
}

func hostModeIndex(mode string) int {
	switch mode {
	case "ask":
		return 1
	case "review":
		return 2
	case "full":
		return 3
	default:
		return 0
	}
}

// aiHostFields is the host editor's AI section.
func (a *App) aiHostFields(c *ui.Context, m locales.Messages, tok theme.Tokens, f *hostForm) {
	ui.Collapsible(c.Key("f:ai"), m.AiSettings(), &f.aiOpen, func() {
		ui.Column(c).Gap(8).FillWidth().Margin(8, 0, 0, 0).Children(func() {
			ui.Text(c, m.AiHostMode()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			if ui.Segmented(c.Key("f:aimode"), &f.aiIdx, m.AiModeInherit(), m.AiModeAsk(), m.AiModeReview(), m.AiModeFull()).Label(m.AiHostMode()).Changed() {
				if f.aiIdx == 3 && f.orig.AIPolicy.Mode != "full" && !f.fullOK {
					f.aiIdx = f.aiPrev
					a.openFull("host", 0)
				} else {
					f.aiPrev = f.aiIdx
				}
			}
			ui.Text(c, m.AiWritableRoots()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			ui.Text(c, m.AiWritableRootsNote()).FontSize(theme.Caption).TextColor(tok.TextMuted)
			ui.TextArea(c.Key("f:roots"), &f.roots).FillWidth().Height(64)
			ui.Text(c, m.AiSensitive()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			ui.Text(c, m.AiSensitiveNote()).FontSize(theme.Caption).TextColor(tok.TextMuted)
			ui.TextArea(c.Key("f:sens"), &f.sens).FillWidth().Height(64)
		})
	})
}
