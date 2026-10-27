package app

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/vault"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// A backup is everything SSHYard keeps (the hosts and what they use, the
// preferences, the trusted host keys and, unless left out, their secrets
// from the encrypted local store) in one file sealed with a password (package vault),
// to keep or to move to another device. Restoring merges it in.

// backupPayload is what a backup file holds once opened.
type backupPayload struct {
	Assets     assets.Snapshot   `json:"assets"`
	Settings   settings.Settings `json:"settings"`
	KnownHosts string            `json:"knownHosts,omitempty"`
	// Secrets are the entries above, by key; never the master key or MCP token.
	Secrets map[string][]byte `json:"secrets,omitempty"`
}

// backupExt is the extension of a backup file.
const backupExt = ".sshyard-backup"

// minBackupPassword is the shortest backup password taken, in characters.
const minBackupPassword = 8

// maxBackupFile bounds what a restore reads.
const maxBackupFile = 64 << 20

// backupForm is the backup sheet: exporting, with a new password typed
// twice, or restoring the file data with its password. The password lives
// here only while the sheet is open.
type backupForm struct {
	restore           bool
	password, confirm string
	// secrets puts the local store's secrets in an export; on by default
	secrets bool
	// name, data and created are the backup file being restored
	name    string
	data    []byte
	created time.Time
	// busy is set while the password is derived and the file sealed or
	// opened, off the UI thread
	busy bool
	err  string
}

// backupSecretKeys are the allowed secret keys of the entries of snap and of
// the AI providers.
func backupSecretKeys(snap assets.Snapshot, provs []settings.AIProvider) []string {
	var out []string
	for _, c := range snap.Credentials {
		for _, k := range keychain.Kinds {
			out = append(out, keychain.Cred(c.ID, k))
		}
	}
	for _, p := range snap.Proxies {
		out = append(out, keychain.ProxyPassword(p.ID))
	}
	for _, f := range snap.Forwards {
		out = append(out, keychain.ForwardPassword(f.ID))
	}
	for _, p := range provs {
		out = append(out, keychain.AIKey(p.ID))
	}
	return out
}

// collectBackup gathers what a backup holds. With secrets it reads them
// from the encrypted local store, and fails when it cannot be read: a
// backup missing secrets it seems to have is worse than none.
func (a *App) collectBackup(secrets bool) (backupPayload, error) {
	p := backupPayload{Assets: a.store.Snapshot(), Settings: a.settings}
	if b, err := a.known.Contents(); err == nil {
		p.KnownHosts = string(b)
	}
	if !secrets {
		return p, nil
	}
	p.Secrets = map[string][]byte{}
	for _, key := range backupSecretKeys(p.Assets, p.Settings.AIProviders) {
		v, err := a.kc.Get(key)
		switch {
		case errors.Is(err, keychain.ErrNotFound):
		case err != nil:
			return backupPayload{}, err
		default:
			p.Secrets[key] = v
		}
	}
	return p, nil
}

// errBadBackup reports a backup whose entries can not be taken.
var errBadBackup = errors.New("app: invalid backup entries")

// restoreBackup merges p into this device: one batch of secrets is encrypted
// under this device's master key, then entries are saved, with compensating
// rollback on failure (not crash atomicity across files); host keys and preferences
// follow, whose
// failures are told but undo nothing. Only the secrets of the backup's
// own entries are taken.
func (a *App) restoreBackup(m locales.Messages, p backupPayload) error {
	snap := p.Assets
	for _, id := range backupIDs(snap, p.Settings.AIProviders) {
		if !assets.ValidID(id) {
			return errBadBackup
		}
	}
	tx := &secretTx{kc: a.kc}
	var changes []keychain.Change
	for _, key := range backupSecretKeys(snap, p.Settings.AIProviders) {
		if v := p.Secrets[key]; len(v) > 0 {
			changes = append(changes, keychain.Change{Key: key, Value: v})
		}
	}
	if err := tx.setBatch(changes); err != nil {
		msg := m.ErrKeychainSave(secretStoreErrorText(m, err))
		if rerr := tx.rollback(); rerr != nil {
			msg = m.ErrKeychainRestore(msg, secretStoreErrorText(m, rerr))
		}
		return errors.New(msg)
	}
	if err := a.store.Restore(snap); err != nil {
		var fe *assets.FieldError
		if errors.As(err, &fe) || errors.Is(err, assets.ErrJumpLoop) {
			err = errBadBackup
		}
		if rerr := tx.rollback(); rerr != nil {
			return errors.New(m.ErrKeychainRestore(backupErrText(m, err), secretStoreErrorText(m, rerr)))
		}
		return err
	}
	if p.KnownHosts != "" {
		if _, err := a.known.Merge([]byte(p.KnownHosts)); err != nil {
			a.toastError(m.ErrGeneric(err.Error()))
		}
	}
	if err := a.ApplySettings(mergeSettings(a.settings, p.Settings)); err != nil {
		a.toastError(m.ErrGeneric(err.Error()))
	}
	return nil
}

// backupIDs are the IDs the backup's secrets are stored under.
func backupIDs(snap assets.Snapshot, provs []settings.AIProvider) []string {
	var out []string
	for _, c := range snap.Credentials {
		out = append(out, c.ID)
	}
	for _, p := range snap.Proxies {
		out = append(out, p.ID)
	}
	for _, f := range snap.Forwards {
		out = append(out, f.ID)
	}
	for _, p := range provs {
		out = append(out, p.ID)
	}
	return out
}

// mergeSettings are the preferences of a backup, in, over those of this
// device, cur: what belongs to this device stays (the window's layout and
// materials, the fonts installed here, the MCP endpoint), AI providers
// are merged by ID and the hosts that confirmed AI use are joined.
func mergeSettings(cur, in settings.Settings) settings.Settings {
	out := in
	out.SidebarWidth, out.ChromeMaterial, out.ContentMaterial = cur.SidebarWidth, cur.ChromeMaterial, cur.ContentMaterial
	out.UIFont, out.TerminalFont = cur.UIFont, cur.TerminalFont
	// A backup never turns the MCP endpoint on: that is asked here.
	out.MCPEnabled, out.MCPPort = cur.MCPEnabled, cur.MCPPort
	out.AIProviders = slices.Clone(cur.AIProviders)
	for _, p := range in.AIProviders {
		if i := slices.IndexFunc(out.AIProviders, func(o settings.AIProvider) bool { return o.ID == p.ID }); i >= 0 {
			out.AIProviders[i] = p
		} else {
			out.AIProviders = append(out.AIProviders, p)
		}
	}
	out.AIConsentHosts = slices.Clone(cur.AIConsentHosts)
	for _, id := range in.AIConsentHosts {
		if !slices.Contains(out.AIConsentHosts, id) {
			out.AIConsentHosts = append(out.AIConsentHosts, id)
		}
	}
	return out
}

// backupErrText says why a backup could not be opened or restored.
func backupErrText(m locales.Messages, err error) string {
	switch {
	case errors.Is(err, vault.ErrFormat):
		return m.BackupNotBackup()
	case errors.Is(err, vault.ErrVersion):
		return m.BackupNewer()
	case errors.Is(err, vault.ErrPassword):
		return m.BackupWrongPassword()
	case errors.Is(err, vault.ErrCorrupt), errors.Is(err, errBadBackup):
		return m.BackupDamaged()
	}
	return m.ErrGeneric(secretStoreErrorText(m, err))
}

// toastError shows msg as an error toast at the next frame.
func (a *App) toastError(msg string) {
	a.toasts = append(a.toasts, func(locales.Messages) ui.Toast { return ui.Toast{Title: msg, Type: "error"} })
}

func (a *App) openExport() {
	a.backup, a.backingUp = &backupForm{secrets: true}, true
}

func (a *App) closeBackup() {
	a.backup, a.backingUp = nil, false
}

// ExportBackup checks the sheet's password and gathers the backup, then,
// off the UI thread, asks where to save it and seals it there.
func (a *App) ExportBackup(m locales.Messages) bool {
	f := a.backup
	if f == nil || f.restore || f.busy || a.p.SaveFile == nil {
		return false
	}
	f.err = ""
	switch {
	case len([]rune(f.password)) < minBackupPassword:
		f.err = m.BackupPasswordShort(strconv.Itoa(minBackupPassword))
		return false
	case f.password != f.confirm:
		f.err = m.BackupPasswordMismatch()
		return false
	}
	p, err := a.collectBackup(f.secrets)
	var data []byte
	if err == nil {
		data, err = json.Marshal(p)
	}
	if err != nil {
		f.err = m.BackupExportFailed(secretStoreErrorText(m, err))
		return false
	}
	pw, cost, title := []byte(f.password), a.kdf, m.ExportBackup()
	def := filepath.Join(a.homeDir(), "SSHYard-"+time.Now().Format("2006-01-02")+backupExt)
	f.busy = true
	go func() {
		path, err := a.p.SaveFile(title, def)
		if path == "" && err == nil {
			a.post(func() { f.busy = false })
			return
		}
		if err == nil {
			var sealed []byte
			if sealed, err = vault.Seal(data, pw, cost); err == nil {
				err = settings.WriteFileAtomic(path, sealed, 0o600)
			}
		}
		a.post(func() { a.exportDone(m, f, path, err) })
	}()
	return true
}

func (a *App) exportDone(m locales.Messages, f *backupForm, path string, err error) {
	f.busy = false
	if err != nil {
		if a.backup == f {
			f.err = m.BackupExportFailed(err.Error())
		} else {
			a.toastError(m.BackupExportFailed(err.Error()))
		}
		return
	}
	if a.backup == f {
		a.closeBackup()
	}
	name := filepath.Base(path)
	a.toasts = append(a.toasts, func(m locales.Messages) ui.Toast { return ui.Toast{Title: m.BackupSaved(name), Type: "success"} })
}

// pickBackup reads a backup file through the system's open dialog, off
// the UI thread.
func (a *App) pickBackup(m locales.Messages) {
	if a.p.PickFile == nil {
		return
	}
	title := m.RestoreBackup()
	go func() {
		path, err := a.p.PickFile(title)
		if path == "" && err == nil {
			return
		}
		var data []byte
		if err == nil {
			data, err = readBackupFile(path)
		}
		a.post(func() { a.openRestore(m, path, data, err) })
	}()
}

func readBackupFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxBackupFile+1))
	if err == nil && len(b) > maxBackupFile {
		err = vault.ErrFormat
	}
	return b, err
}

// openRestore opens the restore sheet on the backup file read from path,
// or says why it can not be restored.
func (a *App) openRestore(m locales.Messages, path string, data []byte, err error) {
	if err == nil {
		err = vault.Check(data)
	}
	if err != nil {
		a.toastError(m.BackupOpenFailed(filepath.Base(path), backupErrText(m, err)))
		return
	}
	created, _ := vault.Created(data)
	a.backup, a.backingUp = &backupForm{restore: true, name: filepath.Base(path), data: data, created: created}, true
}

// RestoreBackup opens the sheet's backup with its password, off the UI
// thread, then merges it in.
func (a *App) RestoreBackup(m locales.Messages) bool {
	f := a.backup
	if f == nil || !f.restore || f.busy {
		return false
	}
	if f.password == "" {
		f.err = m.RequiredField(m.BackupPassword())
		return false
	}
	f.err, f.busy = "", true
	data, pw := f.data, []byte(f.password)
	go func() {
		p := backupPayload{Settings: settings.Default()}
		plain, err := vault.Open(data, pw)
		if err == nil && json.Unmarshal(plain, &p) != nil {
			err = vault.ErrCorrupt
		}
		a.post(func() { a.restoreDone(m, f, p, err) })
	}()
	return true
}

func (a *App) restoreDone(m locales.Messages, f *backupForm, p backupPayload, err error) {
	// A sheet closed while the backup opened restores nothing.
	if a.backup != f {
		return
	}
	f.busy = false
	if err == nil {
		err = a.restoreBackup(m, p)
	}
	if err != nil {
		f.err = backupErrText(m, err)
		return
	}
	a.closeBackup()
	hosts, creds := len(p.Assets.Hosts), len(p.Assets.Credentials)
	a.toasts = append(a.toasts, func(m locales.Messages) ui.Toast {
		return ui.Toast{Title: m.BackupRestored(hosts, creds), Type: "success"}
	})
}

// backupSection is the settings' backup page.
func (a *App) backupSection(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	settingsSection(c, tok, nil, ui.Color{}, "", func(g *settingsGroup) {
		g.row(m.ExportBackup(), m.ExportBackupNote(), func() {
			if pill(c, m.ExportEllipsis(), false, false, a.p.SaveFile == nil) {
				a.openExport()
			}
		})
		g.row(m.RestoreBackup(), m.RestoreBackupNote(), func() {
			if pill(c, m.KeyChooseFile(), false, false, a.p.PickFile == nil) {
				a.pickBackup(m)
			}
		})
	})
}

// backupView is the export or restore sheet.
func (a *App) backupView(c *ui.Context, m locales.Messages) {
	f := a.backup
	if f == nil {
		return
	}
	tok := currentTokens(c)
	sheet(ui.Modal(c, &a.backingUp, func() {
		ui.Column(c).Gap(18).Width(460).Children(func() {
			title, sub := m.ExportBackup(), ""
			if f.restore {
				title, sub = m.RestoreBackup(), f.name
			}
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				tile(c, icons.Backup, tok.TintAt(6), 36)
				ui.Column(c).Gap(1).Shrink(1).Children(func() {
					sheetTitle(c, title)
					if sub != "" {
						ui.Text(c, sub).FontSize(theme.Footnote).TextColor(tok.TextMuted).SingleLine()
					}
				})
			})
			ui.Form(c, func() {
				ui.Field(c, m.BackupPassword(), func() {
					in := ui.TextInput(c.Key("bk:pw"), &f.password).Password().AutoFocus().Disabled(f.busy)
					a.backupHold.apply(c, f, in)
					if in.Submitted() && f.restore {
						a.RestoreBackup(m)
					}
				})
				if !f.restore {
					ui.Field(c, m.ConfirmPassword(), func() {
						if ui.TextInput(c.Key("bk:confirm"), &f.confirm).Password().Disabled(f.busy).Submitted() {
							a.ExportBackup(m)
						}
					})
				}
			})
			if f.restore {
				if !f.created.IsZero() {
					ui.Text(c, m.BackupMadeOn(f.created.Local().Format("2006-01-02 15:04"))).FontSize(theme.Footnote).TextColor(tok.TextMuted)
				}
				ui.Text(c, m.BackupRestoreNote()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			} else {
				ui.Row(c).Gap(16).AlignItems(ui.Center).FillWidth().Children(func() {
					ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
						ui.Text(c, m.BackupIncludeSecrets()).FontSize(theme.Body).FontWeight(500)
						ui.Text(c, m.BackupIncludeSecretsNote()).FontSize(theme.Caption).TextColor(tok.TextMuted)
					})
					ui.Switch(c.Key("bk:secrets"), &f.secrets).Label(m.BackupIncludeSecrets()).Disabled(f.busy)
				})
				ui.Text(c, m.BackupExportWarning()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			}
			if f.err != "" {
				ui.Text(c, f.err).FontSize(theme.Footnote).TextColor(tok.Danger)
			}
			ui.Box(c).Height(1).FillWidth().Background(tok.Line)
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					a.backingUp = false
				}
				switch {
				case f.restore && f.busy:
					pill(c, m.BackupRestoring(), true, false, true)
				case f.restore:
					if pill(c, m.Restore(), true, false, false) {
						a.RestoreBackup(m)
					}
				case f.busy:
					pill(c, m.BackupExporting(), true, false, true)
				default:
					if pill(c, m.ExportEllipsis(), true, false, false) {
						a.ExportBackup(m)
					}
				}
			})
		})
	}), tok)
	if !a.backingUp && a.backup == f {
		a.closeBackup()
	}
}
