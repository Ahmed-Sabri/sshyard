package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/locales"
)

func TestSecretStoreErrorTextSanitizesTypedErrors(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hans"} {
		t.Run(lang, func(t *testing.T) {
			m := locales.In(lang)
			cases := []struct {
				err  error
				want string
			}{
				{&keychain.OpError{Op: "file-write", Err: &os.PathError{Op: "write", Path: "/private/secret-data/secrets.enc", Err: syscall.ENOSPC}}, m.SecretsFailureDetail(m.SecretsStageWriteFile(), m.SecretsReasonDiskFull())},
				{&keychain.OpError{Op: "file-decrypt", Err: fmt.Errorf("private-token: %w", keychain.ErrCorrupt)}, m.SecretsFailureDetail(m.SecretsStageDecrypt(), m.SecretsReasonCorrupt())},
				{&keychain.OpError{Op: "master-read", Err: &keychain.SystemError{Code: -25308, Err: errors.New("private-token /private/secret-data")}}, m.SecretsFailureDetail(m.SecretsStageReadMaster(), m.SecretsSystemCode(m.SecretsReasonInteraction(), "-25308"))},
			}
			for _, tc := range cases {
				got := secretStoreErrorText(m, tc.err)
				if got != tc.want || strings.Contains(got, "private-token") || strings.Contains(got, "/private/") {
					t.Fatalf("unsafe or imprecise error classification: %q", got)
				}
			}
			if got := secretStoreErrorText(m, errFlaky); got != errFlaky.Error() {
				t.Fatal("unrelated backend error wording changed")
			}
			if got := secretStoreErrorText(m, keychain.ErrMasterMissing); got != m.SecretsFailureDetail(m.SecretsStageOpen(), m.SecretsReasonMasterMissing()) {
				t.Fatal("known store error was not classified")
			}
		})
	}
}
