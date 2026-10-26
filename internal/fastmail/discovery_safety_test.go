package fastmail

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

type discoveryCancelConn struct {
	net.Conn
	cancel context.CancelFunc
}

func (c *discoveryCancelConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if bytes.Contains(p[:n], []byte("[READ-ONLY]")) {
		c.cancel()
	}
	return n, err
}

func TestDiscoveryCancelledEmptySnapshotCannotSucceed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, done, _ := discoveryClient(t, 77, 1, "")
	dial := c.dial
	c.dial = func(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
		conn, err := dial(ctx, address, config)
		if err != nil {
			return nil, err
		}
		return &discoveryCancelConn{conn, cancel}, nil
	}
	_, err := c.Discover(ctx, "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
	waitClosed(t, done)
	if adapterCode(err) != "cancelled" {
		t.Fatal("cancelled empty scan succeeded", err)
	}
}

func TestDiscoveryRejectsDatesOmittedBySDK(t *testing.T) {
	c := NewClient()
	called := false
	c.dial = func(context.Context, string, *tls.Config) (net.Conn, error) {
		called = true
		return nil, fmt.Errorf("must not dial")
	}
	for _, since := range []string{"0000-12-31", "0001-01-01", "1899-12-31"} {
		input := discoveryInput()
		input.Since = since
		start, _ := time.Parse(time.DateOnly, since)
		input.Before = start.AddDate(0, 0, 1).Format(time.DateOnly)
		_, err := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"), input)
		if adapterCode(err) != "invalid_response" || called {
			t.Fatal("unsupported calendar reached network", err)
		}
	}
}

func TestDiscoveryBoundsCompressedRangesAndHidesRawErrors(t *testing.T) {
	for _, tc := range []struct{ response, code string }{
		{"ESEARCH:1:4294967295", "invalid_response"},
		{"ESEARCH:1:*", "invalid_response"},
		{"auth-refused", "authentication_failed"},
		{"synthetic-private-password", "invalid_response"},
	} {
		c, done, _ := discoveryClient(t, 77, 10, tc.response)
		previous := os.Stderr
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stderr = w
		_, checkErr := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
		os.Stderr = previous
		w.Close()
		captured, _ := io.ReadAll(r)
		r.Close()
		waitClosed(t, done)
		if adapterCode(checkErr) != tc.code || strings.Contains(checkErr.Error(), "synthetic-private") || len(captured) != 0 {
			t.Fatal("unsafe discovery error", adapterCode(checkErr))
		}
	}
}

func TestDiscoveryUsesFixedTLSAndCancellationClosesTransport(t *testing.T) {
	c := NewClient()
	closed := make(chan struct{})
	c.dial = func(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
		if address != "imap.fastmail.com:993" || config.ServerName != "imap.fastmail.com" || config.InsecureSkipVerify || config.MinVersion < tls.VersionTLS12 {
			t.Error("unsafe discovery transport")
		}
		client, server := net.Pipe()
		go func() { defer close(closed); defer server.Close(); io.Copy(io.Discard, server) }()
		return client, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err := c.Discover(ctx, "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
	waitClosed(t, closed)
	if adapterCode(err) != "timeout" {
		t.Fatal(err)
	}
}

func TestDiscoveryRejectsPreauthenticatedGreetingWithoutLogin(t *testing.T) {
	c, done := scriptedClient(t, func(server net.Conn) {
		// Supplying capabilities avoids an unrelated SDK capability query.
		fmt.Fprint(server, "* PREAUTH [CAPABILITY IMAP4rev1] synthetic-private-password\r\n")
		b, err := io.ReadAll(server)
		if len(b) != 0 || (err != nil && !strings.Contains(err.Error(), "closed")) {
			t.Error("preauthenticated discovery sent a command")
		}
	})
	_, err := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
	waitClosed(t, done)
	if adapterCode(err) != "invalid_response" {
		t.Fatal(err)
	}
}
