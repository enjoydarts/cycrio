// deploy is a CI-only operator command; production database secrets stay on the deployment host.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/productiondeploy"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	c := productiondeploy.Config{SHA: os.Getenv("GITHUB_SHA"), Repository: os.Getenv("GITHUB_REPOSITORY"), GitHubURL: "https://api.github.com", GitHubToken: os.Getenv("GITHUB_TOKEN"), CoolifyURL: strings.TrimRight(os.Getenv("COOLIFY_API_URL"), "/"), CoolifyToken: os.Getenv("COOLIFY_API_TOKEN"), Application: os.Getenv("COOLIFY_APPLICATION_UUID"), APIURL: strings.TrimRight(os.Getenv("PRODUCTION_API_URL"), "/")}
	for _, raw := range []string{c.CoolifyURL, c.APIURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("deployment URLs must be HTTPS without credentials, query or fragment")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	deployed, err := (productiondeploy.Runner{Config: c, Out: os.Stdout}).Deploy(ctx)
	if err != nil {
		return err
	}
	if file := os.Getenv("GITHUB_OUTPUT"); file != "" {
		out, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("cannot open GitHub step output")
		}
		_, writeErr := fmt.Fprintf(out, "deployed=%t\n", deployed)
		closeErr := out.Close()
		if writeErr != nil || closeErr != nil {
			return fmt.Errorf("cannot write GitHub step output")
		}
	}
	return nil
}
