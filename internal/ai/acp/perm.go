package acp

import sdk "github.com/coder/acp-go-sdk"

// PermissionChoice picks an option by kind, never by optionId.
// A run uses allow_once only. Anything else uses reject_once, then
// reject_always. allow_always is never chosen. With no usable option
// the outcome is cancelled.
func PermissionChoice(run bool, opts []sdk.PermissionOption) (optionID string, cancelled bool) {
	var allowOnce, rejectOnce, rejectAlways string
	for _, o := range opts {
		switch o.Kind {
		case sdk.PermissionOptionKindAllowOnce:
			if allowOnce == "" {
				allowOnce = string(o.OptionId)
			}
		case sdk.PermissionOptionKindRejectOnce:
			if rejectOnce == "" {
				rejectOnce = string(o.OptionId)
			}
		case sdk.PermissionOptionKindRejectAlways:
			if rejectAlways == "" {
				rejectAlways = string(o.OptionId)
			}
		}
	}
	if run && allowOnce != "" {
		return allowOnce, false
	}
	if rejectOnce != "" {
		return rejectOnce, false
	}
	if rejectAlways != "" {
		return rejectAlways, false
	}
	return "", true
}
