package fastmail

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/enjoydarts/cycrio/internal/mailsync"
)

func TestIMAPRejectsUnsafeEndpoints(t *testing.T) {
	for _, host := range []string{"", "localhost", "127.0.0.1", "10.0.0.1", "169.254.169.254", "[::1]", "::ffff:8.8.8.8", "mail.test/path", "mail.test:993", "mail.test\r\n", "mail..test"} {
		if _, err := NewIMAPClient(host, 993); err == nil {
			t.Errorf("accepted unsafe host %q", host)
		}
	}
	for _, port := range []int{0, 25, 465, 995, 65536} {
		if _, err := NewIMAPClient("mail.example.test", port); err == nil {
			t.Errorf("accepted port %d", port)
		}
	}
}

func TestIMAPPublicAddresses(t *testing.T) {
	for _, address := range []string{"0.0.0.0", "0.1.2.3", "127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "100.64.0.1", "169.254.169.254", "168.63.129.16", "192.0.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "::", "::1", "::ffff:8.8.8.8", "fc00::1", "fe80::1", "ff02::1", "2001:db8::1", "2001::1", "2002:0808:0808::1", "3ffe::1", "3fff::1", "64:ff9b::a00:1"} {
		if publicIMAPAddress(netip.MustParseAddr(address)) {
			t.Errorf("accepted non-public IP %s", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "2001:4860:4860::8888"} {
		if !publicIMAPAddress(netip.MustParseAddr(address)) {
			t.Errorf("rejected public IP %s", address)
		}
	}
}

func TestIMAPDNSRejectsEveryUnsafeAnswerBeforeDial(t *testing.T) {
	for _, answers := range [][]netip.Addr{nil, {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")}, {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("::ffff:8.8.8.8")}} {
		c, err := NewIMAPClient("mail.example.test", 993)
		if err != nil {
			t.Fatal(err)
		}
		c.lookup = func(context.Context, string) ([]netip.Addr, error) { return answers, nil }
		c.dial = func(context.Context, string, *tls.Config) (net.Conn, error) {
			t.Error("dialed unsafe DNS result")
			return nil, errors.New("dial")
		}
		_, err = c.Check(context.Background(), "user", []byte("secret"))
		if adapterCode(err) != "connection_failed" {
			t.Fatal(err)
		}
	}
}

func TestIMAPPinsResolvedIPAndOriginalTLSNameForAllOperations(t *testing.T) {
	for _, operation := range []string{"check", "discover", "read"} {
		t.Run(operation, func(t *testing.T) {
			c, err := NewIMAPClient("mail.example.test", 993)
			if err != nil {
				t.Fatal(err)
			}
			lookups, dials := 0, 0
			c.lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
				lookups++
				if host != "mail.example.test" {
					t.Error(host)
				}
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			}
			c.dial = func(_ context.Context, address string, config *tls.Config) (net.Conn, error) {
				dials++
				if address != "8.8.8.8:993" || config.ServerName != "mail.example.test" || config.InsecureSkipVerify || config.MinVersion < tls.VersionTLS12 {
					t.Error("unsafe dial", address, config)
				}
				return nil, errors.New("synthetic-private-password")
			}
			switch operation {
			case "check":
				_, err = c.Check(context.Background(), "user", []byte("secret"))
			case "discover":
				_, err = c.Discover(context.Background(), "user", []byte("secret"), mailsync.PageRequest{Folder: "INBOX", Since: "2026-01-01", Before: "2026-02-01", Limit: 10})
			case "read":
				_, err = c.ReadText(context.Background(), "user", []byte("secret"), mailcontent.Source{Folder: "INBOX", UID: 1, UIDValidity: 1, MaxTextBytes: 100})
			}
			if lookups != 1 || dials != 1 || adapterCode(err) != "connection_failed" || strings.Contains(err.Error(), "synthetic-private") {
				t.Fatal(lookups, dials, err)
			}
		})
	}
}

func TestIMAPSTARTTLSNeverAuthenticatesBeforeUpgrade(t *testing.T) {
	for _, mode := range []string{"missing", "rejected", "untrusted", "valid"} {
		t.Run(mode, func(t *testing.T) {
			c, err := NewIMAPClient("mail.example.test", 143)
			if err != nil {
				t.Fatal(err)
			}
			certificate, roots := syntheticCertificate(t, "mail.example.test", false)
			if mode == "valid" {
				c.tlsConfig.RootCAs = roots
			}
			c.lookup = func(context.Context, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			}
			done := make(chan struct{})
			c.dial = func(_ context.Context, address string, config *tls.Config) (net.Conn, error) {
				if address != "8.8.8.8:143" || config != nil {
					t.Error("invalid STARTTLS dial")
				}
				raw, server := net.Pipe()
				go func() {
					defer close(done)
					defer server.Close()
					caps := "IMAP4rev1 STARTTLS"
					if mode == "missing" {
						caps = "IMAP4rev1"
					}
					fmt.Fprintf(server, "* OK [CAPABILITY %s] ready\r\n", caps)
					r := bufio.NewReader(server)
					for {
						line, e := r.ReadString('\n')
						if e != nil {
							return
						}
						fields := strings.Fields(line)
						if len(fields) < 2 {
							return
						}
						switch fields[1] {
						case "CAPABILITY":
							fmt.Fprintf(server, "* CAPABILITY %s\r\n%s OK done\r\n", caps, fields[0])
						case "STARTTLS":
							if mode == "missing" {
								t.Error("STARTTLS without advertisement")
								return
							}
							if mode == "rejected" {
								fmt.Fprintf(server, "%s NO synthetic-private-password\r\n", fields[0])
								continue
							}
							fmt.Fprintf(server, "%s OK upgrade\r\n", fields[0])
							tlsServer := tls.Server(server, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
							if e := tlsServer.Handshake(); e != nil {
								return
							}
							r = bufio.NewReader(tlsServer)
							for {
								line, e := r.ReadString('\n')
								if e != nil {
									return
								}
								f := strings.Fields(line)
								if len(f) < 2 {
									return
								}
								switch f[1] {
								case "CAPABILITY":
									fmt.Fprintf(tlsServer, "* CAPABILITY IMAP4rev1\r\n%s OK done\r\n", f[0])
								case "LOGIN":
									fmt.Fprintf(tlsServer, "%s OK authenticated\r\n", f[0])
								case "LIST":
									fmt.Fprintf(tlsServer, "* LIST () NIL \"INBOX\"\r\n%s OK done\r\n", f[0])
								default:
									t.Error("unexpected encrypted command", f[1])
									return
								}
							}
						default:
							t.Error("plaintext command", fields[1])
							return
						}
					}
				}()
				return raw, nil
			}
			folders, err := c.Check(context.Background(), "user", []byte("secret"))
			waitClosed(t, done)
			if mode == "valid" {
				if err != nil || len(folders) != 1 {
					t.Fatal(folders, err)
				}
			} else if adapterCode(err) != "tls_failed" || strings.Contains(err.Error(), "synthetic-private") {
				t.Fatal(err)
			}
		})
	}
}

func TestIMAPSTARTTLSRejectsPreauthenticationForAllOperations(t *testing.T) {
	for _, operation := range []string{"check", "discover", "read"} {
		t.Run(operation, func(t *testing.T) {
			c, err := NewIMAPClient("mail.example.test", 143)
			if err != nil {
				t.Fatal(err)
			}
			c.lookup = func(context.Context, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			}
			done := make(chan struct{})
			c.dial = func(context.Context, string, *tls.Config) (net.Conn, error) {
				client, server := net.Pipe()
				go func() {
					defer close(done)
					defer server.Close()
					fmt.Fprint(server, "* PREAUTH synthetic-private-password\r\n")
					buffer := make([]byte, 1)
					if n, _ := server.Read(buffer); n != 0 {
						t.Error("sent command after plaintext PREAUTH")
					}
				}()
				return client, nil
			}
			switch operation {
			case "check":
				_, err = c.Check(context.Background(), "user", []byte("secret"))
			case "discover":
				_, err = c.Discover(context.Background(), "user", []byte("secret"), mailsync.PageRequest{Folder: "INBOX", Since: "2026-01-01", Before: "2026-02-01", Limit: 10})
			case "read":
				_, err = c.ReadText(context.Background(), "user", []byte("secret"), mailcontent.Source{Folder: "INBOX", UID: 1, UIDValidity: 1, MaxTextBytes: 100})
			}
			waitClosed(t, done)
			if adapterCode(err) != "tls_failed" || strings.Contains(err.Error(), "synthetic-private") {
				t.Fatal(err)
			}
		})
	}
}

func TestIMAPSTARTTLSCancellationClosesBlockedPrelude(t *testing.T) {
	c, err := NewIMAPClient("mail.example.test", 143)
	if err != nil {
		t.Fatal(err)
	}
	c.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.dial = func(context.Context, string, *tls.Config) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer close(done)
			defer server.Close()
			server.SetDeadline(time.Now().Add(2 * time.Second))
			fmt.Fprint(server, "* OK ready\r\n")
			if _, err := bufio.NewReader(server).ReadString('\n'); err != nil {
				t.Error(err)
				return
			}
			cancel()
			io.Copy(io.Discard, server)
		}()
		return client, nil
	}
	_, err = c.Check(ctx, "user", []byte("secret"))
	waitClosed(t, done)
	if adapterCode(err) != "cancelled" {
		t.Fatal(err)
	}
}

func TestIMAPSTARTTLSBoundsUnterminatedGreeting(t *testing.T) {
	c, err := NewIMAPClient("mail.example.test", 143)
	if err != nil {
		t.Fatal(err)
	}
	c.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	done := make(chan struct{})
	c.dial = func(context.Context, string, *tls.Config) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer close(done)
			defer server.Close()
			fmt.Fprint(server, "* OK "+strings.Repeat("x", 70*1024))
			buffer := make([]byte, 1)
			if n, _ := server.Read(buffer); n != 0 {
				t.Error("sent command after oversized greeting")
			}
		}()
		return client, nil
	}
	_, err = c.Check(context.Background(), "user", []byte("secret"))
	waitClosed(t, done)
	if adapterCode(err) != "tls_failed" {
		t.Fatal(err)
	}
}
