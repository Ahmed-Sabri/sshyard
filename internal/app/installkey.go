package app

import (
	"context"
	"errors"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/keys"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sftpsvc"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// installForm is the sheet that installs a key credential's public key
// on a host.
type installForm struct {
	hostID string
	// cred is the label of the key chosen
	cred string
	// password is the host's password, for a host that asks each time;
	// passphrase the key's, when it is not saved
	password, passphrase         string
	needPassword, needPassphrase bool
	busy                         bool
	res                          *installResult
	// unknown is the host's key, seen for the first time: the install
	// goes on once it is trusted
	unknown *session.UnknownHostError
}

// installResult is how an install went.
type installResult struct {
	credID string
	// added is false when the key was there already
	added bool
	// user is the account the key went to; switchUser the one the host
	// would log in as after switching to the key credential (its own user
	// name, else the host's). Switching is offered only when they match.
	user, switchUser string
	// keyUser is set when the key credential's own user name differs,
	// hostUser when the host's user name does (the key credential has none)
	keyUser, hostUser string
	// err stopped the install; verifyErr is the login with the key
	// alone failing after it
	err, verifyErr error
}

func (r *installResult) ok() bool { return r != nil && r.err == nil && r.verifyErr == nil }

func (a *App) openInstall(hostID string) {
	a.install, a.installing = &installForm{hostID: hostID}, true
}

// keyCredentials are the credentials that are private keys.
func (a *App) keyCredentials(m locales.Messages) []authChoice {
	var out []authChoice
	for _, cr := range a.store.Credentials() {
		if cr.Kind == assets.CredKey {
			out = append(out, authChoice{credLabel(m, cr), assets.AuthCredential, cr.ID})
		}
	}
	return out
}

// InstallKey logs in to h as it does now (its shared connection, or a
// new one with password for a host that asks each time), appends the
// public key of the key credential credID to ~/.ssh/authorized_keys over
// SFTP, then logs in anew with the key alone to prove it works.
func (a *App) InstallKey(ctx context.Context, h assets.Host, credID, password, passphrase string) installResult {
	res := installResult{credID: credID}
	cr, ok := a.store.Credential(credID)
	if !ok || cr.Kind != assets.CredKey {
		res.err = assets.ErrNotFound
		return res
	}
	pem, err := a.kc.Get(keychain.Cred(cr.ID, keychain.PrivateKey))
	if err != nil {
		res.err = session.ErrNoCredentials
		return res
	}
	pass := []byte(passphrase)
	pk := keychain.Cred(cr.ID, keychain.Passphrase)
	if len(pass) == 0 {
		a.mu.Lock()
		pass = []byte(a.typed[pk])
		a.mu.Unlock()
	}
	if len(pass) == 0 && !cr.AskPassphrase {
		pass, _ = a.kc.Get(pk)
	}
	k, err := keys.Import(pem, pass)
	if err != nil {
		res.err = err
		return res
	}
	// The login as it is now, and the account it reaches: the key is
	// installed for that one, and checked for that one.
	res.user = a.loginUser(h)
	res.switchUser = cr.User
	if res.switchUser == "" {
		res.switchUser = h.User
	}
	switch {
	case res.switchUser == res.user:
	case cr.User != "":
		res.keyUser = cr.User
	default:
		res.hostUser = h.User
	}
	cur := &conn{host: h}
	cfg := a.hostConfig(h, cur, func(ctx context.Context) (session.Credentials, error) {
		if h.Auth == assets.AuthAsk && password != "" {
			return session.Credentials{Password: password}, nil
		}
		return a.credentials(cur, h)
	})
	client, release, err := a.pool.Acquire(ctx, a.poolKey(h), cfg)
	if err != nil {
		res.err = err
		return res
	}
	defer release()
	if h.Auth == assets.AuthAsk && password != "" {
		a.mu.Lock()
		a.typed["ask/"+h.ID] = password
		a.mu.Unlock()
	}
	fs, err := sftpsvc.New(client)
	if err != nil {
		res.err = err
		return res
	}
	defer fs.Close()
	if res.added, err = fs.InstallKey(k.Public, k.AuthorizedLine()); err != nil {
		res.err = err
		return res
	}
	if passphrase != "" && cr.AskPassphrase {
		a.mu.Lock()
		a.typed[pk] = passphrase
		a.mu.Unlock()
	}
	// A new connection, with the key alone.
	vcfg := a.hostConfig(h, cur, func(context.Context) (session.Credentials, error) {
		return session.Credentials{PrivateKey: pem, Passphrase: pass, User: res.user}, nil
	})
	vcfg.Pool, vcfg.Key = nil, ""
	vc, err := session.Dial(ctx, vcfg)
	if err != nil {
		res.verifyErr = err
		return res
	}
	vc.Close()
	return res
}

// canSwitch reports whether switching the host to the key would log in
// as the account the key was installed for.
func (r *installResult) canSwitch() bool { return r.switchUser == r.user }

// loginUser is the user h logs in as, resolved as a connection does: its
// credential's own user name, else the host's.
func (a *App) loginUser(h assets.Host) string {
	if h.Auth == assets.AuthCredential {
		if cr, ok := a.store.Credential(h.CredentialID); ok && cr.User != "" {
			return cr.User
		}
	}
	return h.User
}

// errSwitchUser refuses a switch that would log in as another account
// than the one the key was installed for.
var errSwitchUser = errors.New("the host would log in as another user with this key")

// SwitchToKey makes h log in with the credential credID, the key
// installed for user; it refuses when that would log in as anyone else.
func (a *App) SwitchToKey(hostID, credID, user string) error {
	h, ok := a.store.Get(hostID)
	if !ok {
		return assets.ErrNotFound
	}
	h.Auth, h.CredentialID = assets.AuthCredential, credID
	if a.loginUser(h) != user {
		return errSwitchUser
	}
	_, err := a.store.Update(h)
	return err
}

func (a *App) startInstall(m locales.Messages, f *installForm, h assets.Host) {
	var credID string
	for _, ch := range a.keyCredentials(m) {
		if ch.label == f.cred {
			credID = ch.cred
		}
	}
	if credID == "" {
		return
	}
	f.busy, f.res, f.unknown = true, nil, nil
	pw, pp := f.password, f.passphrase
	go func() {
		res := a.InstallKey(context.Background(), h, credID, pw, pp)
		a.post(func() {
			f.busy = false
			var u *session.UnknownHostError
			switch {
			case errors.As(res.err, &u):
				f.unknown = u
			case errors.Is(res.err, keys.ErrPassphraseNeeded):
				f.needPassphrase = true
			case errors.Is(res.err, session.ErrNoCredentials) && h.Auth == assets.AuthAsk:
				f.needPassword = true
			case h.Auth == assets.AuthAsk && session.Classify(res.err) == session.KindAuth:
				f.needPassword, f.password = true, ""
				f.res = &res
			default:
				f.res = &res
			}
		})
	}()
}

func (a *App) installView(c *ui.Context, m locales.Messages) {
	f := a.install
	if f == nil {
		return
	}
	h, ok := a.store.Get(f.hostID)
	if !ok {
		a.install, a.installing = nil, false
		return
	}
	tok := currentTokens(c)
	choices := a.keyCredentials(m)
	if f.cred == "" && len(choices) > 0 {
		f.cred = choices[0].label
		for _, ch := range choices {
			if h.Auth == assets.AuthCredential && ch.cred == h.CredentialID {
				f.cred = ch.label
			}
		}
	}
	sheet(ui.Modal(c, &a.installing, func() {
		ui.Column(c).Gap(16).Width(480).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				tile(c, icons.InstallKey, tok.Tint(h.ID), 36)
				ui.Column(c).Gap(1).Shrink(1).Children(func() {
					sheetTitle(c, m.InstallKeyTitle())
					ui.Text(c, h.Label()).FontSize(theme.Footnote).TextColor(tok.TextMuted).SingleLine()
				})
			})
			ui.Text(c, m.InstallKeyNote(h.Label())).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			if len(choices) == 0 {
				ui.Text(c, m.InstallNoKeys()).FontSize(theme.Body)
			} else {
				ui.Form(c, func() {
					ui.Field(c, m.AuthKey(), func() {
						var labels []string
						for _, ch := range choices {
							labels = append(labels, ch.label)
						}
						if ui.Select(c.Key("ik:cred"), &f.cred, labels).Label(m.AuthKey()).Changed() {
							f.res, f.needPassphrase, f.passphrase = nil, false, ""
						}
					})
					if f.needPassword {
						ui.Field(c, m.FieldPassword(), func() {
							in := ui.TextInput(c.Key("ik:pw"), &f.password).Password().Placeholder(m.InstallLoginPassword(h.User + "@" + h.Address))
							a.installHold.apply(c, "pw", in)
							if in.Submitted() && !f.busy {
								a.startInstall(m, f, h)
							}
						})
					}
					if f.needPassphrase {
						ui.Field(c, m.FieldPassphrase(), func() {
							in := ui.TextInput(c.Key("ik:pp"), &f.passphrase).Password()
							a.installHold.apply(c, "pp", in)
							if in.Submitted() && !f.busy {
								a.startInstall(m, f, h)
							}
						})
					}
				})
			}
			if u := f.unknown; u != nil {
				ui.Text(c, m.HostKeyUnknown(h.Label(), u.Key.Type(), u.Fingerprint)).FontSize(theme.Footnote)
			}
			a.installResultView(c, m, tok, f, h)
			ui.Box(c).Height(1).FillWidth().Background(tok.Line)
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				r := f.res
				if r.ok() && r.canSwitch() && !(h.Auth == assets.AuthCredential && h.CredentialID == r.credID) {
					if pill(c, m.KeepCurrent(), false, false, false) {
						a.installing = false
					}
					if pill(c, m.SwitchAuth(), true, false, false) {
						if err := a.SwitchToKey(h.ID, r.credID, r.user); err != nil {
							c.AddToast(ui.Toast{Title: m.ErrGeneric(err.Error()), Type: "error"})
						} else if cr, ok := a.store.Credential(r.credID); ok {
							c.AddToast(ui.Toast{Title: m.HostSwitched(h.Label(), cr.Name)})
						}
						a.installing = false
					}
					return
				}
				if r != nil && (r.ok() || r.verifyErr != nil) {
					if pill(c, m.Close(), false, false, false) {
						a.installing = false
					}
					return
				}
				if pill(c, m.Cancel(), false, false, false) {
					a.installing = false
				}
				if u := f.unknown; u != nil {
					if pill(c, m.TrustAndConnect(), true, false, false) {
						if err := a.known.Add(u.Host, u.Key); err != nil {
							c.AddToast(ui.Toast{Title: m.ErrGeneric(err.Error()), Type: "error"})
							return
						}
						a.startInstall(m, f, h)
					}
					return
				}
				label := m.Install()
				if f.busy {
					label = m.Installing()
				}
				if pill(c, label, true, false, f.busy || len(choices) == 0) {
					a.startInstall(m, f, h)
				}
			})
		})
	}), tok)
	if !a.installing {
		a.install = nil
	}
}

func (a *App) installResultView(c *ui.Context, m locales.Messages, tok theme.Tokens, f *installForm, h assets.Host) {
	r := f.res
	if r == nil {
		return
	}
	text := func(s string, col ui.Color) { ui.Text(c, s).FontSize(theme.Footnote).TextColor(col) }
	if r.err != nil {
		detail := a.errText(m, h, r.err)
		var ke error
		for _, e := range []error{keys.ErrPassphraseWrong, keys.ErrCorrupt} {
			if errors.Is(r.err, e) {
				ke = e
			}
		}
		if ke != nil {
			detail = keyError(m, ke)
		}
		text(m.InstallFailed(detail), tok.Danger)
		return
	}
	if r.added {
		text(m.InstallAdded(), tok.Text)
	} else {
		text(m.InstallAlready(), tok.Text)
	}
	if r.verifyErr != nil {
		text(m.InstallVerifyFailed(a.errText(m, h, r.verifyErr)), tok.Danger)
		text(m.InstallHints(), tok.TextMuted)
		return
	}
	text(m.InstallVerified(), tok.Text)
	if r.keyUser != "" {
		text(m.InstallOtherUser(r.user, r.keyUser), tok.TextMuted)
		return
	}
	if !r.canSwitch() {
		text(m.InstallOtherHostUser(r.user, r.switchUser), tok.TextMuted)
		return
	}
	if !(h.Auth == assets.AuthCredential && h.CredentialID == r.credID) {
		name := r.credID
		if cr, ok := a.store.Credential(r.credID); ok {
			name = cr.Name
		}
		ui.Text(c, m.SwitchToCredential(h.Label(), name)).FontSize(theme.Body).FontWeight(500)
	}
}
