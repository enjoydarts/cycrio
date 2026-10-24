// Package bootstrap loads the public configuration required before signing in.
package bootstrap

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var ErrInvalidConfig = errors.New("invalid bootstrap configuration")

type Config struct {
	ClerkPublishableKey string   `json:"clerk_publishable_key"`
	AllowedOrigins      []string `json:"allowed_origins"`
}

func (c Config) Configured() bool { return c.ClerkPublishableKey != "" && len(c.AllowedOrigins) > 0 }

func (c Config) Issuer() string {
	parts := strings.SplitN(c.ClerkPublishableKey, "_", 3)
	if len(parts) != 3 || parts[0] != "pk" || (parts[1] != "test" && parts[1] != "live") {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(parts[2])
	}
	if err != nil || !strings.HasSuffix(string(decoded), "$") {
		return ""
	}
	host := strings.TrimSuffix(string(decoded), "$")
	if !strings.Contains(host, ".") || net.ParseIP(host) != nil || len(host) > 253 {
		return ""
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return ""
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return ""
			}
		}
	}
	return "https://" + host
}

func (c Config) Validate() error {
	if !c.Configured() || c.Issuer() == "" {
		return ErrInvalidConfig
	}
	for _, origin := range c.AllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.ForceQuery {
			return ErrInvalidConfig
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
			return ErrInvalidConfig
		}
	}
	return nil
}

func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{AllowedOrigins: []string{"http://localhost:3100"}}, nil
	}
	if err != nil {
		return Config{}, ErrInvalidConfig
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 64*1024))
	decoder.DisallowUnknownFields()
	var c Config
	if err := decoder.Decode(&c); err != nil {
		return Config{}, ErrInvalidConfig
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Config{}, ErrInvalidConfig
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return ErrInvalidConfig
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("bootstrap directory could not be created")
	}
	f, err := os.CreateTemp(dir, ".bootstrap-*")
	if err != nil {
		return errors.New("bootstrap file could not be created")
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return errors.New("bootstrap file could not be written")
	}
	if err := f.Close(); err != nil {
		return errors.New("bootstrap file could not be closed")
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return errors.New("bootstrap file could not be saved")
	}
	return nil
}
