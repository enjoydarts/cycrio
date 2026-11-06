//go:build integration

package database

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/healthchecks"
	"github.com/enjoydarts/cycrio/internal/secretstore"
)

type monitoringTestTransport func(*http.Request) (*http.Response, error)

func (f monitoringTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHealthchecksRealHTTPPostRequiresHealthyAPIAndWorker(t *testing.T) {
	identity, pool := testStore(t)
	ctx := context.Background()
	actor, e := identity.AllowIdentity(ctx, "https://clerk.test.example", "monitoring_wire_admin", true)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "keys.json")
	if e = secretstore.Initialize(path); e != nil {
		t.Fatal(e)
	}
	keys, e := secretstore.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	secrets := credentials.NewService(NewCredentialRepository(pool), keys)
	var posts, gets atomic.Int64
	var apiFailed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			gets.Add(1)
			if apiFailed.Load() {
				http.Error(w, "not ready", 503)
				return
			}
			_, _ = io.WriteString(w, "ok\n")
			return
		}
		posts.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || len(body) != 0 || r.Header.Get("Authorization") != "" {
			t.Error("unexpected ping payload", r.Method, len(body))
		}
		_, _ = io.WriteString(w, "OK")
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	client := &http.Client{Transport: monitoringTestTransport(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		copy := *r.URL
		copy.Scheme, copy.Host = target.Scheme, target.Host
		clone.URL = &copy
		return http.DefaultTransport.RoundTrip(clone)
	})}
	workerFailed := false
	service := healthchecks.NewService(NewHealthchecksRepository(pool), secrets.Resolve, healthchecks.NewClient(client), func(context.Context) error {
		if workerFailed {
			return errors.New("worker unavailable")
		}
		return nil
	}, healthchecks.NewAPIProbe(client))
	if e = service.Tick(ctx); e != nil || posts.Load() != 0 || gets.Load() != 0 {
		t.Fatal("unconfigured monitoring sent requests", e)
	}
	if _, e = secrets.Put(ctx, actor.ID, "healthchecks", 0, "https://hc-ping.com/11111111-1111-4111-8111-111111111111"); e != nil {
		t.Fatal(e)
	}
	if e = service.Tick(ctx); e != nil || posts.Load() != 1 {
		t.Fatal("healthy app did not POST", e, posts.Load())
	}
	if e = service.Tick(ctx); e != nil || posts.Load() != 1 || gets.Load() != 1 {
		t.Fatal("duplicate ping or unnecessary API probe", e, posts.Load(), gets.Load())
	}
	if _, e = pool.Exec(ctx, `UPDATE healthchecks_ping_schedule SET next_attempt_at=clock_timestamp()-interval '1 second'`); e != nil {
		t.Fatal(e)
	}
	apiFailed.Store(true)
	if e = service.Tick(ctx); e == nil || posts.Load() != 1 {
		t.Fatal("unhealthy API sent success", e, posts.Load())
	}
	apiFailed.Store(false)
	workerFailed = true
	if e = service.Tick(ctx); e == nil || posts.Load() != 1 {
		t.Fatal("unhealthy worker sent success", e, posts.Load())
	}
	workerFailed = false
	if e = service.Tick(ctx); e != nil || posts.Load() != 2 {
		t.Fatal("recovery did not POST", e, posts.Load())
	}
}
