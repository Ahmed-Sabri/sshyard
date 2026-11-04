package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/keys"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// credForm is the credential editor's fields. Its secrets live here only
// while the sheet is open; saving moves them into the keychain.
type credForm struct {
	isNew bool
	orig  assets.Credential
	name  string
	// kind is the segmented control's index: 0 a password, 1 a key
	kind       int
	user, note string
	password   string
	// keyData is a key read from a file, pasted or dropped, not yet
	// decrypted; keyFrom names its file ("" pasted)
	keyData []byte
	keyFrom string
	// genKey is a key generated in the sheet, encoded on save
	genKey ed25519.PrivateKey
	// preview is what keyData (or genKey) reads as without the
	// passphrase; previewErr why it is no key
	preview    keys.Key
	previewErr error
	passphrase string
	askPass    bool
	err        string
	// back is the host editor that opened the sheet ("New credential…"),
	// shown again with the new credential chosen
	back *hostForm
}

func (f *credForm) isKey() bool { return f.kind == 1 }

// loaded is whether the sheet has a new key to save.
func (f *credForm) loaded() bool { return f.genKey != nil || f.keyData != nil }

// setKey takes a key read from a file, the clipboard or a drop.
func (f *credForm) setKey(data []byte, from string) {
	f.keyData, f.keyFrom, f.genKey, f.err = data, from, nil, ""
	f.preview, f.previewErr = keys.Import(data, nil)
	if errors.Is(f.previewErr, keys.ErrPassphraseNeeded) {
		f.previewErr = nil
	}
}

// generate makes a new ed25519 key for the sheet.
func (f *credForm) generate() {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		f.err = err.Error()
		return
	}
	f.keyData, f.keyFrom, f.genKey, f.err = nil, "", priv, ""
	f.preview, f.previewErr = keys.Encode(priv, f.comment(), nil)
}

func (f *credForm) comment() string {
	if n := strings.TrimSpace(f.name); n != "" {
		return n
	}
	return "sshyard"
}

// encrypted is whether the key the sheet will save needs a passphrase.
func (f *credForm) encrypted() bool {
	switch {
	case f.genKey != nil:
		return f.passphrase != "" || f.askPass
	case f.keyData != nil:
		return f.preview.Encrypted
	}
	return f.orig.Kind == assets.CredKey && f.orig.Encrypted
}

func (a *App) openCredForm(cr *assets.Credential, back *hostForm) {
	f := &credForm{isNew: cr == nil, back: back}
	if cr != nil {
		f.orig = *cr
		f.name, f.user, f.note, f.askPass = cr.Name, cr.User, cr.Note, cr.AskPassphrase
		if cr.Kind == assets.CredKey {
			f.kind = 1
		}
	}
	a.credForm, a.credEditing = f, true
}

// closeCredForm closes the sheet, back to the host editor that opened it.
func (a *App) closeCredForm() {
	f := a.credForm
	a.credForm, a.credEditing = nil, false
	if f != nil && f.back != nil {
		a.form, a.editing = f.back, true
	}
}

// keyError is a key that cannot be read, in the user's language.
func keyError(m locales.Messages, err error) string {
	var ue *keys.UnsupportedError
	switch {
	case errors.Is(err, keys.ErrPassphraseNeeded):
		return m.ErrKeyPassphraseNeeded()
	case errors.Is(err, keys.ErrPassphraseWrong):
		return m.ErrKeyPassphraseWrong()
	case errors.Is(err, keys.ErrPublicKey):
		return m.ErrPublicKey()
	case errors.Is(err, keys.ErrNotAKey):
		return m.ErrNotAKey()
	case errors.Is(err, keys.ErrCorrupt):
		return m.ErrKeyCorrupt()
	case errors.As(err, &ue):
		return m.ErrKeyUnsupported(ue.What)
	}
	return m.ErrGeneric(err.Error())
}

// SaveCredForm validates the credential sheet, saves the credential and
// moves its secrets into the keychain.
func (a *App) SaveCredForm(m locales.Messages) bool {
	f := a.credForm
	if f == nil {
		return false
	}
	f.err = ""
	cr := f.orig
	cr.Name, cr.User, cr.Note = f.name, f.user, f.note
	kind := assets.CredPassword
	if f.isKey() {
		kind = assets.CredKey
	}
	fresh := f.isNew || cr.Kind != kind // nothing of this kind saved yet
	cr.Kind = kind
	set := map[keychain.Kind][]byte{}
	var drop []keychain.Kind
	if kind == assets.CredPassword {
		if f.password == "" && fresh {
			f.err = m.CredPasswordRequired()
			return false
		}
		if f.password != "" {
			set[keychain.Password] = []byte(f.password)
		}
		drop = []keychain.Kind{keychain.PrivateKey, keychain.Passphrase}
		cr.KeyType, cr.Fingerprint, cr.Encrypted, cr.AskPassphrase = "", "", false, false
	} else {
		drop = []keychain.Kind{keychain.Password}
		pass := []byte(f.passphrase)
		var k keys.Key
		var err error
		switch {
		case f.genKey != nil:
			k, err = keys.Encode(f.genKey, f.comment(), pass)
		case f.keyData != nil:
			if f.previewErr != nil {
				f.err = keyError(m, f.previewErr)
				return false
			}
			k, err = keys.Import(f.keyData, pass)
			// An OpenSSH key asked for each run is saved unread: its
			// public half names it.
			if errors.Is(err, keys.ErrPassphraseNeeded) && f.askPass && k.Public != nil && !keys.IsPPK(f.keyData) {
				err = nil
			}
		case fresh:
			f.err = m.CredKeyRequired()
			return false
		default:
			// The saved key stays; a passphrase typed now must open it.
			if cr.Encrypted && len(pass) > 0 {
				pem, gerr := a.kc.Get(keychain.Cred(cr.ID, keychain.PrivateKey))
				if gerr == nil {
					_, err = keys.Import(pem, pass)
				}
			}
		}
		if err != nil {
			f.err = keyError(m, err)
			return false
		}
		if f.loaded() {
			set[keychain.PrivateKey] = k.PEM
			cr.KeyType, cr.Fingerprint, cr.Encrypted = k.Type(), k.Fingerprint(), k.Encrypted
		}
		cr.AskPassphrase = cr.Encrypted && f.askPass
		switch {
		case !cr.Encrypted || cr.AskPassphrase:
			drop = append(drop, keychain.Passphrase)
		case len(pass) > 0:
			set[keychain.Passphrase] = pass
		case !a.kc.Has(keychain.Cred(cr.ID, keychain.Passphrase)) || f.loaded():
			f.err = m.ErrKeyPassphraseNeeded()
			return false
		}
	}
	// Secrets first, then the entry, so the two never disagree: when
	// either fails, the keychain gets back what it had.
	if f.isNew {
		cr.ID = assets.NewID()
	}
	tx := &secretTx{kc: a.kc}
	fail := func(msg string) bool {
		if rerr := tx.rollback(); rerr != nil {
			msg = m.ErrKeychainRestore(msg, secretStoreErrorText(m, rerr))
		}
		f.err = msg
		return false
	}
	for _, kind := range keychain.Kinds {
		key := keychain.Cred(cr.ID, kind)
		var err error
		if v, ok := set[kind]; ok {
			err = tx.set(key, v)
		} else if slices.Contains(drop, kind) {
			err = tx.delete(key)
		}
		if err != nil {
			return fail(m.ErrKeychainSave(secretStoreErrorText(m, err)))
		}
	}
	var saved assets.Credential
	var err error
	if f.isNew {
		saved, err = a.store.AddCredential(cr)
	} else {
		saved, err = a.store.SaveCredential(cr)
	}
	if err != nil {
		return fail(fieldError(m, err))
	}
	// What was typed for the old secret no longer applies.
	a.mu.Lock()
	for _, kind := range []keychain.Kind{keychain.Password, keychain.Passphrase} {
		key := keychain.Cred(saved.ID, kind)
		if _, ok := set[kind]; ok || f.loaded() || !cr.AskPassphrase {
			delete(a.typed, key)
		}
	}
	a.mu.Unlock()
	if f.back != nil {
		f.back.auth = credLabel(m, saved)
	}
	a.closeCredForm()
	return true
}

func credLabel(m locales.Messages, cr assets.Credential) string {
	kind := m.CredKindPassword()
	if cr.Kind == assets.CredKey {
		kind = m.CredKindKey()
	}
	return cr.Name + " · " + kind
}

// hostNames are the labels of hosts, joined.
func hostNames(hs []assets.Host) string {
	var names []string
	for _, h := range hs {
		names = append(names, h.Label())
	}
	return strings.Join(names, ", ")
}

// keychainDeleteError is a delete that stopped because the keychain
// would not remove a secret; the entry is kept, so a retry can finish.
type keychainDeleteError struct{ err error }

func (e *keychainDeleteError) Error() string { return e.err.Error() }
func (e *keychainDeleteError) Unwrap() error { return e.err }

// DeleteCredential removes a credential and its secrets. The hosts that
// use it switch to rebind; with rebind nil a credential in use is kept
// (an *assets.InUseError). The secrets go first: when the keychain fails,
// the credential and its hosts stay as they were (a *keychainDeleteError),
// and only then are the hosts rebound and the entry removed.
func (a *App) DeleteCredential(id string, rebind *authChoice) error {
	if rebind != nil && rebind.cred == id {
		return errors.New("app: a credential cannot replace itself")
	}
	if _, ok := a.store.Credential(id); !ok {
		return assets.ErrNotFound
	}
	users := a.store.CredentialUsers(id)
	if rebind == nil && len(users) > 0 {
		return a.store.DeleteCredential(id) // the in-use error
	}
	if rebind != nil && rebind.cred != "" {
		if _, ok := a.store.Credential(rebind.cred); !ok {
			return assets.ErrNotFound
		}
	}
	if err := a.kc.DeleteCred(id); err != nil {
		return &keychainDeleteError{err}
	}
	for _, h := range users {
		h.Auth, h.CredentialID = rebind.method, rebind.cred
		if _, err := a.store.Update(h); err != nil {
			return err
		}
	}
	if err := a.store.DeleteCredential(id); err != nil {
		return err
	}
	a.mu.Lock()
	for _, kind := range []keychain.Kind{keychain.Password, keychain.Passphrase} {
		delete(a.typed, keychain.Cred(id, kind))
	}
	a.mu.Unlock()
	return nil
}

// DeleteProxy removes a proxy no host uses, and its password; the
// password goes first.
func (a *App) DeleteProxy(id string) error {
	if _, ok := a.store.Proxy(id); !ok {
		return assets.ErrNotFound
	}
	if us := a.store.ProxyUsers(id); len(us) > 0 {
		return a.store.DeleteProxy(id) // the in-use error
	}
	if err := a.kc.Delete(keychain.ProxyPassword(id)); err != nil {
		return &keychainDeleteError{err}
	}
	return a.store.DeleteProxy(id)
}

// pickKeyFile reads a key through the system's open dialog, off the UI
// thread.
func (a *App) pickKeyFile(m locales.Messages, f *credForm) {
	if a.p.PickFile == nil {
		return
	}
	title := m.KeyChooseFile()
	go func() {
		path, err := a.p.PickFile(title)
		if path == "" && err == nil {
			return
		}
		var data []byte
		if err == nil {
			data, err = os.ReadFile(path)
		}
		a.post(func() { a.loadKeyFile(m, f, path, data, err) })
	}()
}

func (a *App) loadKeyFile(m locales.Messages, f *credForm, path string, data []byte, err error) {
	if a.credForm != f && (a.form == nil || &a.form.secret != f) {
		return
	}
	if err != nil {
		f.err = m.ErrReadFile(filepath.Base(path), err.Error())
		return
	}
	f.setKey(data, filepath.Base(path))
}

// maxKeyFile bounds what a drop reads: no key is near a megabyte.
const maxKeyFile = 1 << 20

func readKeyFile(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > maxKeyFile {
		return nil, keys.ErrNotAKey
	}
	return os.ReadFile(path)
}

func (a *App) credView(c *ui.Context, m locales.Messages) {
	if a.credForm != nil {
		f := a.credForm
		tok := currentTokens(c)
		sheet(ui.Modal(c, &a.credEditing, func() {
			ui.Column(c).Gap(18).Width(500).Children(func() {
				title := m.EditCredential()
				if f.isNew {
					title = m.NewCredential()
				}
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					ic, tint := icons.Password, theme.Brand
					if f.isKey() {
						ic = icons.Key
					}
					if !f.isNew {
						tint = tok.Tint(f.orig.ID)
					}
					tile(c, ic, tint, 36)
					ui.Column(c).Gap(1).Shrink(1).Children(func() {
						sheetTitle(c, title)
						if f.name != "" {
							ui.Text(c, f.name).FontSize(theme.Footnote).TextColor(tok.TextMuted).SingleLine()
						}
					})
				})
				ui.Form(c, func() {
					ui.Field(c, m.FieldName(), func() {
						in := ui.TextInput(c.Key("cf:name"), &f.name).Placeholder("ops").AutoFocus()
						a.credHold.apply(c, f, in)
					})
					ui.Field(c, m.FieldType(), func() {
						if ui.Segmented(c.Key("cf:kind"), &f.kind, m.CredKindPassword(), m.CredKindPrivateKey()).Label(m.FieldType()).Changed() {
							f.err = ""
						}
					})
					ui.Field(c, m.FieldUser(), func() {
						ui.TextInput(c.Key("cf:user"), &f.user).Placeholder(m.CredUserPlaceholder())
					})
					if f.isKey() {
						ui.Field(c, m.FieldPrivateKey(), func() { a.keyPicker(c, m, tok, f) })
						if f.encrypted() || f.genKey != nil {
							ui.Field(c, m.FieldPassphrase(), func() {
								ph := ""
								if !f.isNew && !f.loaded() && f.orig.Encrypted {
									ph = m.PasswordUnchanged()
								}
								ui.TextInput(c.Key("cf:pass"), &f.passphrase).Password().Placeholder(ph)
							})
							ui.Field(c, "", func() {
								ui.Checkbox(c.Key("cf:ask"), &f.askPass, m.AskPassphraseEachTime())
							})
						}
					} else {
						ui.Field(c, m.FieldPassword(), func() {
							ph := ""
							if !f.isNew && f.orig.Kind == assets.CredPassword {
								ph = m.PasswordUnchanged()
							}
							ui.TextInput(c.Key("cf:pw"), &f.password).Password().Placeholder(ph)
						})
					}
					ui.Field(c, m.FieldNote(), func() { ui.TextInput(c.Key("cf:note"), &f.note) })
				})
				if !f.isNew {
					used := m.CredUnused()
					if hs := a.store.CredentialUsers(f.orig.ID); len(hs) > 0 {
						used = m.CredUsedBy(hostNames(hs))
					}
					ui.Text(c, used).FontSize(theme.Footnote).TextColor(tok.TextMuted)
				}
				if f.err != "" {
					ui.Text(c, f.err).FontSize(theme.Footnote).TextColor(tok.Danger)
				}
				ui.Box(c).Height(1).FillWidth().Background(tok.Line)
				ui.Row(c).Gap(8).Children(func() {
					if !f.isNew && pill(c, m.Delete(), false, true, false) {
						a.askDeleteCred(m, f.orig.ID)
					}
					ui.Spacer(c)
					if pill(c, m.Cancel(), false, false, false) {
						a.credEditing = false
					}
					if pill(c, m.Save(), true, false, false) {
						a.SaveCredForm(m)
					}
				})
			})
		}), tok)
		if !a.credEditing && a.credForm == f {
			a.closeCredForm()
		}
	}
	a.credDeleteView(c, m)
}

// keyPicker is the key field: a drop zone with what was loaded, and
// buttons to choose a file, paste or generate.
func (a *App) keyPicker(c *ui.Context, m locales.Messages, tok theme.Tokens, f *credForm) {
	ui.Column(c).Gap(8).FillWidth().Children(func() {
		zone := ui.Column(c.Key("cf:drop")).Gap(4).Padding(10, 12).FillWidth().Radius(theme.RadiusControl).
			Border(1, tok.Line).Background(tok.Fill)
		if zone.FileDragOver() {
			zone.Border(1, tok.Accent)
		}
		if paths := zone.DroppedFiles(); len(paths) > 0 {
			data, err := readKeyFile(paths[0])
			a.loadKeyFile(m, f, paths[0], data, err)
		}
		zone.Children(func() {
			switch {
			case f.previewErr != nil:
				ui.Text(c, keyError(m, f.previewErr)).FontSize(theme.Footnote).TextColor(tok.Danger)
			case f.loaded() && f.preview.Public != nil:
				ui.Text(c, m.KeyInfo(f.preview.Type(), f.preview.Fingerprint())).FontSize(theme.Footnote).SingleLine()
				a.keyNotes(c, m, tok, f)
			case f.loaded():
				// A legacy encrypted PEM: its public half is sealed too.
				a.keyNotes(c, m, tok, f)
			case !f.isNew && f.orig.Kind == assets.CredKey:
				ui.Text(c, m.KeyInfo(f.orig.KeyType, f.orig.Fingerprint)).FontSize(theme.Footnote).SingleLine()
				if f.orig.Encrypted {
					ui.Text(c, m.KeyEncrypted()).FontSize(theme.Caption).TextColor(tok.TextMuted)
				}
			default:
				ui.Text(c, m.KeyDropHint()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			}
		})
		ui.Row(c).Gap(4).Children(func() {
			if iconButton(c, "cf:file", icons.OpenFile, m.KeyChooseFile()) {
				a.pickKeyFile(m, f)
			}
			if iconButton(c, "cf:paste", icons.Paste, m.Paste()) {
				if s := c.Services().ReadClipboard(); strings.TrimSpace(s) != "" {
					f.setKey([]byte(s), "")
				}
			}
			if iconButton(c, "cf:gen", icons.Generate, m.KeyGenerate()) {
				f.generate()
			}
		})
	})
}

func (a *App) keyNotes(c *ui.Context, m locales.Messages, tok theme.Tokens, f *credForm) {
	note := func(s string) { ui.Text(c, s).FontSize(theme.Caption).TextColor(tok.TextMuted) }
	switch {
	case f.genKey != nil:
		note(m.KeyGenerated())
	case f.keyFrom != "":
		note(m.KeyFromFile(f.keyFrom))
	}
	if f.keyData != nil && keys.IsPPK(f.keyData) {
		note(m.KeyPuttyConverted())
	}
	if f.keyData != nil && f.preview.Encrypted {
		note(m.KeyEncrypted())
	}
}

// askDeleteCred opens the delete dialog of a credential.
func (a *App) askDeleteCred(m locales.Messages, id string) {
	a.credDelete, a.credDeleting = id, true
	a.rebindChoice = m.AuthAsk()
	a.credEditing = false
	if f := a.credForm; f != nil {
		f.back = nil // the host editor behind it may name it
	}
}

// rebindChoices are what the hosts of a deleted credential may switch to.
func (a *App) rebindChoices(m locales.Messages, id string) []authChoice {
	var out []authChoice
	for _, ch := range a.authChoices(m) {
		if ch.cred != id {
			out = append(out, ch)
		}
	}
	return out
}

func (a *App) credDeleteView(c *ui.Context, m locales.Messages) {
	if !a.credDeleting {
		return
	}
	id := a.credDelete
	cr, ok := a.store.Credential(id)
	if !ok {
		a.credDeleting = false
		return
	}
	users := a.store.CredentialUsers(id)
	if len(users) == 0 {
		if ui.AlertDialog(c, &a.credDeleting, m.DeleteCredential(), m.DeleteCredentialConfirm(cr.Name), m.Cancel(), m.Delete()) == 1 {
			if err := a.DeleteCredential(id, nil); err != nil {
				c.AddToast(ui.Toast{Title: a.inUseText(m, err), Type: "error"})
			}
		}
		return
	}
	tok := currentTokens(c)
	sheet(ui.Modal(c, &a.credDeleting, func() {
		ui.Column(c).Gap(16).Width(440).Children(func() {
			sheetTitle(c, m.DeleteCredential())
			ui.Text(c, m.DeleteCredentialInUse(cr.Name, hostNames(users))).FontSize(theme.Body)
			choices := a.rebindChoices(m, id)
			var labels []string
			for _, ch := range choices {
				labels = append(labels, ch.label)
			}
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, m.RebindTo()).FontSize(theme.Body)
				ui.Select(c.Key("cd:rebind"), &a.rebindChoice, labels).Label(m.RebindTo()).Width(240)
			})
			ui.Box(c).Height(1).FillWidth().Background(tok.Line)
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					a.credDeleting = false
				}
				if pill(c, m.RebindAndDelete(), false, true, false) {
					for _, ch := range choices {
						if ch.label == a.rebindChoice {
							if err := a.DeleteCredential(id, &ch); err != nil {
								c.AddToast(ui.Toast{Title: a.inUseText(m, err), Type: "error"})
							}
							break
						}
					}
					a.credDeleting = false
				}
			})
		})
	}), tok)
}

// credentialsSection lists the credentials in Settings.
func (a *App) credentialsSection(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	settingsSection(c, tok, nil, ui.Color{}, "", func(g *settingsGroup) {
		crs := a.store.Credentials()
		if len(crs) == 0 {
			g.row(m.NoCredentials(), "", func() {})
		}
		for _, cr := range crs {
			cr := cr
			note := m.CredKindPassword()
			if cr.Kind == assets.CredKey {
				note = m.KeyInfo(cr.KeyType, cr.Fingerprint)
			}
			if hs := a.store.CredentialUsers(cr.ID); len(hs) > 0 {
				note += " — " + m.CredUsedBy(hostNames(hs))
			}
			g.row(cr.Name, note, func() {
				if iconButton(c, "cred:edit:"+cr.ID, icons.Edit, m.Edit()) {
					a.openCredForm(&cr, nil)
				}
				if iconButton(c, "cred:del:"+cr.ID, icons.Delete, m.Delete()) {
					a.askDeleteCred(m, cr.ID)
				}
			})
		}
		g.row("", "", func() {
			if pill(c, m.NewCredential(), false, false, false) {
				a.openCredForm(nil, nil)
			}
		})
	})
}
