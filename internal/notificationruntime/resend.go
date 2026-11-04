// Package notificationruntime delivers fixed notification templates. Database
// authorization, verified recipient lookup and durable send receipts belong to
// the caller; this transport never resolves credentials or retries a delivery.
package notificationruntime

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/google/uuid"
)

type SendState string

const (
	Accepted SendState = "accepted"
	Rejected SendState = "rejected"
	Unknown  SendState = "unknown"
	Invalid  SendState = "invalid"
)

// Delivery is ephemeral runtime input, never a Hatchet payload. The caller
// supplies only its currently authorized sender and verified owner's address.
// Subject, text and HTML cannot be supplied independently of structured facts.
type Delivery struct {
	ID        string
	From      string
	To        string
	Candidate notifications.Candidate
}

type SendResult struct {
	State      SendState
	ProviderID string
}

type Sender interface {
	Send(context.Context, []byte, Delivery) SendResult
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type ResendSender struct{ client httpDoer }

// NewResendSender copies the client and standard transport, disabling connection
// reuse, HTTP/2 and redirects so net/http cannot replay a delivery. A custom
// RoundTripper is a trusted injection and must itself perform only one attempt.
func NewResendSender(client *http.Client) *ResendSender {
	if client == nil {
		client = http.DefaultClient
	}
	bounded := *client
	if bounded.Timeout <= 0 || bounded.Timeout > 8*time.Second {
		bounded.Timeout = 8 * time.Second
	}
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	transport := bounded.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if standard, ok := transport.(*http.Transport); ok {
		single := standard.Clone()
		single.DisableKeepAlives = true
		single.ForceAttemptHTTP2 = false
		single.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
		single.Protocols = new(http.Protocols)
		single.Protocols.SetHTTP1(true)
		if single.TLSClientConfig == nil {
			single.TLSClientConfig = &tls.Config{}
		}
		single.TLSClientConfig.NextProtos = []string{"http/1.1"}
		transport = single
	}
	bounded.Transport = transport
	return &ResendSender{client: &bounded}
}

func canonicalAddress(value string) bool {
	if len(value) > 254 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	a, err := mail.ParseAddress(value)
	return err == nil && a.Address == value && a.Name == "" && strings.Contains(value, "@")
}

func canonicalID(value string) (string, bool) {
	if len(value) != 36 {
		return "", false
	}
	id, err := uuid.Parse(value)
	return id.String(), err == nil && id != uuid.Nil
}

func validAPIKey(key []byte) bool {
	if len(key) == 0 || len(key) > 8192 {
		return false
	}
	for _, ch := range key {
		if ch <= ' ' || ch >= 127 {
			return false
		}
	}
	return true
}

func (s *ResendSender) Send(ctx context.Context, apiKey []byte, delivery Delivery) SendResult {
	invalid := SendResult{State: Invalid}
	id, ok := canonicalID(delivery.ID)
	if !ok || !canonicalAddress(delivery.From) || !canonicalAddress(delivery.To) || !validAPIKey(apiKey) {
		return invalid
	}
	content, err := notifications.RenderEmail(delivery.Candidate)
	if err != nil {
		return invalid
	}
	body, err := json.Marshal(struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
		HTML    string   `json:"html"`
	}{delivery.From, []string{delivery.To}, content.Subject, content.Text, content.HTML})
	if err != nil {
		return invalid
	}
	unknown := SendResult{State: Unknown}
	if s == nil || s.client == nil {
		return unknown
	}
	bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	// Wrapping the reader prevents NewRequest from installing GetBody. The
	// provider idempotency header must not make this body replayable by net/http.
	req, err := http.NewRequestWithContext(bounded, http.MethodPost, "https://api.resend.com/emails", io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		return unknown
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+string(apiKey))
	req.Header.Set("Idempotency-Key", "cycrio-notification-"+id)
	res, err := s.client.Do(req)
	if err != nil {
		if res != nil && res.Body != nil {
			res.Body.Close()
		}
		return unknown
	}
	if res == nil || res.Body == nil {
		return unknown
	}
	defer res.Body.Close()
	// A conflict can mean the same idempotent request is already in flight.
	// Timeouts and early requests likewise provide no definitive send outcome.
	if res.StatusCode >= 400 && res.StatusCode < 500 && res.StatusCode != 408 && res.StatusCode != 409 && res.StatusCode != 425 {
		return SendResult{State: Rejected}
	}
	if res.StatusCode != http.StatusOK {
		return unknown
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4097))
	if err != nil || len(raw) > 4096 {
		return unknown
	}
	providerID, ok := acknowledgementID(raw)
	if !ok {
		return unknown
	}
	return SendResult{State: Accepted, ProviderID: providerID}
}

// Only an unambiguous provider ACK is acceptance. Upstream bodies and errors
// are never returned or logged, including on rejection or malformed responses.
func acknowledgementID(raw []byte) (string, bool) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') || !d.More() {
		return "", false
	}
	key, err := d.Token()
	if err != nil || key != "id" {
		return "", false
	}
	var id string
	if d.Decode(&id) != nil || d.More() {
		return "", false
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') || d.Decode(new(any)) != io.EOF {
		return "", false
	}
	return canonicalID(id)
}
