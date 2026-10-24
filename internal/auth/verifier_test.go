package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testOrigin = "https://app.example.test"

type issuerFixture struct {
	server   *httptest.Server
	key      *rsa.PrivateKey
	requests atomic.Int64
	response atomic.Value
}

func newIssuerFixture(t *testing.T) *issuerFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &issuerFixture{key: key}
	f.response.Store(jwksBody(t, "key-1", key))
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		if r.URL.Path != "/.well-known/jwks.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, f.response.Load().(string))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func jwksBody(t *testing.T, kid string, key *rsa.PrivateKey) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"keys": []any{map[string]any{
		"kty": "RSA", "kid": kid, "alg": "RS256", "use": "sig",
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (f *issuerFixture) claims() map[string]any {
	return map[string]any{"iss": f.server.URL, "sub": "user_123", "sid": "sess_123", "azp": testOrigin, "exp": time.Now().Add(time.Minute).Unix(), "nbf": time.Now().Add(-time.Minute).Unix()}
}

func signedToken(t *testing.T, key *rsa.PrivateKey, kid, alg string, claims map[string]any) string {
	t.Helper()
	return tokenWithHeader(t, key, map[string]any{"alg": alg, "kid": kid, "typ": "JWT"}, claims)
}

func tokenWithHeader(t *testing.T, key *rsa.PrivateKey, headers, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(headers)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestVerifierAcceptsSignedSessionAndCachesKeys(t *testing.T) {
	f := newIssuerFixture(t)
	v := NewVerifier(f.server.URL, []string{testOrigin}, f.server.Client())
	token := signedToken(t, f.key, "key-1", "RS256", f.claims())
	for range 2 {
		identity, err := v.Verify(context.Background(), token)
		if err != nil {
			t.Fatalf("Verify() error = %v", err)
		}
		if identity != (Identity{Issuer: f.server.URL, Subject: "user_123", SessionID: "sess_123"}) {
			t.Fatalf("unexpected identity: %#v", identity)
		}
	}
	if got := f.requests.Load(); got != 1 {
		t.Fatalf("JWKS requests = %d, want 1", got)
	}
}

func TestVerifierRejectsInvalidSessions(t *testing.T) {
	f := newIssuerFixture(t)
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	v := NewVerifier(f.server.URL, []string{testOrigin}, f.server.Client())
	tests := []struct {
		name   string
		modify func(map[string]any)
		alg    string
		key    *rsa.PrivateKey
	}{
		{name: "wrong issuer", modify: func(c map[string]any) { c["iss"] = "https://evil.clerk.accounts.dev" }},
		{name: "wrong origin", modify: func(c map[string]any) { c["azp"] = "https://app.example.test.evil" }},
		{name: "expired", modify: func(c map[string]any) { c["exp"] = time.Now().Add(-time.Second).Unix() }},
		{name: "expiry boundary", modify: func(c map[string]any) { c["exp"] = time.Now().Unix() }},
		{name: "future nbf", modify: func(c map[string]any) { c["nbf"] = time.Now().Add(time.Minute).Unix() }},
		{name: "future iat", modify: func(c map[string]any) { c["iat"] = time.Now().Add(time.Minute).Unix() }},
		{name: "wrong signature", key: wrongKey},
		{name: "wrong algorithm", alg: "HS256"},
	}
	for _, field := range []string{"iss", "sub", "sid", "azp", "exp"} {
		field := field
		tests = append(tests, struct {
			name   string
			modify func(map[string]any)
			alg    string
			key    *rsa.PrivateKey
		}{name: "missing " + field, modify: func(c map[string]any) { delete(c, field) }})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := f.claims()
			if tt.modify != nil {
				tt.modify(claims)
			}
			key := tt.key
			if key == nil {
				key = f.key
			}
			alg := tt.alg
			if alg == "" {
				alg = "RS256"
			}
			identity, err := v.Verify(context.Background(), signedToken(t, key, "key-1", alg, claims))
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("Verify() = %#v, %v; want invalid token", identity, err)
			}
			if identity != (Identity{}) {
				t.Fatalf("invalid token exposed identity: %#v", identity)
			}
			if err.Error() != "invalid session token" {
				t.Fatalf("error exposed details: %v", err)
			}
		})
	}
	for _, token := range []string{"", "not-a-token", "e30.e30.", strings.Repeat("a", 65537)} {
		if _, err := v.Verify(context.Background(), token); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("malformed token error = %v", err)
		}
	}
}

func TestVerifierLimitsUnknownKeyRefreshAndSupportsRotation(t *testing.T) {
	f := newIssuerFixture(t)
	v := NewVerifier(f.server.URL, []string{testOrigin}, f.server.Client())
	now := time.Now()
	v.now = func() time.Time { return now }
	if _, err := v.Verify(context.Background(), signedToken(t, f.key, "key-1", "RS256", f.claims())); err != nil {
		t.Fatal(err)
	}
	f.response.Store(jwksBody(t, "key-2", f.key))
	for i := range 20 {
		if _, err := v.Verify(context.Background(), signedToken(t, f.key, fmt.Sprintf("unknown-%d", i), "RS256", f.claims())); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("unknown key error = %v", err)
		}
	}
	if got := f.requests.Load(); got != 1 {
		t.Fatalf("unknown keys forced %d requests", got)
	}
	now = now.Add(31 * time.Second)
	if _, err := v.Verify(context.Background(), signedToken(t, f.key, "key-2", "RS256", f.claims())); err != nil {
		t.Fatalf("rotated key error = %v", err)
	}
	if got := f.requests.Load(); got != 2 {
		t.Fatalf("rotation requests = %d, want 2", got)
	}
	if _, err := v.Verify(context.Background(), signedToken(t, f.key, "key-1", "RS256", f.claims())); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("removed key error = %v", err)
	}
	now = now.Add(5 * time.Minute)
	if _, err := v.Verify(context.Background(), signedToken(t, f.key, "key-2", "RS256", f.claims())); err != nil {
		t.Fatalf("refresh expired cache = %v", err)
	}
	if got := f.requests.Load(); got != 3 {
		t.Fatalf("expiry requests = %d, want 3", got)
	}
}

func TestVerifierFailsClosedAndThrottlesJWKSFailures(t *testing.T) {
	f := newIssuerFixture(t)
	v := NewVerifier(f.server.URL, []string{testOrigin}, f.server.Client())
	now := time.Now()
	v.now = func() time.Time { return now }
	token := signedToken(t, f.key, "key-1", "RS256", f.claims())
	if _, err := v.Verify(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Minute)
	f.response.Store("invalid upstream data with secret details")
	for range 3 {
		if identity, err := v.Verify(context.Background(), token); !errors.Is(err, ErrUnavailable) || identity != (Identity{}) {
			t.Fatalf("JWKS outage = %#v, %v", identity, err)
		}
	}
	if got := f.requests.Load(); got != 2 {
		t.Fatalf("outage requests = %d, want 2", got)
	}
	now = now.Add(31 * time.Second)
	f.response.Store(jwksBody(t, "key-1", f.key))
	if _, err := v.Verify(context.Background(), token); err != nil {
		t.Fatalf("recovery = %v", err)
	}
}

func TestVerifierRejectsBadJWKSResponses(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{"status", http.StatusServiceUnavailable, "upstream secret"},
		{"malformed", http.StatusOK, "invalid json"},
		{"empty keys", http.StatusOK, `{"keys":[]}`},
		{"oversized", http.StatusOK, strings.Repeat(" ", (1<<20)+1)},
		{"trailing garbage", http.StatusOK, `{"keys":[]}secret`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newIssuerFixture(t)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tt.status); fmt.Fprint(w, tt.body) }))
			defer server.Close()
			claims := f.claims()
			claims["iss"] = server.URL
			_, err := NewVerifier(server.URL, []string{testOrigin}, server.Client()).Verify(context.Background(), signedToken(t, f.key, "key-1", "RS256", claims))
			if !errors.Is(err, ErrUnavailable) || err.Error() != "session verification unavailable" {
				t.Fatalf("JWKS error = %v", err)
			}
		})
	}
}

func TestVerifierRefusesRedirectsWithoutMutatingClient(t *testing.T) {
	f := newIssuerFixture(t)
	var redirected atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			redirected.Add(1)
			fmt.Fprint(w, f.response.Load().(string))
			return
		}
		http.Redirect(w, r, "/target", http.StatusFound)
	}))
	defer server.Close()
	client := server.Client()
	claims := f.claims()
	claims["iss"] = server.URL
	_, err := NewVerifier(server.URL, []string{testOrigin}, client).Verify(context.Background(), signedToken(t, f.key, "key-1", "RS256", claims))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("redirect error = %v", err)
	}
	if redirected.Load() != 0 {
		t.Fatal("JWKS redirect followed")
	}
	if client.CheckRedirect != nil || client.Timeout != 0 {
		t.Fatal("caller client mutated")
	}
}

func TestVerifierHonorsTimeoutAndCancellation(t *testing.T) {
	f := newIssuerFixture(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	claims := f.claims()
	claims["iss"] = server.URL
	token := signedToken(t, f.key, "key-1", "RS256", claims)
	client := server.Client()
	client.Timeout = 30 * time.Millisecond
	v := NewVerifier(server.URL, []string{testOrigin}, client)
	start := time.Now()
	if _, err := v.Verify(context.Background(), token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("timeout error = %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("JWKS timeout exceeded bound")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := v.Verify(ctx, token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("cancellation error = %v", err)
	}
	// Cancellation must be honored even when a key is already cached.
	cached := NewVerifier(f.server.URL, []string{testOrigin}, f.server.Client())
	validToken := signedToken(t, f.key, "key-1", "RS256", f.claims())
	if _, err := cached.Verify(context.Background(), validToken); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.Verify(ctx, validToken); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("cached cancellation = %v", err)
	}
}

func TestVerifierCoalescesConcurrentKeyFetches(t *testing.T) {
	f := newIssuerFixture(t)
	v := NewVerifier(f.server.URL, []string{testOrigin}, f.server.Client())
	token := signedToken(t, f.key, "key-1", "RS256", f.claims())
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := v.Verify(context.Background(), token); err != nil {
				t.Errorf("concurrent Verify() = %v", err)
			}
		})
	}
	wg.Wait()
	if got := f.requests.Load(); got != 1 {
		t.Fatalf("concurrent JWKS requests = %d, want 1", got)
	}
}

func TestVerifierReportsNetworkFailure(t *testing.T) {
	f := newIssuerFixture(t)
	f.server.Close()
	_, err := NewVerifier(f.server.URL, []string{testOrigin}, f.server.Client()).Verify(context.Background(), signedToken(t, f.key, "key-1", "RS256", f.claims()))
	if !errors.Is(err, ErrUnavailable) || err.Error() != "session verification unavailable" {
		t.Fatalf("network error = %v", err)
	}
}

func TestVerifierNeverFetchesTokenSuppliedURLs(t *testing.T) {
	f := newIssuerFixture(t)
	var attackerRequests atomic.Int64
	attacker := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerRequests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer attacker.Close()
	v := NewVerifier(f.server.URL, []string{testOrigin}, f.server.Client())
	headers := map[string]any{"alg": "RS256", "kid": "key-1", "jku": attacker.URL + "/jwks", "x5u": attacker.URL + "/certificate"}
	if _, err := v.Verify(context.Background(), tokenWithHeader(t, f.key, headers, f.claims())); err != nil {
		t.Fatalf("valid token with untrusted URL headers = %v", err)
	}
	claims := f.claims()
	claims["iss"] = attacker.URL
	if _, err := v.Verify(context.Background(), tokenWithHeader(t, f.key, headers, claims)); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("untrusted issuer = %v", err)
	}
	if attackerRequests.Load() != 0 {
		t.Fatal("token supplied URL was fetched")
	}
	if f.requests.Load() != 1 {
		t.Fatal("configured issuer JWKS not reused")
	}
}

func TestVerifierWaitingForSharedFetchHonorsCancellation(t *testing.T) {
	f := newIssuerFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		fmt.Fprint(w, f.response.Load().(string))
	}))
	defer server.Close()
	claims := f.claims()
	claims["iss"] = server.URL
	token := signedToken(t, f.key, "key-1", "RS256", claims)
	v := NewVerifier(server.URL, []string{testOrigin}, server.Client())
	first := make(chan error, 1)
	go func() { _, err := v.Verify(context.Background(), token); first <- err }()
	defer func() {
		close(release)
		if err := <-first; err != nil {
			t.Errorf("shared fetch = %v", err)
		}
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := v.Verify(ctx, token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("waiting cancellation = %v", err)
	}
	// Cancellation of the waiting caller must not cancel the shared fetch.
}

func TestVerifierRejectsMissingKeyIDBeforeFetching(t *testing.T) {
	f := newIssuerFixture(t)
	token := signedToken(t, f.key, "", "RS256", f.claims())
	if _, err := NewVerifier(f.server.URL, []string{testOrigin}, f.server.Client()).Verify(context.Background(), token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("missing kid = %v", err)
	}
	if f.requests.Load() != 0 {
		t.Fatal("missing kid triggered JWKS request")
	}
}

func TestVerifierInitiatingCancellationDoesNotPoisonSharedFetch(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired_cache_%t", expired), func(t *testing.T) {
			f := newIssuerFixture(t)
			started := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			var block atomic.Bool
			var requests atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if block.Load() {
					close(started)
					<-release
				}
				fmt.Fprint(w, f.response.Load().(string))
			}))
			defer server.Close()
			defer releaseOnce.Do(func() { close(release) })
			claims := f.claims()
			claims["iss"] = server.URL
			token := signedToken(t, f.key, "key-1", "RS256", claims)
			v := NewVerifier(server.URL, []string{testOrigin}, server.Client())
			now := time.Now()
			v.now = func() time.Time { return now }
			if expired {
				if _, err := v.Verify(context.Background(), token); err != nil {
					t.Fatal(err)
				}
				now = now.Add(5 * time.Minute)
			}
			block.Store(true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			first := make(chan error, 1)
			go func() { _, err := v.Verify(ctx, token); first <- err }()
			<-started
			cancel()
			select {
			case err := <-first:
				if !errors.Is(err, ErrUnavailable) {
					t.Fatalf("canceled initiator = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled initiator waited for shared JWKS response")
			}
			second := make(chan error, 1)
			go func() { _, err := v.Verify(context.Background(), token); second <- err }()
			releaseOnce.Do(func() { close(release) })
			select {
			case err := <-second:
				if err != nil {
					t.Fatalf("uncanceled caller affected by initiator cancellation: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("uncanceled caller did not receive shared JWKS result")
			}
			wantRequests := int64(1)
			if expired {
				wantRequests++
			}
			if got := requests.Load(); got != wantRequests {
				t.Fatalf("JWKS requests = %d, want %d", got, wantRequests)
			}
		})
	}
}
