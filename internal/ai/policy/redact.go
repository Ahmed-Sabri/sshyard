package policy

import "regexp"

// Redact masks secrets in text that may be written to the audit log or
// sent to a model. It is a best-effort scan, not a parser.
//
// It masks PEM private-key blocks, bearer tokens, AWS access key ids
// (AKIA plus 16 letters or digits), GitHub tokens (ghp_, gho_, ghu_,
// ghs_, ghr_, github_pat_), sk- keys of 20 or more characters,
// api_key/access_token/password/passwd/token/secret assignments (equals
// or colon, including --password=), mysql/mariadb -p passwords, and
// user:password in URLs. An attached mysql -p value that is only letters
// (the shape of a short flag such as -print) is left alone unless the
// text names mysql or mariadb.
func Redact(s string) string {
	if s == "" {
		return s
	}
	s = pemRE.ReplaceAllString(s, "[REDACTED-PRIVATE-KEY]")
	s = urlRE.ReplaceAllString(s, "${1}${2}:[REDACTED]@")
	s = bearerRE.ReplaceAllString(s, "Bearer [REDACTED]")
	s = awsRE.ReplaceAllString(s, "[REDACTED-AWS-KEY]")
	s = ghTokenRE.ReplaceAllString(s, "[REDACTED]")
	s = ghPatRE.ReplaceAllString(s, "[REDACTED]")
	s = skTokenRE.ReplaceAllString(s, "[REDACTED]")
	s = assignRE.ReplaceAllString(s, "${1}=[REDACTED]")
	s = longFlagRE.ReplaceAllString(s, "${1} [REDACTED]")
	s = redactMySQLPassword(s)
	return s
}

var (
	pemRE      = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	urlRE      = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)([^/\s:@]+):([^@\s/]+)@`)
	bearerRE   = regexp.MustCompile(`(?i)Bearer[ \t]+[A-Za-z0-9._~+/=-]+`)
	awsRE      = regexp.MustCompile(`AKIA[A-Z0-9]{16}`)
	ghTokenRE  = regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`)
	ghPatRE    = regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`)
	skTokenRE  = regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`)
	assignRE   = regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|password|passwd|token|secret)[ \t]*[:=][ \t]*\S+`)
	longFlagRE = regexp.MustCompile(`(?i)(--(?:password|passwd|token|secret))[ \t]+\S+`)
	mysqlRE    = regexp.MustCompile(`(?i)(^|[\s])(-p)(\S+)`)
	mysqlSpRE  = regexp.MustCompile(`(?i)(^|[\s])(-p)[ \t]+(\S+)`)
	sqlWord    = regexp.MustCompile(`(?i)\b(?:mysql|mariadb)\b`)
)

func redactMySQLPassword(s string) string {
	sql := sqlWord.MatchString(s)
	if sql {
		s = mysqlSpRE.ReplaceAllString(s, "${1}${2} [REDACTED]")
	}
	return mysqlRE.ReplaceAllStringFunc(s, func(m string) string {
		parts := mysqlRE.FindStringSubmatch(m)
		if len(parts) != 4 {
			return m
		}
		lead, flag, val := parts[1], parts[2], parts[3]
		if val == "" {
			return m
		}
		letters := true
		for _, r := range val {
			if r < 'A' || (r > 'Z' && r < 'a') || r > 'z' {
				letters = false
				break
			}
		}
		if letters && !sql {
			return m
		}
		return lead + flag + "[REDACTED]"
	})
}
