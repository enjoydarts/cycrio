package productiondeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testedSHA = "1111111111111111111111111111111111111111"

func TestDeploy(t *testing.T) {
	for _, tc := range []struct {
		name, head, status, commit      string
		auto, unhealthy, stale, wantErr bool
		readyFailures                   int
	}{
		{name: "tested commit reaches healthy", head: testedSHA, status: "finished", commit: testedSHA},
		{name: "old CI cannot overwrite main", head: strings.Repeat("2", 40), stale: true},
		{name: "failed migration stops deployment", head: testedSHA, status: "failed", wantErr: true},
		{name: "cancelled deployment fails", head: testedSHA, status: "cancelled", wantErr: true},
		{name: "wrong deployed commit fails", head: testedSHA, status: "finished", commit: strings.Repeat("2", 40), wantErr: true},
		{name: "native auto deploy cannot bypass CI", head: testedSHA, auto: true, wantErr: true},
		{name: "unhealthy service times out", head: testedSHA, status: "finished", commit: testedSHA, unhealthy: true, wantErr: true},
		{name: "public readiness recovers during startup", head: testedSHA, status: "finished", commit: testedSHA, readyFailures: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var paths []string
			var pinned string
			readyCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "GET /repos/owner/repo/git/ref/heads/main":
					_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": tc.head}})
				case "GET /applications/app":
					status := "running:healthy"
					if tc.unhealthy {
						status = "running:unhealthy"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "git_branch": "main", "git_commit_sha": pinned, "settings": map[string]bool{"is_auto_deploy_enabled": tc.auto}})
				case "PATCH /applications/app":
					var body struct {
						SHA   string `json:"git_commit_sha"`
						Start string `json:"docker_compose_custom_start_command"`
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					pinned = body.SHA
					if body.Start != "docker compose up -d --wait --wait-timeout 300" {
						t.Error("new release must wait for both container healthchecks")
					}
					_, _ = w.Write([]byte(`{}`))
				case "POST /deploy":
					if pinned != testedSHA {
						t.Error("deployment was not pinned to tested commit")
					}
					_, _ = w.Write([]byte(`{"deployments":[{"resource_uuid":"app","deployment_uuid":"release"}]}`))
				case "GET /deployments/release":
					_ = json.NewEncoder(w).Encode(map[string]string{"status": tc.status, "commit": tc.commit, "logs": "PRIVATE PROVIDER SECRET"})
				case "GET /readyz":
					readyCalls++
					if readyCalls <= tc.readyFailures {
						w.WriteHeader(503)
						return
					}
					_, _ = w.Write([]byte(`{"status":"ready"}`))
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			var output bytes.Buffer
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			deployed, err := (Runner{Config: Config{SHA: testedSHA, Repository: "owner/repo", GitHubURL: server.URL, GitHubToken: "github-secret", CoolifyURL: server.URL, CoolifyToken: "coolify-secret", Application: "app", APIURL: server.URL, PollInterval: time.Millisecond}, Out: &output}).Deploy(ctx)
			if (err != nil) != tc.wantErr {
				t.Fatalf("deployed=%v err=%v", deployed, err)
			}
			if tc.stale && (deployed || len(paths) != 1) {
				t.Fatalf("stale CI mutated production: %v", paths)
			}
			if !tc.wantErr && !tc.stale && !deployed {
				t.Fatal("successful deployment skipped")
			}
			if strings.Contains(output.String(), "SECRET") {
				t.Fatal("deployment logs leaked")
			}
		})
	}
}

func TestHTTPFailureDoesNotExposeBodyOrFollowRedirect(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/stolen")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("PRIVATE SECRET"))
			}))
			defer server.Close()
			_, err := (Runner{Config: Config{SHA: testedSHA, Repository: "owner/repo", GitHubURL: server.URL, GitHubToken: "token", CoolifyURL: server.URL, CoolifyToken: "token", Application: "app", APIURL: server.URL}, Out: &bytes.Buffer{}}).Deploy(context.Background())
			if err == nil || strings.Contains(err.Error(), "SECRET") || calls != 1 {
				t.Fatalf("unsafe HTTP failure: calls=%d err=%v", calls, err)
			}
		})
	}
}
