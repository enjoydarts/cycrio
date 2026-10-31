package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/subscriptionprompts"
)

func TestLiquidUsesSharedCriteriaAndExplicitOverride(t *testing.T) {
	for _, override := range []map[string]string{nil, {"subscription_related": "old related", "not_related": "old excluded", "uncertain": "old uncertain"}} {
		want := override
		if want == nil {
			want = subscriptionprompts.ClassificationCriteria()
		}
		c := clientReply(t, `{"answers":{"classification":{"type":"choice","choice":"uncertain","probabilities":{"subscription_related":0.2,"not_related":0.1,"uncertain":0.7}}}}`, func(r *http.Request) {
			var body struct {
				Questions map[string]struct {
					Criteria map[string]string `json:"criteria"`
				} `json:"questions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(body.Questions["classification"].Criteria, want) {
				t.Fatal("wrong criteria transmitted")
			}
		})
		_, err := c.Do(context.Background(), Request{Provider: "liquid", ModelID: "d1", Key: []byte("synthetic"), Input: RequestInput{Text: "synthetic", ClassificationCriteria: override}})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestLiquidRejectsMalformedCriteriaBeforeSending(t *testing.T) {
	valid := map[string]string{"subscription_related": "yes", "not_related": "no", "uncertain": "maybe"}
	cases := []map[string]string{{}, {"subscription_related": "yes", "not_related": "no"}}
	for _, bad := range []string{"", " \n", string([]byte{0xff}), strings.Repeat("x", 2049)} {
		m := make(map[string]string)
		for k, v := range valid {
			m[k] = v
		}
		m["uncertain"] = bad
		cases = append(cases, m)
	}
	cases = append(cases, map[string]string{"subscription_related": "yes", "not_related": "no", "other": "maybe"}, map[string]string{"subscription_related": "yes", "not_related": "no", "uncertain": "maybe", "extra": "x"})
	for i, criteria := range cases {
		c := clientReply(t, `{}`, func(*http.Request) { t.Fatal("invalid criteria reached transport") })
		_, err := c.Do(context.Background(), Request{Provider: "liquid", ModelID: "d1", Key: []byte("synthetic"), Input: RequestInput{Text: "synthetic", ClassificationCriteria: criteria}})
		if e, ok := err.(*Error); !ok || e.Code != "invalid_request" {
			t.Fatalf("case %d: %v", i, err)
		}
	}
}

func TestClassificationCriteriaAreTransient(t *testing.T) {
	b, err := json.Marshal(RequestInput{Text: "private", Instructions: "private", ClassificationCriteria: map[string]string{"private": "private"}})
	if err != nil || string(b) != "{}" {
		t.Fatal("request input leaked", err)
	}
}

func TestClassificationCriteriaCannotLeakThroughMetadata(t *testing.T) {
	c := clientReply(t, `{"id":"private-criterion","answers":{"classification":{"type":"choice","choice":"uncertain","probabilities":{"subscription_related":0.2,"not_related":0.1,"uncertain":0.7}}}}`, nil)
	out, err := c.Do(context.Background(), Request{Provider: "liquid", ModelID: "d1", Key: []byte("synthetic"), Input: RequestInput{Text: "mail", ClassificationCriteria: map[string]string{"subscription_related": "private-criterion", "not_related": "no", "uncertain": "maybe"}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.GenerationID != "" {
		t.Fatal("classification criterion leaked into metadata")
	}
}
