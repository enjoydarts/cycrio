package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/pushnotifications"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestPushCredentialNormalizesBeforeEncryptedStorage(t *testing.T) {
	s, repo := testService(t)
	value := "{\n \"app_id\": \"A38BA9C8-0319-409A-84CF-F2684F885EDC\", \"api_key\": \"synthetic-push-key\"\n}"
	if _, err := s.Put(context.Background(), uuid.NewString(), "onesignal", 0, value); err != nil {
		t.Fatal(err)
	}
	plain, err := OpenVersion(s.keyring, repo.version)
	if err != nil || string(plain) != `{"app_id":"a38ba9c8-0319-409a-84cf-f2684f885edc","api_key":"synthetic-push-key"}` {
		t.Fatal("正規化した暗号文", err)
	}
	for _, bad := range []string{`null`, `{"app_id":"bad","api_key":"synthetic-push-key"}`, strings.Replace(value, `"api_key":`, `"extra":`, 1), strings.Replace(value, `"api_key":`, `"api_key":"duplicate","api_key":`, 1)} {
		if _, err := s.Put(context.Background(), uuid.NewString(), "onesignal", 1, bad); !errors.Is(err, ErrInvalid) {
			t.Fatal("不正な資格情報", err)
		}
	}
}

func TestPushCredentialMaximumEscapedAPIKey(t *testing.T) {
	s, repo := testService(t)
	key := strings.Repeat(`"`, 4096)
	raw, _ := json.Marshal(pushnotifications.Credentials{AppID: uuid.NewString(), APIKey: key})
	if _, err := s.Put(context.Background(), uuid.NewString(), "onesignal", 0, string(raw)); err != nil {
		t.Fatal("最大キーの正規化JSONを保存", err)
	}
	plain, err := OpenVersion(s.keyring, repo.version)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := pushnotifications.ParseCredential(plain)
	if err != nil || parsed.APIKey != key {
		t.Fatal("最大キーの復号", err)
	}
}
