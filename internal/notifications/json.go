package notifications

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
)

func strictMembers(raw []byte, keys []string) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return nil, ErrInvalid
	}
	values := map[string]json.RawMessage{}
	for d.More() {
		token, e = d.Token()
		key, ok := token.(string)
		if e != nil || !ok || !slices.Contains(keys, key) {
			return nil, ErrInvalid
		}
		if _, exists := values[key]; exists {
			return nil, ErrInvalid
		}
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, ErrInvalid
		}
		values[key] = value
	}
	token, e = d.Token()
	if e != nil || token != json.Delim('}') || d.Decode(new(any)) != io.EOF || len(values) != len(keys) {
		return nil, ErrInvalid
	}
	return values, nil
}
func (c *Channels) UnmarshalJSON(raw []byte) error {
	fields, e := strictMembers(raw, []string{"in_app", "email", "push"})
	if e != nil {
		return e
	}
	var next Channels
	if json.Unmarshal(fields["in_app"], &next.InApp) != nil || json.Unmarshal(fields["email"], &next.Email) != nil || json.Unmarshal(fields["push"], &next.Push) != nil {
		return ErrInvalid
	}
	*c = next
	return nil
}

type KindChannels map[Kind]Channels

func (p *Preferences) UnmarshalJSON(raw []byte) error {
	if _, err := strictMembers(raw, []string{"version", "kinds", "lead_days", "send_hour", "timezone", "email_opt_in", "push_opt_in"}); err != nil {
		return err
	}
	type plain Preferences
	var next plain
	if err := json.Unmarshal(raw, &next); err != nil {
		return ErrInvalid
	}
	*p = Preferences(next)
	return nil
}

func (s *SettingsInput) UnmarshalJSON(raw []byte) error {
	fields, err := strictMembers(raw, []string{"expected_version", "kinds", "lead_days", "send_hour", "timezone", "email_opt_in", "push_opt_in", "confirmed"})
	if err != nil {
		return err
	}
	var next SettingsInput
	if json.Unmarshal(fields["expected_version"], &next.ExpectedVersion) != nil || json.Unmarshal(fields["confirmed"], &next.Confirmed) != nil {
		return ErrInvalid
	}
	delete(fields, "expected_version")
	delete(fields, "confirmed")
	fields["version"], _ = json.Marshal(next.ExpectedVersion)
	preferences, _ := json.Marshal(fields)
	if json.Unmarshal(preferences, &next.Preferences) != nil {
		return ErrInvalid
	}
	*s = next
	return nil
}

func (k *KindChannels) UnmarshalJSON(raw []byte) error {
	keys := []string{}
	for _, kind := range kinds {
		keys = append(keys, string(kind))
	}
	fields, e := strictMembers(raw, keys)
	if e != nil {
		return e
	}
	next := KindChannels{}
	for key, value := range fields {
		var channels Channels
		if json.Unmarshal(value, &channels) != nil {
			return ErrInvalid
		}
		next[Kind(key)] = channels
	}
	*k = next
	return nil
}
