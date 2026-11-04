package clerkdelete

import (
	"context"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

// Profile is private source data. Public sync endpoints return readiness and
// synchronization time, never a Clerk subject or an email address.
type Profile struct {
	Subject   string `json:"-"`
	Email     string `json:"-"`
	UpdatedAt int64  `json:"-"`
	Blocked   bool   `json:"-"`
	Deleted   bool   `json:"-"`
}

func validProfileEmail(value string) bool {
	parsed, err := mail.ParseAddress(value)
	return err == nil && parsed.Address == value && parsed.Name == "" && len(value) <= 254 && strings.Contains(value, "@") && !strings.ContainsAny(value, "\r\n")
}

// ReadProfile verifies that the backend key belongs to the session issuer
// before fetching only that subject. It never substitutes another address for
// an absent or unverified primary address.
func (c *Client) ReadProfile(ctx context.Context, target Target, secret []byte) (Profile, error) {
	if !validTarget(target, secret) {
		return Profile{}, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if binding := c.verifyTarget(ctx, target, secret); binding.Code != "" {
		return Profile{}, ErrUnavailable
	}
	rsp, err := c.request(ctx, http.MethodGet, "/users/"+url.PathEscape(target.Subject), secret)
	if err != nil {
		return Profile{}, ErrUnavailable
	}
	defer rsp.Body.Close()
	if rsp.StatusCode == http.StatusNotFound {
		return Profile{Subject: target.Subject, Deleted: true, UpdatedAt: time.Now().UnixMilli()}, nil
	}
	if rsp.StatusCode != http.StatusOK {
		return Profile{}, ErrUnavailable
	}
	var user struct {
		Object        string  `json:"object"`
		ID            string  `json:"id"`
		UpdatedAt     int64   `json:"updated_at"`
		Banned        *bool   `json:"banned"`
		Deprovisioned bool    `json:"deprovisioned"`
		Primary       *string `json:"primary_email_address_id"`
		Emails        []struct {
			ID           string `json:"id"`
			Address      string `json:"email_address"`
			Verification struct {
				Status string `json:"status"`
			} `json:"verification"`
		} `json:"email_addresses"`
	}
	if !decodeResponse(rsp, &user) || user.Object != "user" || user.ID != target.Subject || user.UpdatedAt <= 0 || user.UpdatedAt > time.Now().Add(5*time.Minute).UnixMilli() || user.Banned == nil || user.Emails == nil {
		return Profile{}, ErrUnavailable
	}
	profile := Profile{Subject: user.ID, UpdatedAt: user.UpdatedAt, Blocked: *user.Banned || user.Deprovisioned}
	seen := map[string]bool{}
	for _, email := range user.Emails {
		if email.ID == "" || seen[email.ID] {
			return Profile{}, ErrUnavailable
		}
		seen[email.ID] = true
		if user.Primary == nil || email.ID != *user.Primary || email.Verification.Status != "verified" {
			continue
		}
		if !validProfileEmail(email.Address) {
			return Profile{}, ErrUnavailable
		}
		if !profile.Blocked {
			profile.Email = email.Address
		}
	}
	return profile, nil
}
