// Package productiondeploy pins a CI-tested release and waits for Coolify.
package productiondeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

type Config struct {
	SHA, Repository, GitHubURL, GitHubToken       string
	CoolifyURL, CoolifyToken, Application, APIURL string
	PollInterval                                  time.Duration
}

type Runner struct {
	Config Config
	Out    io.Writer
}

// Deploy returns false when a newer main commit has superseded this CI run.
// Once started, a release finishes under the workflow's production concurrency lock.
func (r Runner) Deploy(ctx context.Context) (bool, error) {
	c := r.Config
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(c.SHA) ||
		!regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(c.Repository) ||
		!regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(c.Application) ||
		c.GitHubToken == "" || c.CoolifyToken == "" {
		return false, errors.New("invalid production deployment configuration")
	}
	if r.Out == nil {
		r.Out = io.Discard
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 10 * time.Second
	}
	// Refuse redirects rather than forwarding service credentials to another URL.
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var head struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := request(ctx, client, "GET", c.GitHubURL+"/repos/"+c.Repository+"/git/ref/heads/main", c.GitHubToken, nil, &head); err != nil {
		return false, err
	}
	if head.Object.SHA == "" {
		return false, errors.New("GitHub returned no main commit")
	}
	if head.Object.SHA != c.SHA {
		fmt.Fprintln(r.Out, "A newer main commit exists; skipping this release.")
		return false, nil
	}
	appURL := c.CoolifyURL + "/applications/" + c.Application
	type application struct {
		Status   string `json:"status"`
		Branch   string `json:"git_branch"`
		SHA      string `json:"git_commit_sha"`
		Settings *struct {
			Auto bool `json:"is_auto_deploy_enabled"`
		} `json:"settings"`
	}
	var app application
	if err := request(ctx, client, "GET", appURL, c.CoolifyToken, nil, &app); err != nil {
		return false, err
	}
	if app.Branch != "main" || app.Settings == nil || app.Settings.Auto {
		return false, errors.New("Coolify must use main with native auto deploy disabled")
	}
	// Coolify otherwise marks Compose deployments finished after `up -d`,
	// before its asynchronous status collector has checked the new containers.
	if err := request(ctx, client, "PATCH", appURL, c.CoolifyToken, map[string]string{
		"git_commit_sha":                      c.SHA,
		"docker_compose_custom_start_command": "docker compose --project-name " + c.Application + " up -d --wait --wait-timeout 300",
	}, nil); err != nil {
		return false, err
	}
	var queued struct {
		Deployments []struct {
			Resource string `json:"resource_uuid"`
			UUID     string `json:"deployment_uuid"`
		} `json:"deployments"`
	}
	if err := request(ctx, client, "POST", c.CoolifyURL+"/deploy", c.CoolifyToken, map[string]any{"uuid": c.Application, "force": false}, &queued); err != nil {
		return false, err
	}
	if len(queued.Deployments) != 1 || queued.Deployments[0].Resource != c.Application || !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(queued.Deployments[0].UUID) {
		return false, errors.New("Coolify returned an unexpected deployment")
	}
	uuid := queued.Deployments[0].UUID
	fmt.Fprintln(r.Out, "Coolify deployment:", uuid, "commit:", c.SHA)
	previous := ""
	for {
		var deployment struct {
			Status string `json:"status"`
			Commit string `json:"commit"`
		}
		if err := request(ctx, client, "GET", c.CoolifyURL+"/deployments/"+uuid, c.CoolifyToken, nil, &deployment); err != nil {
			return false, err
		}
		if deployment.Status != previous {
			// Only emit recognized states; provider responses/logs may contain secrets.
			switch deployment.Status {
			case "queued", "in_progress", "finished", "failed", "cancelled", "canceled":
				fmt.Fprintln(r.Out, "Deployment status:", deployment.Status)
			}
			previous = deployment.Status
		}
		switch deployment.Status {
		case "failed", "cancelled", "canceled":
			return false, errors.New("Coolify deployment failed or was cancelled; inspect private Coolify logs")
		case "finished":
			if deployment.Commit != c.SHA {
				return false, errors.New("Coolify deployed a different commit")
			}
			if err := request(ctx, client, "GET", appURL, c.CoolifyToken, nil, &app); err != nil {
				return false, err
			}
			if app.SHA != c.SHA {
				return false, errors.New("Coolify release pin changed during deployment")
			}
			if app.Status == "running:healthy" {
				if err := request(ctx, client, "GET", c.APIURL+"/readyz", "", nil, nil); err == nil {
					fmt.Fprintln(r.Out, "Migration, API and Worker deployment completed; API is ready.")
					return true, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return false, errors.New("production deployment timed out; inspect Coolify before retrying")
		case <-time.After(c.PollInterval):
		}
	}
}

func request(ctx context.Context, client *http.Client, method, url, token string, body, result any) error {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid deployment request")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return errors.New("deployment API request failed or timed out")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("deployment API returned HTTP %d", response.StatusCode)
	}
	if result != nil {
		if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(result); err != nil {
			return errors.New("deployment API returned invalid JSON")
		}
	}
	return nil
}
