// Package healthchecks sends empty, explicitly configured Healthchecks.io pings.
package healthchecks

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrUnavailable = errors.New("monitoring unavailable")

func ValidPingURL(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host != "hc-ping.com" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" || !strings.HasPrefix(u.Path, "/") {
		return false
	}
	id := strings.TrimPrefix(u.Path, "/")
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id && u.String() == value
}

type Client struct{ http *http.Client }

func boundedClient(client *http.Client, timeout time.Duration) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	copy := *client
	if copy.Timeout == 0 || copy.Timeout > timeout {
		copy.Timeout = timeout
	}
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copy
}
func NewClient(client *http.Client) *Client {
	return &Client{http: boundedClient(client, 3*time.Second)}
}

func responseOK(response *http.Response, expected string) bool {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1025))
	return err == nil && len(body) <= 1024 && response.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == expected
}

func (c *Client) Ping(ctx context.Context, pingURL string) error {
	if !ValidPingURL(pingURL) {
		return ErrUnavailable
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, pingURL, nil)
	if err != nil {
		return ErrUnavailable
	}
	response, err := c.http.Do(r)
	if err != nil {
		return ErrUnavailable
	}
	if !responseOK(response, "OK") {
		return ErrUnavailable
	}
	return nil
}

// The api service alias is shared by local and production Compose deployments.
func NewAPIProbe(client *http.Client) func(context.Context) error {
	probe := boundedClient(client, time.Second)
	return func(ctx context.Context) error {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://api:8080/readyz", nil)
		if err != nil {
			return ErrUnavailable
		}
		response, err := probe.Do(r)
		if err != nil {
			return ErrUnavailable
		}
		if !responseOK(response, "ok") {
			return ErrUnavailable
		}
		return nil
	}
}
