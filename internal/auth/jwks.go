package auth

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
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
			return nil, ErrUnavailable
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
				return nil, ErrUnavailable
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
				return nil, ErrUnavailable
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
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, jwksTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	request.Header.Set("Accept", "application/json")
	response, err := v.client.Do(request)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, jwksMaxBytes+1))
	if err != nil || len(body) > jwksMaxBytes {
		return nil, ErrUnavailable
	}
	var keySet clerk.JSONWebKeySet
	if json.Unmarshal(body, &keySet) != nil {
		return nil, ErrUnavailable
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
			return nil, ErrUnavailable
		}
		keys[key.KeyID] = key
	}
	if len(keys) == 0 {
		return nil, ErrUnavailable
	}
	return keys, nil
}
