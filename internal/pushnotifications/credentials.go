package pushnotifications

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/google/uuid"
)

// ASCIIのAPIキーはJSONのエスケープで最大6倍に展開される。
const maxCredentialBytes = 32 * 1024

var errCredential = ErrInvalid

// NormalizeCredentialは厳密な資格情報JSONを検証してコンパクト形式へ正規化する。
// エラーには入力値もパーサーの詳細も含めない。
func NormalizeCredential(value string) (string, error) {
	credential, err := ParseCredential([]byte(value))
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		return "", errCredential
	}
	return string(encoded), nil
}

func ParseCredential(value []byte) (Credentials, error) {
	if len(value) > maxCredentialBytes {
		return Credentials{}, errCredential
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return Credentials{}, errCredential
	}
	fields := make(map[string]string, 2)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return Credentials{}, errCredential
		}
		name, ok := key.(string)
		if !ok || (name != "app_id" && name != "api_key") {
			return Credentials{}, errCredential
		}
		if _, exists := fields[name]; exists {
			return Credentials{}, errCredential
		}
		var field string
		if err := decoder.Decode(&field); err != nil {
			return Credentials{}, errCredential
		}
		fields[name] = field
	}
	if _, err := decoder.Token(); err != nil {
		return Credentials{}, errCredential
	}
	if _, err := decoder.Token(); err != io.EOF {
		return Credentials{}, errCredential
	}
	if !validUUID(fields["app_id"]) || !printableToken(fields["api_key"], 4096) {
		return Credentials{}, errCredential
	}
	return Credentials{AppID: strings.ToLower(fields["app_id"]), APIKey: fields["api_key"]}, nil
}

func validUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && len(value) == 36 && id != uuid.Nil && strings.EqualFold(id.String(), value) && id.Variant() == uuid.RFC4122 && id.Version() >= 1 && id.Version() <= 8
}

func printableToken(value string, limit int) bool {
	if len(value) == 0 || len(value) > limit {
		return false
	}
	for i := range len(value) {
		if value[i] < 33 || value[i] > 126 {
			return false
		}
	}
	return true
}
