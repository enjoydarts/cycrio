package auth

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
)

const (
	jwksTimeout         = 5 * time.Second
	jwksTTL             = 5 * time.Minute
	jwksRefreshInterval = 30 * time.Second
	jwksMaxBytes        = 1 << 20
)

func (v *ClerkVerifier) key(ctx context.Context, keyID string) (*clerk.JSONWebKey, error) {
	for {
		if ctx.Err() != nil {
			return nil, diagnostics.Wrap(ErrUnavailable, ctx.Err())
		}
		v.mu.Lock()
		now := v.now()
		if key := v.keys[keyID]; key != nil && now.Before(v.expiresAt) {
			v.mu.Unlock()
			return key, nil
		}
		if done := v.inflight; done != nil {
			v.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, diagnostics.Wrap(ErrUnavailable, ctx.Err())
			case <-done:
				continue
			}
		}
		// A global cooldown, rather than a per-kid cache, bounds both network
		// requests and memory when untrusted tokens supply arbitrary key IDs.
		if now.Before(v.nextFetch) {
			err := v.fetchErr
			v.mu.Unlock()
			if err != nil {
				return nil, diagnostics.Wrap(ErrUnavailable, err)
			}
			return nil, ErrInvalidToken
		}
		v.inflight = make(chan struct{})
		v.nextFetch = now.Add(jwksRefreshInterval)
		v.mu.Unlock()
		// Shared work is independent of any individual caller's cancellation.
		// The initiator loops back to the same cancellable wait as all callers.
		go v.refreshKeys()
	}
}

func (v *ClerkVerifier) refreshKeys() {
	// fetchKeys applies its own timeout. At most one bounded fetch goroutine
	// exists per verifier, and the global cooldown still applies to failures.
	keys, err := v.fetchKeys(context.Background())
	v.mu.Lock()
	defer v.mu.Unlock()
	if err == nil {
		v.keys = keys
		v.expiresAt = v.now().Add(jwksTTL)
	}
	v.fetchErr = err
	close(v.inflight)
	v.inflight = nil
}

func (v *ClerkVerifier) fetchKeys(ctx context.Context) (map[string]*clerk.JSONWebKey, error) {
	if v.jwksURL == "" {
		return nil, diagnostics.Wrap(ErrUnavailable, fmt.Errorf("JWKS issuer URL is not configured"))
	}
	ctx, cancel := context.WithTimeout(ctx, jwksTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return nil, diagnostics.Wrap(ErrUnavailable, err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := v.client.Do(request)
	if err != nil {
		return nil, diagnostics.Wrap(ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, diagnostics.Wrap(ErrUnavailable, fmt.Errorf("JWKS fetch HTTP %d", response.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, jwksMaxBytes+1))
	if err != nil {
		return nil, diagnostics.Wrap(ErrUnavailable, err)
	}
	if len(body) > jwksMaxBytes {
		return nil, diagnostics.Wrap(ErrUnavailable, fmt.Errorf("JWKS exceeds %d bytes", jwksMaxBytes))
	}
	var keySet clerk.JSONWebKeySet
	if err := json.Unmarshal(body, &keySet); err != nil {
		return nil, diagnostics.Wrap(ErrUnavailable, diagnostics.Snapshot(err, func(text string) string { return diagnostics.ScrubKnown(text, string(body)) }))
	}
	keys := make(map[string]*clerk.JSONWebKey, len(keySet.Keys))
	for _, key := range keySet.Keys {
		if key == nil || key.KeyID == "" || key.Algorithm != "RS256" || (key.Use != "" && key.Use != "sig") {
			continue
		}
		publicKey, ok := key.Key.(*rsa.PublicKey)
		if !ok || publicKey.N == nil || publicKey.N.BitLen() < 2048 || publicKey.E < 3 {
			continue
		}
		if _, duplicate := keys[key.KeyID]; duplicate {
			return nil, diagnostics.Wrap(ErrUnavailable, fmt.Errorf("JWKS contains duplicate key IDs"))
		}
		keys[key.KeyID] = key
	}
	if len(keys) == 0 {
		return nil, diagnostics.Wrap(ErrUnavailable, fmt.Errorf("JWKS contains no supported signing keys"))
	}
	return keys, nil
}
