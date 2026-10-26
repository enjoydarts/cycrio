package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

// Every editable member is explicit, including unknown values represented by null.
// Token inspection rejects duplicates before encoding/json can discard them.
func readSubscriptionJSON(w http.ResponseWriter, r *http.Request, target any, keys []string, nonNull ...string) error {
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" {
		return subscriptions.ErrInvalid
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return subscriptions.ErrInvalid
	}
	values := map[string]json.RawMessage{}
	for d.More() {
		token, e := d.Token()
		key, ok := token.(string)
		if e != nil || !ok || !slices.Contains(keys, key) {
			return subscriptions.ErrInvalid
		}
		if _, exists := values[key]; exists {
			return subscriptions.ErrInvalid
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil || !utf8.Valid(raw) {
			return subscriptions.ErrInvalid
		}
		values[key] = raw
	}
	token, e = d.Token()
	if e != nil || token != json.Delim('}') || d.Decode(new(any)) != io.EOF {
		return subscriptions.ErrInvalid
	}
	for _, key := range keys {
		raw, exists := values[key]
		if !exists || slices.Contains(nonNull, key) && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return subscriptions.ErrInvalid
		}
	}
	raw, e := json.Marshal(values)
	if e != nil || json.Unmarshal(raw, target) != nil {
		return subscriptions.ErrInvalid
	}
	return nil
}
func applicationQuery(raw string, allowed ...string) (url.Values, error) {
	if len(raw) > 4096 {
		return nil, subscriptions.ErrInvalid
	}
	q, e := url.ParseQuery(raw)
	if e != nil {
		return nil, subscriptions.ErrInvalid
	}
	for key, values := range q {
		if !slices.Contains(allowed, key) || len(values) != 1 {
			return nil, subscriptions.ErrInvalid
		}
	}
	return q, nil
}
func applicationPagination(q url.Values) (limit, offset int, e error) {
	limit = 20
	if q.Has("limit") {
		limit, e = strconv.Atoi(q.Get("limit"))
		if e != nil || limit < 1 || limit > 100 {
			return 0, 0, subscriptions.ErrInvalid
		}
	}
	if q.Has("offset") {
		offset, e = strconv.Atoi(q.Get("offset"))
		if e != nil || offset < 0 {
			return 0, 0, subscriptions.ErrInvalid
		}
	}
	return limit, offset, nil
}
func subscriptionFilter(raw string) (subscriptions.Filter, error) {
	f := subscriptions.Filter{}
	q, e := applicationQuery(raw, "q", "status", "sort", "limit", "offset")
	if e != nil {
		return f, e
	}
	if q.Has("status") && q.Get("status") == "" || q.Has("sort") && q.Get("sort") == "" {
		return f, subscriptions.ErrInvalid
	}
	f.Query = q.Get("q")
	f.Status = q.Get("status")
	f.Sort = q.Get("sort")
	f.Limit, f.Offset, e = applicationPagination(q)
	if e != nil || subscriptions.InvalidFilter(f) {
		return f, subscriptions.ErrInvalid
	}
	return f, nil
}
