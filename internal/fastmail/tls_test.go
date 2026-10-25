package fastmail

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

func syntheticCertificate(t *testing.T, hostname string, expired bool) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	if expired {
		until = time.Now().Add(-time.Hour)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{hostname},
		NotBefore: time.Now().Add(-2 * time.Hour), NotAfter: until,
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey}, roots
}

func localTLSClient(t *testing.T, hostname string, expired, trust, requireClientCert bool) (*Client, <-chan struct{}) {
	t.Helper()
	certificate, roots := syntheticCertificate(t, hostname, expired)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	serverConfig := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	if requireClientCert {
		serverConfig.ClientAuth = tls.RequireAnyClientCert
		serverConfig.MaxVersion = tls.VersionTLS12
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		server := tls.Server(conn, serverConfig)
		defer server.Close()
		server.SetDeadline(time.Now().Add(2 * time.Second))
		if err := server.Handshake(); err != nil {
			return
		}
		tag := untilCommand(t, server, "LIST")
		if tag != "" {
			fmt.Fprintf(server, "* LIST () NIL \"INBOX\"\r\n%s OK listed\r\n", tag)
		}
		io.Copy(io.Discard, server)
	}()
	c := NewClient()
	c.dial = func(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
		if address != "imap.fastmail.com:993" || config.ServerName != "imap.fastmail.com" || config.InsecureSkipVerify {
			t.Error("unsafe TLS endpoint")
		}
		config = config.Clone()
		if trust {
			config.RootCAs = roots
		} else {
			config.RootCAs = x509.NewCertPool()
		}
		return dialTLS(ctx, listener.Addr().String(), config)
	}
	return c, done
}

func TestCheckRealTLSVerifiesCertificates(t *testing.T) {
	for _, entry := range []struct {
		name, hostname             string
		expired, trust, clientCert bool
		code                       string
	}{
		{"valid", "imap.fastmail.com", false, true, false, ""},
		{"hostname", "synthetic-private-password.test", false, true, false, "tls_failed"},
		{"expired", "imap.fastmail.com", true, true, false, "tls_failed"},
		{"untrusted", "imap.fastmail.com", false, false, false, "tls_failed"},
		{"handshake-alert", "imap.fastmail.com", false, true, true, "tls_failed"},
	} {
		t.Run(entry.name, func(t *testing.T) {
			c, done := localTLSClient(t, entry.hostname, entry.expired, entry.trust, entry.clientCert)
			folders, err := c.Check(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"))
			waitClosed(t, done)
			if adapterCode(err) != entry.code {
				t.Fatal("unexpected TLS result", err, adapterCode(err), entry.code)
			}
			if err != nil && strings.Contains(err.Error(), "synthetic-private") {
				t.Fatal("TLS error exposed peer details")
			}
			if err == nil && (len(folders) != 1 || folders[0].Name != "INBOX") {
				t.Fatal(folders)
			}
		})
	}
}
