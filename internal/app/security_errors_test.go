package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/netproxy"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sftpsvc"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestSecurityErrorTranslations(t *testing.T) {
	cases := []struct {
		err    error
		en, zh string
	}{
		{sftpsvc.ErrReadLimit, "The file exceeds the read limit", "文件超过读取大小上限"},
		{sftpsvc.ErrUnsafePath, "Symbolic links and unsafe paths are not allowed", "不允许符号链接或不安全的路径"},
		{sftpsvc.ErrUnsafeDestination, "The destination folder is writable by other users", "目标文件夹可被其他用户写入"},
		{sftpsvc.ErrOperationsBusy, "Too many SFTP operations are pending; try again later", "待处理的 SFTP 操作已达上限，请稍后重试"},
		{sftpsvc.ErrPartChanged, "The partial download file changed; transfer stopped", "部分下载文件的身份已变化，传输已停止"},
		{errors.New("sftp: invalid path"), "The file path is invalid", "文件路径无效"},
		{errors.New("sftp: parent is not a directory"), "The parent path is not a folder", "父路径不是文件夹"},
		{errors.New("sftp: not a regular file"), "The path is not a regular file", "路径不是普通文件"},
		{netproxy.ErrClientLimit, "The proxy active connection limit has been reached", "代理活跃连接数已达上限"},
		{netproxy.ErrHandshakeTooLarge, "The proxy handshake exceeds the size limit", "代理握手数据超过大小上限"},
	}
	for _, locale := range []string{"en", "zh-Hans"} {
		m := locales.In(locale)
		for _, test := range cases {
			want := test.en
			if locale == "zh-Hans" {
				want = test.zh
			}
			if got := securityErrorText(m, test.err); got != want {
				t.Errorf("%s: %v => %q want %q", locale, test.err, got, want)
			}
			if got := securityText(m, test.err.Error()); got != want {
				t.Errorf("%s string: %q", locale, got)
			}
			if strings.HasPrefix(test.err.Error(), "sftp: ") {
				if got := securityText(m, "sftp: "+test.err.Error()); got != want {
					t.Errorf("wrapped string: %q", got)
				}
			}
		}
		busy := fmt.Errorf("sensitive wrapper: %w", sftpsvc.ErrOperationsBusy)
		if got := securityErrorText(m, busy); got != m.SecuritySftpOperationsBusy() {
			t.Fatalf("wrapped operation limit: %q", got)
		}
		wrapped := fmt.Errorf("sensitive wrapper: %w", sftpsvc.ErrUnsafePath)
		if got := securityErrorText(m, wrapped); strings.Contains(got, "sensitive wrapper") || got != securityText(m, sftpsvc.ErrUnsafePath.Error()) {
			t.Fatalf("typed wrapper: %q", got)
		}
		for _, raw := range []string{"plain diagnostic", "prefix sftp: invalid path", "sftp: invalid path: details", "ordinary\nai.command_output_limit", "ai.command_output_limit extra"} {
			if got := securityText(m, raw); got != raw {
				t.Fatalf("rewrote unknown content %q as %q", raw, got)
			}
		}
	}
}

func TestSecurityToolOutputOnlyMapsExactFirstLine(t *testing.T) {
	for _, locale := range []string{"en", "zh-Hans"} {
		m := locales.In(locale)
		body := "normal output\nai.command_output_limit\nsftp: invalid path\n"
		got := toolOutput(m, "ai.command_output_limit\n"+body)
		if got != m.AiCommandOutputLimit()+"\n"+body {
			t.Fatalf("rewrote body: %q", got)
		}
		for _, raw := range []string{body, "ai.command_output_limit extra\n" + body, "prefix ai.command_output_limit"} {
			if toolOutput(m, raw) != raw {
				t.Fatalf("rewrote ordinary output %q", raw)
			}
		}
		if aiErrLine(m, "ai.command_output_limit") != m.AiRequestFailed(m.AiCommandOutputLimit()) {
			t.Fatal("AI error token not translated")
		}
	}
}

func TestSecurityErrorsNativeViewsBothLanguages(t *testing.T) {
	a := &App{}
	host := assets.Host{Name: "box"}
	cn := &conn{host: host, ai: &aiState{items: []aiItem{
		{kind: "tool", tool: agent.ToolCard{ID: "limit", Name: "run_command", Run: true, Phase: "done", Output: "ai.command_output_limit\nnormal output"}},
		{kind: "err", text: "sftp: invalid path"},
	}}}
	proxyErr := &netproxy.Error{Addr: "proxy.test:8080", Err: netproxy.ErrHandshakeTooLarge}
	tt := ui.NewTester(func(c *ui.Context) {
		m := locales.In(c.Locale())
		ui.Column(c).Children(func() {
			for i := range cn.ai.items {
				a.aiCard(c, cn, i, currentTokens(c), m)
			}
			ui.Text(c, a.statusText(m, host, session.Status{State: session.Failed, Err: proxyErr}, c.Now()))
			ui.Text(c, transferLabel(m, sftpsvc.Transfer{State: sftpsvc.Failed, Err: sftpsvc.ErrPartChanged}))
		})
	}, 900, 700)
	for _, locale := range []string{"en", "zh-Hans"} {
		tt.SetLocale(locale)
		tt.Frame()
		m := locales.In(locale)
		for _, want := range []string{m.AiCommandOutputLimit(), "normal output", m.SecuritySftpInvalidPath(), m.SecurityProxyHandshakeLimit(), m.SecuritySftpPartChanged()} {
			if !tt.HasText(want) {
				t.Errorf("%s missing %q in %q", locale, want, tt.Texts())
			}
		}
		for _, raw := range []string{"ai.command_output_limit", "sftp: invalid path", "proxy: handshake too large", "sftp: partial file identity changed"} {
			if tt.HasText(raw) {
				t.Errorf("%s raw error visible: %q", locale, raw)
			}
		}
	}
}
