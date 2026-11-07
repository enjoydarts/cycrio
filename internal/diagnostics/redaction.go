package diagnostics

import (
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxExceptionText = 2048

var (
	emailPattern             = regexp.MustCompile(`(?i)[\p{L}\p{N}\p{M}.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[\p{L}\p{N}](?:[\p{L}\p{N}\p{M}.-]*[\p{L}\p{N}])?\.[\p{L}\p{N}\p{M}-]{2,}`)
	bearerPattern            = regexp.MustCompile(`(?i)\bBearer\s+[a-z0-9._~+/-]+=*`)
	jwtPattern               = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	credentialPattern        = regexp.MustCompile(`\b(?:sk[-_][A-Za-z0-9_-]{12,}|ph[csx]_[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9_]{12,}|github_pat_[A-Za-z0-9_]{12,}|xox[baprs]-[A-Za-z0-9-]{12,}|(?:AKIA|ASIA)[A-Z0-9]{16}|ya29\.[A-Za-z0-9._~-]{12,}|GOCSPX-[A-Za-z0-9_-]{12,}|re_[A-Za-z0-9_-]{16,}|whsec_[A-Za-z0-9+/=_-]{12,}|AIza[A-Za-z0-9_-]{20,}|user_[A-Za-z0-9_-]{12,})`)
	privateURLSegmentPattern = regexp.MustCompile(`(?i)^(?:users?|accounts?|profiles?|people|recipients?|senders?|emails?)$`)
	urlPattern               = regexp.MustCompile(`(?i)(?:https?|postgres(?:ql)?|imaps?|smtps?|mysql|rediss?|mongodb(?:\+srv)?|sqlserver)://[^\s<>"']+`)
	privateFieldPattern      = regexp.MustCompile(`(?is)\b(authorization|proxy-authorization|cookie|set-cookie|password|passwd|app[-_ ]?password|client[-_ ]?secret|secret|api[-_ ]?key|token|access[-_ ]?token|refresh[-_ ]?token|id[-_ ]?token|authorization[-_ ]?code|login|user(?:[-_ ]?name)?|body|response[_-]?body|response|subject|from|to|recipient|payload|prompt|content|input|headers|email)\b\s*["']?\s*[:=]\s*.*`)
	objectPattern            = regexp.MustCompile(`(?s)\{\s*"[^"\n]+"\s*:.*(?:\}|$)|\[\s*["'{].*(?:\]|$)`)
	unexpectedJSONPattern    = regexp.MustCompile(`(?is)(unexpected token[^,\n]{0,120},\s*)("[^"]*(?:"|$)|'[^']*(?:'|$))`)
	inputSnippetPattern      = regexp.MustCompile(`(?is)(invalid (?:input|value|syntax|json)[^"'\n]{0,120})("[^"]*(?:"|$)|'[^']*(?:'|$))`)
	parserInputPattern       = regexp.MustCompile(`(?i)(parsing(?: time)?\s+|cannot parse\s+)("(?:\\.|[^"\\])*(?:"|$)|'(?:\\.|[^'\\])*(?:'|$))`)
	absoluteSourcePattern    = regexp.MustCompile(`(?:/[A-Za-z0-9_.@-]+)+/[A-Za-z0-9_.-]+\.go`)
	homePathPattern          = regexp.MustCompile(`(?i)(?:/(?:Users|home)/[^/\s"'<>:]+|/root)(?:/[^\s"'<>:]*)?|[A-Z]:\\Users\\[^\s"'<>:]+`)
)

// scrubText retains ordinary technical messages, including multiline causes and
// quoted identifiers, while removing recognizable secrets and embedded payloads.
func ScrubText(text string) string {
	if len(text) > 16*1024 {
		text = text[:16*1024]
	}
	text = strings.ToValidUTF8(text, "")
	text = urlPattern.ReplaceAllStringFunc(text, func(raw string) string {
		// Error strings commonly append punctuation after a URL.
		trailing := ""
		for len(raw) > 0 && strings.ContainsRune(")],;:", rune(raw[len(raw)-1])) {
			trailing = raw[len(raw)-1:] + trailing
			raw = raw[:len(raw)-1]
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return "[redacted URL]" + trailing
		}
		u.User = nil
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = ""
		u.RawFragment = ""
		segments := strings.Split(u.Path, "/")
		for index := len(segments) - 1; index > 0; index-- {
			if privateURLSegmentPattern.MatchString(segments[index-1]) && segments[index] != "" && segments[index] != "me" {
				segments[index] = "[redacted]"
			}
		}
		u.Path = strings.Join(segments, "/")
		if strings.EqualFold(u.Hostname(), "hc-ping.com") && u.Path != "" && u.Path != "/" {
			u.Path = "/[redacted]"
		}
		u.RawPath = ""
		return u.String() + trailing
	})
	text = privateFieldPattern.ReplaceAllString(text, "$1=[redacted]")
	text = objectPattern.ReplaceAllString(text, "[redacted data]")
	text = unexpectedJSONPattern.ReplaceAllString(text, "$1[redacted input]")
	text = inputSnippetPattern.ReplaceAllString(text, "$1[redacted input]")
	text = parserInputPattern.ReplaceAllString(text, "$1[redacted input]")
	text = emailPattern.ReplaceAllString(text, "[redacted]")
	text = bearerPattern.ReplaceAllString(text, "Bearer [redacted]")
	text = jwtPattern.ReplaceAllString(text, "[redacted]")
	text = credentialPattern.ReplaceAllString(text, "[redacted]")
	text = homePathPattern.ReplaceAllString(text, "[redacted path]")
	text = absoluteSourcePattern.ReplaceAllStringFunc(text, func(filename string) string { return path.Base(filename) })
	text = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, text)
	text = strings.TrimSpace(text)
	if len(text) > maxExceptionText {
		text = text[:maxExceptionText]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		text += "…"
	}
	if text == "" {
		return "error detail unavailable"
	}
	return text
}
