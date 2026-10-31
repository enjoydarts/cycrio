package fastmail

import (
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailcontent"
)

// pooledContentConn keeps framing state for the entire connection. Its mutex
// never spans a blocking read: the SDK reader may already be waiting for the
// next response when the owner installs a new operation's budget and guard.
// A trailing CR is withheld until its LF has been validated, as in boundedConn.
type pooledContentConn struct {
	net.Conn
	mu                    sync.Mutex
	parser                boundedConn
	guard                 *contentResponseGuard
	deadline              time.Time
	expires               time.Time
	active                bool
	opRead, total         int
	searchLow, searchHigh uint32
	searching, searchSeen bool
	searchTag             string
}

func newPooledContentConn(conn net.Conn, deadline time.Time, guard *contentResponseGuard) *pooledContentConn {
	expires := time.Now().Add(contentLifetime)
	if deadline.After(expires) {
		deadline = expires
	}
	c := &pooledContentConn{Conn: conn, guard: guard, deadline: deadline, expires: expires, active: true}
	c.parser.maxBytes = mailcontent.MaxWireBytes
	c.parser.observe = c.observe
	conn.SetDeadline(deadline)
	return c
}

func (c *pooledContentConn) boundary() bool {
	p := &c.parser
	return p.literal == 0 && p.header == 0 && !p.expectLF && !p.quoted && !p.escaped && len(c.guard.line) == 0 && p.observerErr == nil && !p.exceeded.Load() && !p.timedOut.Load() && !p.disconnected.Load() && !p.invalidFraming.Load()
}

func (c *pooledContentConn) begin(deadline time.Time, guard *contentResponseGuard) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.boundary() {
		return errInvalidFraming
	}
	c.guard = guard
	c.active = true
	c.opRead = 0
	if deadline.After(c.expires) {
		deadline = c.expires
	}
	c.deadline = deadline
	return c.Conn.SetDeadline(deadline)
}

func (c *pooledContentConn) end() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.boundary() {
		return false
	}
	c.active = false
	// Keep the same parser and reject unsolicited FETCH while the SDK waits.
	c.guard = &contentResponseGuard{}
	c.deadline = time.Now().Add(contentIdle)
	if c.deadline.After(c.expires) {
		c.deadline = c.expires
	}
	return c.Conn.SetDeadline(c.deadline) == nil
}

func (c *pooledContentConn) generation(generation uint32) {
	c.mu.Lock()
	c.guard.uidValidity = generation
	c.mu.Unlock()
}
func (c *pooledContentConn) totalBytes() int { c.mu.Lock(); defer c.mu.Unlock(); return c.total }
func (c *pooledContentConn) search(low, high uint32) {
	c.mu.Lock()
	c.searchLow = low
	c.searchHigh = high
	c.searching = true
	c.searchSeen = false
	c.searchTag = ""
	c.mu.Unlock()
}
func (c *pooledContentConn) searchDone() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.searching = false
	return c.searchSeen
}

func (c *pooledContentConn) observe(b byte, literal, headerEnd bool) error {
	if !literal && b == '\n' && !headerEnd {
		tokens, err := contentTokens(append(c.guard.line, b))
		if err != nil {
			return err
		}
		if len(tokens) >= 2 && tokens[0] == "*" && (strings.EqualFold(tokens[1], "SEARCH") || strings.EqualFold(tokens[1], "ESEARCH")) {
			if !c.searching || c.searchSeen {
				return errInvalidFraming
			}
			if strings.EqualFold(tokens[1], "ESEARCH") {
				if err := validateContentESearch(append(c.guard.line, b), c.searchTag, c.searchLow, c.searchHigh); err != nil {
					return err
				}
				c.searchSeen = true
				return c.guard.accept(b, literal, headerEnd)
			}
			if len(tokens) > 258 {
				return errInvalidFraming
			}
			seen := make(map[uint32]struct{}, len(tokens)-2)
			for _, token := range tokens[2:] {
				n, err := strconv.ParseUint(token, 10, 32)
				if err != nil || uint32(n) < c.searchLow || uint32(n) > c.searchHigh {
					return errInvalidFraming
				}
				if _, ok := seen[uint32(n)]; ok {
					return errInvalidFraming
				}
				seen[uint32(n)] = struct{}{}
			}
			c.searchSeen = true
		}
	}
	return c.guard.accept(b, literal, headerEnd)
}

func (c *pooledContentConn) consume(p []byte) error {
	c.total += len(p)
	if c.active {
		c.opRead += len(p)
	}
	if c.total > contentTotalWire || c.opRead > mailcontent.MaxWireBytes {
		c.parser.exceeded.Store(true)
		return errResponseTooLarge
	}
	for i, b := range p {
		if err := c.parser.accept(b); err != nil {
			c.parser.observerErr = err
			if errors.Is(err, errInvalidFraming) {
				c.parser.invalidFraming.Store(true)
			}
			return err
		}
		// Reject a declared literal exceeding the remaining operation budget
		// before it reaches the SDK, which may otherwise buffer the payload.
		consumed := c.opRead - len(p) + i + 1
		if c.active && c.parser.literal > mailcontent.MaxWireBytes-consumed {
			c.parser.exceeded.Store(true)
			return errResponseTooLarge
		}
	}
	return nil
}

func (c *pooledContentConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.mu.Lock()
	if c.parser.observerErr != nil {
		err := c.parser.observerErr
		c.mu.Unlock()
		return 0, err
	}
	if c.parser.exceeded.Load() {
		c.mu.Unlock()
		return 0, errResponseTooLarge
	}
	if c.parser.pendingLF {
		c.parser.pendingLF = false
		p[0] = '\n'
		c.mu.Unlock()
		return 1, nil
	}
	remaining := contentTotalWire - c.total + 1
	if c.active && mailcontent.MaxWireBytes-c.opRead+1 < remaining {
		remaining = mailcontent.MaxWireBytes - c.opRead + 1
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	c.mu.Unlock()
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	c.parser.recordTimeout(err)
	c.parser.recordDisconnect(err)
	guardErr := c.consume(p[:n])
	trailingCR := n > 0 && p[n-1] == '\r' && c.parser.literal == 0 && c.parser.expectLF
	c.mu.Unlock()
	if guardErr != nil {
		return 0, guardErr
	}
	if trailingCR {
		var next [1]byte
		m, nextErr := c.Conn.Read(next[:])
		c.mu.Lock()
		defer c.mu.Unlock()
		c.parser.recordTimeout(nextErr)
		if m == 0 {
			if nextErr == io.EOF {
				c.parser.invalidFraming.Store(true)
				nextErr = errInvalidFraming
			}
			c.parser.recordDisconnect(nextErr)
			return n - 1, nextErr
		}
		if err := c.consume(next[:]); err != nil {
			return 0, err
		}
		c.parser.pendingLF = true
		return n, nil
	}
	return n, err
}

func (c *pooledContentConn) Write(p []byte) (int, error) {
	// The SDK flushes the small UID SEARCH command in one write. Capture its
	// generated tag before sending, so ESEARCH cannot target another command.
	// Other outbound bytes (including LOGIN) are never retained.
	c.mu.Lock()
	if c.searching {
		fields := strings.Fields(string(p))
		if len(fields) >= 3 && fields[1] == "UID" && fields[2] == "SEARCH" {
			c.searchTag = fields[0]
		}
	}
	c.mu.Unlock()
	n, err := c.Conn.Write(p)
	c.mu.Lock()
	c.total += n
	exceeded := c.total > contentTotalWire
	if exceeded {
		c.parser.exceeded.Store(true)
		err = errResponseTooLarge
	}
	c.parser.recordTimeout(err)
	c.parser.recordDisconnect(err)
	c.mu.Unlock()
	if exceeded {
		c.Conn.Close()
	}
	return n, err
}

func (c *pooledContentConn) bound(t time.Time) time.Time {
	if t.IsZero() || t.After(c.deadline) {
		return c.deadline
	}
	return t
}
func (c *pooledContentConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.SetReadDeadline(c.bound(t))
}
func (c *pooledContentConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.SetWriteDeadline(c.bound(t))
}
