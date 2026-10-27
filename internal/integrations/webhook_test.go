package integrations

import (
	"net/http"
	"testing"
	"time"
)

func TestWebhookOfficialVectorAndBoundaries(t *testing.T) {
	h := http.Header{"Svix-Id": {"msg_loFOjxBNrRLzqYUf"}, "Svix-Timestamp": {"1731705121"}, "Svix-Signature": {"v1,rAvfW3dJ/X/qxhsaXPOyyCGmRKsaKWcsNccKXlIktD0="}}
	body := []byte(`{"event_type":"ping","data":{"success":true}}`)
	secret := []byte("whsec_plJ3nmyCDGBKInavdOK15jsl")
	for _, delta := range []int64{-300, 0, 300} {
		if err := VerifyWebhook(body, h, secret, time.Unix(1731705121+delta, 0)); err != nil {
			t.Fatalf("boundary %d: %v", delta, err)
		}
	}
	for _, delta := range []int64{-301, 301} {
		if VerifyWebhook(body, h, secret, time.Unix(1731705121+delta, 0)) == nil {
			t.Fatal("stale signature accepted")
		}
	}
	if VerifyWebhook(append(body, ' '), h, secret, time.Unix(1731705121, 0)) == nil {
		t.Fatal("tampered body accepted")
	}
	h.Add("Svix-Id", "second")
	if VerifyWebhook(body, h, secret, time.Unix(1731705121, 0)) == nil {
		t.Fatal("duplicate header accepted")
	}
}
