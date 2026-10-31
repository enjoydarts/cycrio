package fastmail

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailcontent"
)

type poolServer struct {
	mu             sync.Mutex
	commands       []string
	dials          atomic.Int32
	validity       atomic.Uint32
	present        bool
	body           string
	stall          bool
	searchResponse string
	disconnect     chan struct{}
	wg             sync.WaitGroup
}

func newPoolServer(t *testing.T, present bool) (*poolServer, *Client) {
	t.Helper()
	s := &poolServer{present: present, body: "hello", disconnect: make(chan struct{})}
	s.validity.Store(77)
	c := NewClient()
	c.dial = func(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
		s.dials.Add(1)
		client, server := net.Pipe()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer server.Close()
			serverDone := make(chan struct{})
			defer close(serverDone)
			go func() {
				select {
				case <-s.disconnect:
					server.Close()
				case <-serverDone:
				}
			}()
			fmt.Fprint(server, "* OK [CAPABILITY IMAP4rev1] synthetic\r\n")
			r := bufio.NewReader(server)
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				f := strings.Fields(line)
				s.mu.Lock()
				s.commands = append(s.commands, line)
				s.mu.Unlock()
				tag := f[0]
				switch f[1] {
				case "CAPABILITY":
					fmt.Fprintf(server, "* CAPABILITY IMAP4rev1\r\n%s OK done\r\n", tag)
				case "LOGIN":
					fmt.Fprintf(server, "%s OK authenticated\r\n", tag)
				case "EXAMINE":
					fmt.Fprintf(server, "* 1 EXISTS\r\n* OK [UIDVALIDITY %d] generation\r\n%s OK [READ-ONLY] selected\r\n", s.validity.Load(), tag)
				case "UID":
					if f[2] == "SEARCH" {
						if s.searchResponse != "" {
							if s.searchResponse != "OMIT" {
								fmt.Fprint(server, strings.ReplaceAll(s.searchResponse, "$TAG", tag))
							}
						} else if s.present {
							fmt.Fprint(server, "* SEARCH 9\r\n")
						} else {
							fmt.Fprint(server, "* SEARCH\r\n")
						}
					} else if strings.Contains(line, "BODYSTRUCTURE") {
						fmt.Fprintf(server, "* 1 FETCH (UID 9 BODYSTRUCTURE %s)\r\n", plainStructure)
					} else if s.stall {
						io.Copy(io.Discard, server)
						return
					} else if strings.HasPrefix(s.body, "RAW:") {
						fmt.Fprint(server, strings.TrimPrefix(s.body, "RAW:"))
					} else {
						fmt.Fprintf(server, "* 1 FETCH (UID 9 BODY[TEXT] {%d}\r\n%s)\r\n", len(s.body), s.body)
					}
					fmt.Fprintf(server, "%s OK done\r\n", tag)
				default:
					t.Errorf("unexpected pool command %q", f[1])
					return
				}
			}
		}()
		return client, nil
	}
	return s, c
}

func (s *poolServer) count(fragment string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, line := range s.commands {
		if strings.Contains(line, fragment) {
			n++
		}
	}
	return n
}

func poolScope() ContentScope {
	return ContentScope{OwnerID: "owner", AccountID: "account", CredentialID: "credential", CredentialVersion: 1, Provider: "fastmail", Host: "imap.fastmail.com", Port: 993}
}

func TestContentPoolReusesLoginAndBatchesOnlyMissingUIDs(t *testing.T) {
	s, c := newPoolServer(t, false)
	p := NewContentPool()
	defer p.Close()
	r := p.Reader(poolScope(), c)
	for _, uid := range []uint32{9, 10, 11} {
		source := contentSource()
		source.UID = uid
		body, err := r.ReadText(context.Background(), "synthetic@example.test", []byte("password"), source)
		if body != nil || adapterCode(err) != "source_missing" {
			t.Fatalf("missing result: %s", adapterCode(err))
		}
	}
	if s.dials.Load() != 1 || s.count(" LOGIN ") != 1 || s.count(" UID SEARCH UID 9:264") != 1 || s.count(" EXAMINE ") != 3 {
		t.Fatalf("no bounded reuse: dials=%d login=%d search=%d examine=%d", s.dials.Load(), s.count(" LOGIN "), s.count(" UID SEARCH "), s.count(" EXAMINE "))
	}
	p.Close()
	s.wg.Wait()
}

func TestContentPoolFetchesPositiveBodyEveryTime(t *testing.T) {
	s, c := newPoolServer(t, true)
	p := NewContentPool()
	defer p.Close()
	r := p.Reader(poolScope(), c)
	for range 2 {
		body, err := r.ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
		if err != nil || string(body) != "hello" {
			t.Fatalf("body: %q %v", body, err)
		}
	}
	if s.dials.Load() != 1 || s.count("BODY.PEEK[TEXT]") != 2 || s.count("BODYSTRUCTURE") != 2 {
		t.Fatal("body was cached or login not reused")
	}
}

func TestContentPoolIsolatesAuthenticationAndSourceScope(t *testing.T) {
	for _, change := range []string{"owner", "account", "credential", "version", "provider", "host", "port", "login", "password", "folder", "generation"} {
		t.Run(change, func(t *testing.T) {
			s, c := newPoolServer(t, false)
			p := NewContentPool()
			defer p.Close()
			scope := poolScope()
			source := contentSource()
			login := "synthetic@example.test"
			password := []byte("password")
			p.Reader(scope, c).ReadText(context.Background(), login, password, source)
			switch change {
			case "owner":
				scope.OwnerID = "other"
			case "account":
				scope.AccountID = "other"
			case "credential":
				scope.CredentialID = "other"
			case "version":
				scope.CredentialVersion++
			case "provider":
				scope.Provider = "imap"
			case "host":
				scope.Host = "mail.example.test"
			case "port":
				scope.Port = 143
			case "login":
				login = "other@example.test"
			case "password":
				password = []byte("other")
			case "folder":
				source.Folder = "Archive"
			case "generation":
				source.UIDValidity++
			}
			p.Reader(scope, c).ReadText(context.Background(), login, password, source)
			if s.dials.Load() != 2 {
				t.Fatalf("%s shared session", change)
			}
		})
	}
}

func TestContentPoolCachesAuthenticatedGenerationBriefly(t *testing.T) {
	s, c := newPoolServer(t, false)
	s.validity.Store(78)
	p := NewContentPool()
	defer p.Close()
	r := p.Reader(poolScope(), c)
	for _, strict := range []bool{false, true} {
		source := contentSource()
		source.StrictGeneration = strict
		_, err := r.ReadText(context.Background(), "synthetic@example.test", []byte("password"), source)
		want := "source_missing"
		if strict {
			want = "uid_validity_changed"
		}
		if adapterCode(err) != want {
			t.Fatalf("generation: %s", adapterCode(err))
		}
	}
	if s.dials.Load() != 1 || s.count(" EXAMINE ") != 1 || s.count(" UID ") != 0 {
		t.Fatal("generation metadata not reused")
	}
}

func TestContentPoolPoisonsInvalidResponsesAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		stall            bool
	}{
		{"uid", "RAW:* 1 FETCH (UID 8 BODY[TEXT] {5}\r\nhello)\r\n", "invalid_response", false},
		{"duplicate", "RAW:* 1 FETCH (UID 9 BODY[TEXT] {5}\r\nhello)\r\n* 1 FETCH (UID 9 BODY[TEXT] {5}\r\nhello)\r\n", "invalid_response", false},
		{"literal", "RAW:* 1 FETCH (UID 9 BODY[TEXT] {5242881}\r\n", "input_too_large", false},
		{"framing", "RAW:* 1 FETCH (UID 9 BODY[TEXT] {5}\rXhello)\r\n", "invalid_response", false},
		{"timeout", "", "timeout", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c := newPoolServer(t, true)
			s.body = tc.body
			s.stall = tc.stall
			p := NewContentPool()
			defer p.Close()
			r := p.Reader(poolScope(), c)
			for range 2 {
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				body, err := r.ReadText(ctx, "synthetic@example.test", []byte("password"), contentSource())
				cancel()
				if body != nil || adapterCode(err) != tc.want {
					t.Fatalf("poison result: %s", adapterCode(err))
				}
			}
			if s.dials.Load() != 2 {
				t.Fatal("poison session reused")
			}
		})
	}
}

func TestContentPoolCloseInterruptsActiveAndRejectsNewReads(t *testing.T) {
	s, c := newPoolServer(t, true)
	s.stall = true
	p := NewContentPool()
	r := p.Reader(poolScope(), c)
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
	}()
	deadline := time.Now().Add(time.Second)
	for s.count("BODY.PEEK") == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not join active operation")
	}
	_, err := r.ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
	if err == nil || s.dials.Load() != 1 {
		t.Fatal("closed pool dialed")
	}
	s.wg.Wait()
}

var _ mailcontent.Reader = (*pooledContentReader)(nil)

func TestContentPoolRequiresExactlyOneBoundedSearchResponse(t *testing.T) {
	for _, response := range []string{"OMIT", "* SEARCH 8\r\n", "* SEARCH 9 9\r\n", "* SEARCH 9\r\n* SEARCH 9\r\n", "* ESEARCH ALL 9\r\n"} {
		t.Run(response, func(t *testing.T) {
			s, c := newPoolServer(t, true)
			s.searchResponse = response
			p := NewContentPool()
			defer p.Close()
			body, err := p.Reader(poolScope(), c).ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
			if body != nil || adapterCode(err) != "invalid_response" {
				t.Fatalf("invalid search accepted: %s", adapterCode(err))
			}
		})
	}
}

func TestContentPoolAcceptsBoundedCorrelatedESearch(t *testing.T) {
	for _, response := range []string{"* ESEARCH (TAG \"$TAG\") UID ALL 9:10\r\n", "* ESEARCH (TAG \"$TAG\") UID\r\n", "* ESEARCH (TAG \"$TAG\") UID ALL 9 COUNT 1 MIN 9 MAX 9\r\n"} {
		t.Run(response, func(t *testing.T) {
			s, c := newPoolServer(t, true)
			s.searchResponse = response
			p := NewContentPool()
			defer p.Close()
			body, err := p.Reader(poolScope(), c).ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
			if strings.HasSuffix(response, " UID\r\n") {
				if body != nil || adapterCode(err) != "source_missing" {
					t.Fatalf("empty ESEARCH: %s", adapterCode(err))
				}
			} else if err != nil || string(body) != "hello" {
				t.Fatalf("valid ESEARCH rejected: %s", adapterCode(err))
			}
		})
	}
}

func TestContentPoolRejectsUncorrelatedAndUnsafeESearch(t *testing.T) {
	for _, response := range []string{"* ESEARCH (TAG \"wrong\") UID ALL 9\r\n", "* ESEARCH UID ALL 9\r\n", "* ESEARCH (TAG \"$TAG\") UID ALL 8:10\r\n", "* ESEARCH (TAG \"$TAG\") UID ALL 9,9\r\n", "* ESEARCH (TAG \"$TAG\") UID ALL 9:*\r\n", "* ESEARCH (TAG \"$TAG\") UID ALL 9:265\r\n", "* ESEARCH (TAG \"$TAG\") UID ALL 9 ALL 10\r\n", "* ESEARCH (TAG \"$TAG\") UID ALL 9 COUNT 2\r\n", "* ESEARCH (TAG \"$TAG\") UID COUNT 1\r\n", "* ESEARCH (TAG \"$TAG\") UID ALL 9\r\n* ESEARCH (TAG \"$TAG\") UID ALL 9\r\n"} {
		t.Run(response, func(t *testing.T) {
			s, c := newPoolServer(t, true)
			s.searchResponse = response
			p := NewContentPool()
			defer p.Close()
			body, err := p.Reader(poolScope(), c).ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
			if body != nil || adapterCode(err) != "invalid_response" {
				t.Fatalf("unsafe ESEARCH accepted: %s", adapterCode(err))
			}
		})
	}
}

func TestContentPoolExpiresMissingCacheAndRechecksGeneration(t *testing.T) {
	s, c := newPoolServer(t, false)
	p := NewContentPool()
	defer p.Close()
	r := p.Reader(poolScope(), c)
	r.ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
	p.mu.Lock()
	for _, session := range p.sessions {
		session.missingAt = time.Now().Add(-contentMetadataTTL)
	}
	p.mu.Unlock()
	r.ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
	if s.count(" UID SEARCH ") != 2 {
		t.Fatal("negative cache did not expire")
	}
	s.validity.Store(78)
	for range 2 {
		_, err := r.ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
		if adapterCode(err) != "source_missing" {
			t.Fatal("generation change ignored")
		}
	}
	if s.count(" EXAMINE ") != 3 {
		t.Fatal("generation cache did not reuse authenticated observation")
	}
	p.mu.Lock()
	for _, session := range p.sessions {
		session.observedAt = time.Now().Add(-contentMetadataTTL)
	}
	p.mu.Unlock()
	r.ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
	if s.count(" EXAMINE ") != 4 {
		t.Fatal("generation cache did not expire")
	}
}

func TestContentPoolSessionRetirementAndCapacity(t *testing.T) {
	for _, limit := range []string{"idle", "lifetime", "lifetime_margin", "uses", "wire"} {
		t.Run(limit, func(t *testing.T) {
			s, c := newPoolServer(t, false)
			p := NewContentPool()
			defer p.Close()
			r := p.Reader(poolScope(), c)
			r.ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
			p.mu.Lock()
			for _, session := range p.sessions {
				switch limit {
				case "idle":
					session.idle = time.Now().Add(-contentIdle)
				case "lifetime":
					session.created = time.Now().Add(-contentLifetime)
				case "lifetime_margin":
					session.created = time.Now().Add(-contentLifetime + checkTimeout/2)
				case "uses":
					session.uses = contentUses
				case "wire":
					session.wire.mu.Lock()
					session.wire.total = contentTotalWire
					session.wire.mu.Unlock()
				}
			}
			p.mu.Unlock()
			r.ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
			if s.dials.Load() != 2 {
				t.Fatalf("%s limit did not retire session", limit)
			}
		})
	}
	_, c := newPoolServer(t, false)
	p := NewContentPool()
	defer p.Close()
	for i := range 12 {
		scope := poolScope()
		scope.OwnerID = fmt.Sprint(i)
		p.Reader(scope, c).ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
	}
	p.mu.Lock()
	n := len(p.sessions)
	p.mu.Unlock()
	if n != contentPoolSize {
		t.Fatalf("unbounded sessions: %d", n)
	}
}

func TestContentPoolConcurrentRequestsSerializeAndJoinCancellation(t *testing.T) {
	s, c := newPoolServer(t, true)
	p := NewContentPool()
	defer p.Close()
	r := p.Reader(poolScope(), c)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, err := r.ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
			if err != nil || string(body) != "hello" {
				t.Errorf("concurrent read: %s", adapterCode(err))
			}
		}()
	}
	wg.Wait()
	if s.dials.Load() != 1 || s.count("BODY.PEEK[TEXT]") != 20 {
		t.Fatal("requests mixed or connection not reused")
	}
	// A completed call's context must no longer be able to close this session.
	ctx, cancel := context.WithCancel(context.Background())
	r.ReadText(ctx, "synthetic@example.test", []byte("password"), contentSource())
	cancel()
	body, err := r.ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
	if err != nil || string(body) != "hello" || s.dials.Load() != 1 {
		t.Fatal("previous context closed reused session")
	}
}

func TestContentPoolWaitingRequestHonorsCancellation(t *testing.T) {
	s, c := newPoolServer(t, true)
	s.stall = true
	p := NewContentPool()
	defer p.Close()
	r := p.Reader(poolScope(), c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := r.ReadText(ctx, "synthetic@example.test", []byte("password"), contentSource())
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for s.count("BODY.PEEK") == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer waitCancel()
	_, err := r.ReadText(waitCtx, "synthetic@example.test", []byte("password"), contentSource())
	if adapterCode(err) != "timeout" {
		t.Fatalf("waiting request: %s", adapterCode(err))
	}
	cancel()
	if adapterCode(<-done) != "cancelled" {
		t.Fatal("active request did not cancel")
	}
	if s.dials.Load() != 1 {
		t.Fatal("waiter created another session")
	}
}

func TestPooledWirePreservesLiteralStateAndAdmitsWithinBudgetLiteral(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	guard := &contentResponseGuard{uid: 9}
	guard.phase.Store(2)
	wire := newPooledContentConn(client, time.Now().Add(time.Second), guard)
	header := fmt.Sprintf("* 1 FETCH (UID 9 BODY[TEXT] {%d}\r\n", mailcontent.MaxWireBytes-128)
	if err := wire.consume([]byte(header + strings.Repeat("x", 256))); err != nil {
		t.Fatalf("valid partial literal rejected: %v", err)
	}
	remaining := wire.parser.literal
	if err := wire.begin(time.Now().Add(time.Second), &contentResponseGuard{uid: 10}); err == nil || wire.parser.literal != remaining {
		t.Fatal("partial parser was reset")
	}
}

func TestPooledWireBoundsLifetime(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	started := time.Now()
	wire := newPooledContentConn(client, started.Add(3*time.Minute), &contentResponseGuard{})
	if wire.deadline.After(started.Add(contentLifetime + time.Second)) {
		t.Fatal("initial operation exceeded connection lifetime")
	}
	if err := wire.begin(started.Add(4*time.Minute), &contentResponseGuard{}); err != nil {
		t.Fatal(err)
	}
	if wire.deadline.After(started.Add(contentLifetime + time.Second)) {
		t.Fatal("next operation extended connection lifetime")
	}
}

func TestPooledWireCountsOutboundBytes(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	wire := newPooledContentConn(client, time.Now().Add(time.Second), &contentResponseGuard{})
	wire.total = contentTotalWire - 1
	go io.Copy(io.Discard, server)
	if _, err := wire.Write([]byte("abc")); err != errResponseTooLarge {
		t.Fatal("outbound bytes bypassed connection wire budget")
	}
}

func TestContentPoolClampsUIDRangeAtUint32Boundary(t *testing.T) {
	s, c := newPoolServer(t, false)
	p := NewContentPool()
	defer p.Close()
	source := contentSource()
	source.UID = ^uint32(0) - 1
	_, err := p.Reader(poolScope(), c).ReadText(context.Background(), "synthetic@example.test", []byte("password"), source)
	if adapterCode(err) != "source_missing" || s.count(" UID SEARCH UID 4294967294:4294967295") != 1 {
		t.Fatal("UID range overflowed")
	}
}

func TestContentPoolCloseCancelsCapacityWaiters(t *testing.T) {
	s, c := newPoolServer(t, true)
	s.stall = true
	p := NewContentPool()
	defer p.Close()
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scope := poolScope()
			scope.OwnerID = fmt.Sprint(i)
			p.Reader(scope, c).ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
		}()
	}
	deadline := time.Now().Add(time.Second)
	for s.count("BODY.PEEK") < 8 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.dials.Load() != contentPoolSize {
		t.Fatal("active session capacity exceeded")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	s.wg.Wait()
}

type poolCloseErrorConn struct {
	net.Conn
	decoderExit <-chan struct{}
}

func (c poolCloseErrorConn) Close() error { c.Conn.Close(); return errors.New("synthetic close error") }
func (c poolCloseErrorConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil {
		<-c.decoderExit
	}
	return n, err
}

func TestContentPoolCloseJoinsDecoderEvenWhenSocketCloseReturnsError(t *testing.T) {
	_, c := newPoolServer(t, false)
	dial := c.dial
	exit := make(chan struct{})
	var allowExit sync.Once
	defer allowExit.Do(func() { close(exit) })
	c.dial = func(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
		conn, err := dial(ctx, address, config)
		return poolCloseErrorConn{Conn: conn, decoderExit: exit}, err
	}
	p := NewContentPool()
	defer p.Close()
	p.Reader(poolScope(), c).ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
		t.Fatal("pool Close returned before decoder exited")
	case <-time.After(20 * time.Millisecond):
	}
	allowExit.Do(func() { close(exit) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not complete after decoder exited")
	}
}

func TestContentPoolClassifiesIdleServerDisconnectWithoutRetry(t *testing.T) {
	s, c := newPoolServer(t, false)
	p := NewContentPool()
	defer p.Close()
	r := p.Reader(poolScope(), c)
	_, err := r.ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
	if adapterCode(err) != "source_missing" {
		t.Fatal(err)
	}
	p.mu.Lock()
	var decoderDone <-chan struct{}
	for _, session := range p.sessions {
		decoderDone = session.client.Closed()
	}
	p.mu.Unlock()
	close(s.disconnect)
	select {
	case <-decoderDone:
	case <-time.After(time.Second):
		t.Fatal("idle disconnect not observed")
	}
	_, err = r.ReadText(context.Background(), "synthetic@example.test", []byte("password"), contentSource())
	if adapterCode(err) != "connection_failed" || s.dials.Load() != 1 {
		t.Fatalf("idle transport failure lost or retried: %s", adapterCode(err))
	}
}
