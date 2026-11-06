// Package telemetry sends deliberately limited production error metadata.
package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
)

type Config struct {
	ProjectToken string `json:"project_token"`
	Host         string `json:"host"`
}

var projectTokenPattern = regexp.MustCompile(`^phc_[A-Za-z0-9_-]{8,}$`)

func (c Config) valid() bool {
	return len(c.ProjectToken) <= 1024 && projectTokenPattern.MatchString(c.ProjectToken) && (c.Host == "https://us.i.posthog.com" || c.Host == "https://eu.i.posthog.com")
}

// ParseConfig accepts only project ingestion credentials and supported cloud regions.
// Validation errors deliberately never include the submitted credential.
func ParseConfig(data []byte) (Config, error) {
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return Config{}, errors.New("invalid PostHog configuration")
	}
	seen := make(map[string]bool, 2)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return Config{}, errors.New("invalid PostHog configuration")
		}
		name, ok := key.(string)
		if !ok || seen[name] || (name != "project_token" && name != "host") {
			return Config{}, errors.New("invalid PostHog configuration")
		}
		seen[name] = true
		var value string
		if err := decoder.Decode(&value); err != nil {
			return Config{}, errors.New("invalid PostHog configuration")
		}
		if name == "project_token" {
			config.ProjectToken = value
		} else {
			config.Host = value
		}
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		return Config{}, errors.New("invalid PostHog configuration")
	}
	if err := decoder.Decode(new(any)); err != io.EOF || !config.valid() {
		return Config{}, errors.New("invalid PostHog configuration")
	}
	return config, nil
}
