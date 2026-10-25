package mailaccounts

import (
	"github.com/google/uuid"
	"math"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

func validID(s string) bool {
	v, e := uuid.Parse(s)
	return e == nil && v != uuid.Nil && v.String() == s
}
func validOwned(owner, id string, version int64) bool {
	return validID(owner) && validID(id) && version >= 0 && version < math.MaxInt64
}
func cleanText(s string, max int) bool {
	if !utf8.ValidString(s) || len(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validSave(owner, id string, in SaveInput) (string, bool) {
	label := strings.TrimSpace(in.Label)
	if !validOwned(owner, id, in.ExpectedVersion) || label == "" || !cleanText(label, 320) || utf8.RuneCountInString(label) > 80 || !cleanText(in.Login, 254) || len(in.AppPassword) < 8 || len(in.AppPassword) > 1024 {
		return "", false
	}
	address, err := mail.ParseAddress(in.Login)
	if err != nil || address.Name != "" || address.Address != in.Login || !strings.Contains(in.Login, "@") {
		return "", false
	}
	for _, b := range []byte(in.AppPassword) {
		if b < 33 || b > 126 {
			return "", false
		}
	}
	return label, true
}
func validFolders(items []RemoteFolder) bool {
	if len(items) > 1000 {
		return false
	}
	seen := map[string]bool{}
	for _, f := range items {
		if f.Name == "" || !cleanText(f.Name, 1024) || seen[f.Name] || !cleanText(f.Delimiter, 4) || utf8.RuneCountInString(f.Delimiter) > 1 {
			return false
		}
		seen[f.Name] = true
	}
	return true
}
func validErrorCode(s string) bool {
	switch s {
	case "authentication_failed", "tls_failed", "connection_failed", "timeout", "response_too_large", "folder_limit", "invalid_response", "credentials_unavailable", "cancelled", "lease_expired":
		return true
	}
	return false
}
