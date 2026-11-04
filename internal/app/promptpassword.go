package app

import (
	"errors"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/locales"
)

func (a *App) canSavePromptPassword(p *credPrompt) bool {
	if p.passphrase || isTemp(p.host.ID) {
		return false
	}
	_, exists := a.store.Get(p.host.ID)
	return exists
}

// savePromptPassword changes only the prompted host, never a shared credential.
// The old prompt key still serves the existing session's captured auth config.
func (a *App) savePromptPassword(m locales.Messages, p *credPrompt) error {
	if !a.canSavePromptPassword(p) {
		return assets.ErrNotFound
	}
	h, exists := a.store.Get(p.host.ID)
	if !exists || h.Addr() != p.host.Addr() || a.loginUser(h) != a.loginUser(p.host) {
		return assets.ErrNotFound
	}
	sameAuth := h.Auth == p.host.Auth && h.CredentialID == p.host.CredentialID
	// A previous answer may already have saved this host while its failed
	// session still captures the original ask/shared credential configuration.
	savedPassword := h.Auth == assets.AuthPassword && (p.host.Auth == assets.AuthAsk || p.host.Auth == assets.AuthCredential)
	if !sameAuth && !savedPassword {
		return assets.ErrNotFound
	}
	key, pass := a.promptKey(p.host, session.KindAuth)
	if key != p.key || pass {
		return assets.ErrNotFound
	}
	if p.value == "" {
		return errors.New(m.CredPasswordRequired())
	}
	h.User = a.loginUser(h)
	h.Auth, h.CredentialID, h.AskPassphrase = assets.AuthPassword, "", false
	changes := []keychain.Change{
		{Key: keychain.Host(h.ID, keychain.Password), Value: []byte(p.value)},
		{Key: keychain.Host(h.ID, keychain.PrivateKey), Delete: true},
		{Key: keychain.Host(h.ID, keychain.Passphrase), Delete: true},
	}
	_, err := a.saveHostSecrets(h, false, changes)
	return err
}
