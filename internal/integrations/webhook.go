// Package integrations implements explicit outbound email tests and signed provider events.
package integrations

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// VerifyWebhook follows the published Svix v1 protocol over the unmodified body.
func VerifyWebhook(body []byte, h http.Header, secret []byte, now time.Time) error {
	for _, name := range []string{"svix-id", "svix-timestamp", "svix-signature"} {
		if len(h.Values(name)) != 1 || h.Get(name) == "" || len(h.Get(name)) > 4096 {
			return ErrInvalid
		}
	}
	timestamp, err := strconv.ParseInt(h.Get("svix-timestamp"), 10, 64)
	if err != nil || timestamp < now.Unix()-300 || timestamp > now.Unix()+300 {
		return ErrInvalid
	}
	if !strings.HasPrefix(string(secret), "whsec_") {
		return ErrInvalid
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(string(secret), "whsec_"))
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(strings.TrimPrefix(string(secret), "whsec_"))
	}
	if err != nil || len(key) < 16 {
		return ErrInvalid
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(h.Get("svix-id") + "." + h.Get("svix-timestamp") + "."))
	mac.Write(body)
	expected := mac.Sum(nil)
	for _, signature := range strings.Fields(h.Get("svix-signature")) {
		if strings.HasPrefix(signature, "v1,") {
			got, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(signature, "v1,"))
			if e == nil && hmac.Equal(got, expected) {
				return nil
			}
		}
	}
	return ErrInvalid
}
