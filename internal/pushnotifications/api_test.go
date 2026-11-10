package pushnotifications

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestPushRevocationTokenDerivesFromReceivedChallenge(t *testing.T) {
	secret := strings.Repeat("A", 43)
	sum := sha256.Sum256([]byte("cycrio-push-revocation/v1\x00" + secret))
	token, e := DeriveRevocationToken(secret)
	if e != nil || token != base64.RawURLEncoding.EncodeToString(sum[:]) || token == secret {
		t.Fatal("解除トークンの共通導出式", e)
	}
	for _, bad := range []string{"", strings.Repeat("a", 43), strings.Repeat("A", 44), "secret"} {
		if _, e = DeriveRevocationToken(bad); !errors.Is(e, ErrInvalid) {
			t.Fatal("不正な秘密", e)
		}
	}
}
