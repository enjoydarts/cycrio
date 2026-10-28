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
	"strconv"
	"strings"
)

// NewIMAPClient connects only to public IMAP servers. Port 993 uses implicit
// TLS; port 143 requires an advertised, successful STARTTLS upgrade. DNS is
// validated on each operation and the selected address is pinned for dialing.
func NewIMAPClient(host string, port int) (*Client, error) {
	if (port != 143 && port != 993) || !validIMAPHost(host) {
		return nil, errors.New("invalid IMAP endpoint")
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	c := &Client{host: host, port: port,
		tlsConfig: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}
	c.lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}
	c.dial = func(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
		if config == nil {
			return (&net.Dialer{}).DialContext(ctx, "tcp", address)
		}
		return dialTLS(ctx, address, config)
	}
	return c, nil
}

func validIMAPHost(host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return publicIMAPAddress(ip)
	}
	host = strings.TrimSuffix(host, ".")
	if len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// Deny special-use and transition networks in addition to local/private IPs.
// In particular, mapped IPv4 and IPv6 transition addresses must never bypass
// the IPv4 policy or route an apparently public connection to an internal IP.
var nonPublicIMAPNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	// Azure's platform virtual IP is reachable as a public-looking address.
	netip.MustParsePrefix("168.63.129.16/32"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3ffe::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

func publicIMAPAddress(ip netip.Addr) bool {
	if !ip.IsValid() || ip.Zone() != "" || ip.Is4In6() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, network := range nonPublicIMAPNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func (c *Client) connect(ctx context.Context) (net.Conn, error) {
	if c.host == "" {
		// Preserve the existing fixed Fastmail endpoint and dial injection.
		return c.dial(ctx, "imap.fastmail.com:993", &tls.Config{ServerName: "imap.fastmail.com", MinVersion: tls.VersionTLS12})
	}
	var addresses []netip.Addr
	if ip, err := netip.ParseAddr(c.host); err == nil {
		addresses = []netip.Addr{ip}
	} else {
		var err error
		addresses, err = c.lookup(ctx, c.host)
		if err != nil {
			return nil, err
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("IMAP endpoint has no public address")
	}
	for _, ip := range addresses {
		if !publicIMAPAddress(ip) {
			return nil, errors.New("IMAP endpoint is not public")
		}
	}
	address := net.JoinHostPort(addresses[0].String(), strconv.Itoa(c.port))
	config := c.tlsConfig.Clone()
	if c.port == 993 {
		return c.dial(ctx, address, config)
	}
	raw, err := c.dial(ctx, address, nil)
	if err != nil {
		return nil, err
	}
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { raw.Close(); close(closed) })
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	conn, err := upgradeIMAPTLS(ctx, raw, config)
	if err != nil {
		raw.Close()
		return nil, &tlsHandshakeError{cause: err}
	}
	return conn, nil
}

type imapReplayConn struct {
	net.Conn
	reader io.Reader
}

func (c *imapReplayConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func upgradeIMAPTLS(ctx context.Context, raw net.Conn, config *tls.Config) (net.Conn, error) {
	deadline, _ := ctx.Deadline()
	wire := &boundedConn{Conn: raw, deadline: deadline, maxBytes: 64 * 1024}
	r := bufio.NewReader(wire)
	readLine := func() (string, error) {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", err
		}
		if len(line) > 8192 || !strings.HasSuffix(line, "\r\n") {
			return "", errors.New("invalid IMAP response")
		}
		return strings.TrimSuffix(line, "\r\n"), nil
	}
	greeting, err := readLine()
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(strings.ToUpper(greeting), "* OK ") {
		return nil, errors.New("invalid IMAP greeting")
	}
	// Ask explicitly instead of trusting capability text in the greeting.
	if _, err := fmt.Fprint(wire, "C1 CAPABILITY\r\n"); err != nil {
		return nil, err
	}
	advertised := false
	for {
		line, err := readLine()
		if err != nil {
			return nil, err
		}
		fields := strings.Fields(strings.ToUpper(line))
		if len(fields) < 2 {
			return nil, errors.New("invalid IMAP capability")
		}
		if fields[0] == "C1" {
			if fields[1] != "OK" || !advertised {
				return nil, errors.New("IMAP STARTTLS unavailable")
			}
			break
		}
		if fields[0] == "*" && fields[1] == "CAPABILITY" {
			for _, capability := range fields[2:] {
				if capability == "STARTTLS" {
					advertised = true
				}
			}
		} else if fields[0] != "*" || fields[1] != "OK" {
			return nil, errors.New("invalid IMAP capability")
		}
	}
	if _, err := fmt.Fprint(wire, "C2 STARTTLS\r\n"); err != nil {
		return nil, err
	}
	line, err := readLine()
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(strings.ToUpper(line))
	if len(fields) < 2 || fields[0] != "C2" || fields[1] != "OK" {
		return nil, errors.New("IMAP STARTTLS rejected")
	}
	// Leave the plaintext response budget behind for encrypted traffic. Preserve
	// any already-buffered bytes without performing another DNS lookup or dial.
	buffered, err := r.Peek(r.Buffered())
	if err != nil {
		return nil, err
	}
	tlsConn := tls.Client(&imapReplayConn{Conn: raw, reader: io.MultiReader(strings.NewReader(string(buffered)), raw)}, config)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	// The IMAP decoder starts after the upgrade, so replay a capability-free
	// greeting. All subsequent capabilities and authentication travel over TLS.
	return &imapReplayConn{Conn: tlsConn, reader: io.MultiReader(strings.NewReader("* OK TLS ready\r\n"), tlsConn)}, nil
}
