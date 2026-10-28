package mailaccounts

import (
	"context"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

// ConnectionSecret is only marshaled inside the encrypted credential envelope.
// Login/Password retain their byte encoding for existing Fastmail credentials.
type ConnectionSecret struct {
	Login         []byte    `json:"login"`
	Password      []byte    `json:"password"`
	Provider      string    `json:"provider,omitempty"`
	Host          string    `json:"host,omitempty"`
	Port          int       `json:"port,omitempty"`
	OAuthClientID string    `json:"oauth_client_id,omitempty"`
	AccessToken   []byte    `json:"access_token,omitempty"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
}

type CheckerFactory func(context.Context, CredentialVersion) (Checker, error)
type Option func(*Service)

func WithCheckerFactory(f CheckerFactory) Option { return func(s *Service) { s.checkerFactory = f } }

// Syntax checks precede encryption. The IMAP transport additionally resolves and
// validates every address, then pins the actual socket to a validated public IP.
func ValidIMAPHost(host string) bool {
	if host != strings.ToLower(host) || len(host) > 253 || host == "" || strings.HasSuffix(host, ".") {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
	}
	if !strings.Contains(host, ".") || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return false
	}
	label := regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	for _, part := range strings.Split(host, ".") {
		if !label.MatchString(part) {
			return false
		}
	}
	return true
}
