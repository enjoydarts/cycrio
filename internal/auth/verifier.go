package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/jwt"
)

var (
	ErrInvalidToken = errors.New("invalid session token")
	ErrUnavailable  = errors.New("session verification unavailable")
)

type Identity struct {
	Issuer    string
	Subject   string
	SessionID string
}

type Verifier interface {
	Verify(context.Context, string) (Identity, error)
}

// ClerkVerifier verifies session tokens using only the configured issuer's keys.
// It does not require or use a Clerk secret API key.
type ClerkVerifier struct {
	issuer  string
	origins map[string]struct{}
	client  *http.Client
	jwksURL string
	now     func() time.Time

	mu        sync.Mutex
	keys      map[string]*clerk.JSONWebKey
	expiresAt time.Time
	nextFetch time.Time
	fetchErr  error
	inflight  chan struct{}
}

func NewVerifier(issuer string, allowedOrigins []string, client *http.Client) *ClerkVerifier {
	if client == nil {
		client = http.DefaultClient
	}
	boundedClient := *client
	if boundedClient.Timeout <= 0 || boundedClient.Timeout > jwksTimeout {
		boundedClient.Timeout = jwksTimeout
	}
	boundedClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		if origin != "" {
			origins[origin] = struct{}{}
		}
	}
	v := &ClerkVerifier{
		issuer: issuer, origins: origins, client: &boundedClient, now: time.Now,
	}
	// Bootstrap validates configuration. Fail closed as an additional guard if
	// this constructor is called directly with a non-HTTPS or non-origin issuer.
	if u, err := url.Parse(issuer); err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/") {
		v.jwksURL = strings.TrimSuffix(issuer, "/") + "/.well-known/jwks.json"
	}
	return v
}

func (v *ClerkVerifier) Verify(ctx context.Context, token string) (Identity, error) {
	if ctx.Err() != nil {
		return Identity{}, ErrUnavailable
	}
	// Read only the key selector before verification. Neither token issuer nor
	// URL-bearing headers participate in constructing the JWKS request URL.
	if len(token) > 64<<10 {
		return Identity{}, ErrInvalidToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Identity{}, ErrInvalidToken
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Identity{}, ErrInvalidToken
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	if json.Unmarshal(headerBytes, &header) != nil || header.Algorithm != "RS256" || header.KeyID == "" {
		return Identity{}, ErrInvalidToken
	}
	key, err := v.key(ctx, header.KeyID)
	if err != nil {
		return Identity{}, err
	}
	claims, err := jwt.Verify(ctx, &jwt.VerifyParams{
		Token: token, JWK: key, ProxyURL: &v.issuer,
		AuthorizedPartyHandler: func(origin string) bool {
			_, allowed := v.origins[origin]
			return allowed
		},
	})
	if ctx.Err() != nil {
		return Identity{}, ErrUnavailable
	}
	if err != nil || claims == nil {
		return Identity{}, ErrInvalidToken
	}
	if claims.Issuer != v.issuer || claims.Expiry == nil || *claims.Expiry <= time.Now().Unix() || strings.TrimSpace(claims.Subject) == "" || strings.TrimSpace(claims.SessionID) == "" || claims.AuthorizedParty == "" {
		return Identity{}, ErrInvalidToken
	}
	return Identity{Issuer: claims.Issuer, Subject: claims.Subject, SessionID: claims.SessionID}, nil
}
