package policy

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	const pem = "-----BEGIN OPENSSH PRIVATE KEY-----\nZmFrZS1rZXktZm9yLXRlc3RzLW9ubHk=\n-----END OPENSSH PRIVATE KEY-----"
	cases := []struct {
		name, in, want string
	}{
		{"plain", "ls -la /tmp", "ls -la /tmp"},
		{"pem", "cat <<EOF\n" + pem + "\nEOF", "cat <<EOF\n[REDACTED-PRIVATE-KEY]\nEOF"},
		{"bearer", "Authorization: Bearer fake-token-not-real", "Authorization: Bearer [REDACTED]"},
		{"aws", "key AKIAFAKEFAKEFAKEFAKE done", "key [REDACTED-AWS-KEY] done"},
		{"aws short", "AKIAFAKE", "AKIAFAKE"},
		{"password", "echo password=fake-password-value", "echo password=[REDACTED]"},
		{"passwd", "passwd=abc", "passwd=[REDACTED]"},
		{"token", "token=abc", "token=[REDACTED]"},
		{"secret", "secret=abc", "secret=[REDACTED]"},
		{"long password flag", "--password=fake-password-value", "--password=[REDACTED]"},
		{"long password space", "mysql --password fake-password-value", "mysql --password [REDACTED]"},
		{"mysql attached", "mysql -pfake-db-pass -e select", "mysql -p[REDACTED] -e select"},
		{"mysql space", "mysql -p fake-db-pass", "mysql -p [REDACTED]"},
		{"print flag", "find . -print", "find . -print"},
		{"url", "curl https://user:fake-pass@example.com/a", "curl https://user:[REDACTED]@example.com/a"},
		{"url no password", "curl https://example.com/a", "curl https://example.com/a"},
		{"ghp", "ghp_abcdefghijklmnopqrstuvwxyz012345", "[REDACTED]"},
		{"gho", "gho_abcdefghijklmnopqrstuvwxyz012345", "[REDACTED]"},
		{"ghu", "ghu_abcdefghijklmnopqrstuvwxyz012345", "[REDACTED]"},
		{"ghs", "ghs_abcdefghijklmnopqrstuvwxyz012345", "[REDACTED]"},
		{"ghr", "ghr_abcdefghijklmnopqrstuvwxyz012345", "[REDACTED]"},
		{"gh short", "ghp_short", "ghp_short"},
		{"github pat", "github_pat_abcdefghijklmnopqrstuvwxyz", "[REDACTED]"},
		{"sk", "sk-abcdefghijklmnopqrstuvwxyz", "[REDACTED]"},
		{"sk short", "sk-abc", "sk-abc"},
		{"api key", "api_key=supersecretvalue", "api_key=[REDACTED]"},
		{"api-key", "api-key=supersecretvalue", "api-key=[REDACTED]"},
		{"apikey", "apikey=supersecretvalue", "apikey=[REDACTED]"},
		{"access token", "access_token=supersecretvalue", "access_token=[REDACTED]"},
		{"access-token colon", "access-token: supersecretvalue", "access-token: [REDACTED]"},
		{"ordinary", "df -h\ncat /etc/hostname\n", "df -h\ncat /etc/hostname\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Redact(c.in); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
	t.Run("secrets absent together", func(t *testing.T) {
		in := pem + " Bearer fake-token-not-real AKIAFAKEFAKEFAKEFAKE password=fake-password-value -pfake-db-pass https://user:fake-pass@example.com" +
			" ghp_abcdefghijklmnopqrstuvwxyz012345 github_pat_abcdefghijklmnopqrstuvwxyz sk-abcdefghijklmnopqrstuvwxyz api_key=supersecretvalue"
		got := Redact(in)
		for _, secret := range []string{
			"ZmFrZS1rZXktZm9yLXRlc3RzLW9ubHk=",
			"fake-token-not-real",
			"AKIAFAKEFAKEFAKEFAKE",
			"fake-password-value",
			"fake-db-pass",
			"fake-pass",
			"ghp_abcdefghijklmnopqrstuvwxyz012345",
			"github_pat_abcdefghijklmnopqrstuvwxyz",
			"sk-abcdefghijklmnopqrstuvwxyz",
			"supersecretvalue",
		} {
			if strings.Contains(got, secret) {
				t.Fatalf("secret %q still in %q", secret, got)
			}
		}
	})
}
