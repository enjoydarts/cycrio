package subscriptions

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		valid bool
		want  string
	}{
		{"  家族用  ", true, "家族用"}, {"", true, ""}, {"   ", true, ""},
		{strings.Repeat("名", 200), true, strings.Repeat("名", 200)},
		{strings.Repeat("名", 201), false, ""}, {"line\nname", false, ""}, {"\tname", false, ""}, {string([]byte{0xff}), false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeName(NameInput{DisplayName: &tc.name, Confirmed: true, ExpectedVersion: 1})
			if tc.valid {
				if err != nil || tc.want == "" && got.DisplayName != nil || tc.want != "" && (got.DisplayName == nil || *got.DisplayName != tc.want) {
					t.Fatal(got, err)
				}
			} else if !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
	for _, in := range []NameInput{{Confirmed: true, ExpectedVersion: 1}, {ExpectedVersion: 1}, {Confirmed: true}} {
		_, err := NormalizeName(in)
		if (in.Confirmed && in.ExpectedVersion > 0) != (err == nil) {
			t.Fatal(in, err)
		}
	}
}
