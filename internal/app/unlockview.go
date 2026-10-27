package app

import (
	"errors"
	"os"
	"strconv"
	"syscall"

	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func (a *App) unlockView(c *ui.Context) {
	base := *c.Theme()
	base.Font = a.fonts.ui
	tok := theme.For(a.settings.Theme, base.Dark)
	c.SetTheme(tok.Apply(base))
	m := locales.In(c.Locale())
	ui.Column(c).Fill().Background(tok.Canvas).TextColor(tok.Text).Children(func() {
		ui.Row(c.Key("locked-title")).Height(bandHeight(c)).FillWidth().DragWindow().Children(func() {
			ui.Spacer(c)
			a.windowButtons(c, m, tok)
		})
		ui.Column(c).Grow(1).FillWidth().AlignItems(ui.Center).Justify(ui.Center).Gap(theme.Space(4)).Children(func() {
			sheetTitle(c, m.SecretsLockedTitle())
			ui.Text(c, m.SecretsLockedBody()).FontSize(theme.Body).TextColor(tok.TextMuted).MaxWidth(480)
			label := m.SecretsRetry()
			if a.unlock.busy {
				label = m.SecretsAuthorizing()
			}
			if !a.unlock.modal && pill(c, label, true, false, a.unlock.busy) {
				a.retryUnlock()
			}
		})
	})
	if !a.unlock.modal {
		return
	}
	sheet(ui.Modal(c.Key("unlock-error"), &a.unlock.modal, func() {
		ui.Column(c).Width(440).Gap(theme.Space(4)).Children(func() {
			sheetTitle(c, m.SecretsUnlockFailed())
			ui.Text(c, m.SecretsUnlockSafety()).FontSize(theme.Body)
			ui.Text(c, unlockErrorText(m, a.unlock.err)).FontSize(theme.Footnote).TextColor(tok.Danger)
			ui.Row(c).Gap(theme.Space(2)).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					a.unlock.modal = false
				}
				b, clicked := pillEl(c, m.SecretsRetry(), true, false, false)
				a.unlock.hold.apply(c, a.unlock.err, b)
				if clicked {
					a.retryUnlock()
				}
			})
		})
	}), tok)
}

// Only known reasons and operation names enter the interface, never raw errors.
func unlockErrorText(m locales.Messages, err error) string {
	reason := m.SecretsReasonSystem()
	switch {
	case errors.Is(err, os.ErrPermission):
		reason = m.SecretsReasonPermission()
	case errors.Is(err, os.ErrNotExist):
		reason = m.SecretsReasonNotFound()
	case errors.Is(err, syscall.ENOSPC):
		reason = m.SecretsReasonDiskFull()
	case errors.Is(err, keychain.ErrBusy):
		reason = m.SecretsReasonBusy()
	case errors.Is(err, keychain.ErrMasterMissing):
		reason = m.SecretsReasonMasterMissing()
	case errors.Is(err, keychain.ErrMasterInvalid):
		reason = m.SecretsReasonMasterInvalid()
	case errors.Is(err, keychain.ErrCorrupt):
		reason = m.SecretsReasonCorrupt()
	case errors.Is(err, keychain.ErrVersion):
		reason = m.SecretsReasonVersion()
	case errors.Is(err, keychain.ErrUnavailable):
		reason = m.SecretsReasonUnavailable()
	}
	var system *keychain.SystemError
	if errors.As(err, &system) {
		switch system.Code {
		case -128:
			reason = m.SecretsReasonCancelled()
		case -25293:
			reason = m.SecretsReasonAuthentication()
		case -25308:
			reason = m.SecretsReasonInteraction()
		case -25291:
			reason = m.SecretsReasonUnavailable()
		}
		reason = m.SecretsSystemCode(reason, strconv.Itoa(system.Code))
	}
	stage := m.SecretsStageOpen()
	var op *keychain.OpError
	if errors.As(err, &op) {
		switch op.Op {
		case "lock":
			stage = m.SecretsStageLock()
		case "master-read":
			stage = m.SecretsStageReadMaster()
		case "master-write":
			stage = m.SecretsStageWriteMaster()
		case "master-verify":
			stage = m.SecretsStageVerifyMaster()
		case "file-read":
			stage = m.SecretsStageReadFile()
		case "file-decrypt":
			stage = m.SecretsStageDecrypt()
		case "file-write":
			stage = m.SecretsStageWriteFile()
		}
	}
	return m.SecretsFailureDetail(stage, reason)
}
