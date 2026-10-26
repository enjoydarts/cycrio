package fastmail

import (
	"errors"
	"io"
	"net"
	"sync/atomic"
	"time"
)

const maxResponseBytes = 1 << 20

var errResponseTooLarge = errors.New("mail response limit exceeded")
var errInvalidFraming = errors.New("invalid mail response framing")

// boundedConn applies a total budget, including greetings and literals. The
// small lexical guard rejects oversized literal declarations before the SDK
// receives them, even if the server never sends the declared content. IMAP
// grammar and mailbox decoding remain the SDK's responsibility.
type boundedConn struct {
	net.Conn
	deadline    time.Time
	read        int
	exceeded    atomic.Bool
	timedOut    atomic.Bool
	quoted      bool
	escaped     bool
	header      uint8
	size        int
	literal     int
	expectLF    bool
	pendingLF   bool
	maxBytes    int
	observe     func(b byte, literal, headerEnd bool) error
	observerErr error
}

func (c *boundedConn) budget() int {
	if c.maxBytes > 0 {
		return c.maxBytes
	}
	return maxResponseBytes
}

func (c *boundedConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c.observerErr != nil {
		return 0, c.observerErr
	}
	if c.pendingLF {
		c.pendingLF = false
		p[0] = '\n'
		return 1, nil
	}
	if c.exceeded.Load() {
		return 0, errResponseTooLarge
	}
	// Read at most one byte beyond the budget to detect overflow without
	// buffering an unbounded response.
	if remaining := c.budget() - c.read + 1; len(p) > remaining {
		p = p[:remaining]
	}
	n, err := c.Conn.Read(p)
	c.recordTimeout(err)
	c.read += n
	if c.read > c.budget() {
		c.exceeded.Store(true)
		return 0, errResponseTooLarge
	}
	lastLiteral := false
	for _, b := range p[:n] {
		lastLiteral = c.literal > 0
		if guardErr := c.accept(b); guardErr != nil {
			return 0, guardErr
		}
	}
	if n > 0 && p[n-1] == '\r' && !lastLiteral {
		// The SDK notifies command completion before checking the final
		// CRLF. Do not expose a trailing CR until its LF is known, including
		// when CR and LF arrive in separate packets. Literal content is opaque.
		var next [1]byte
		m, nextErr := c.Conn.Read(next[:])
		c.recordTimeout(nextErr)
		if m == 0 {
			if nextErr == io.EOF {
				nextErr = errInvalidFraming
			}
			return n - 1, nextErr
		}
		c.read++
		if c.read > c.budget() {
			c.exceeded.Store(true)
			return 0, errResponseTooLarge
		}
		if guardErr := c.accept(next[0]); guardErr != nil {
			return 0, guardErr
		}
		c.pendingLF = true
		err = nil // Deliver the already validated LF on the next read.
	}
	return n, err
}

func (c *boundedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.recordTimeout(err)
	return n, err
}

func (c *boundedConn) recordTimeout(err error) {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		c.timedOut.Store(true)
	}
}

func (c *boundedConn) accept(b byte) error {
	literal := c.literal > 0
	headerEnd := !literal && (c.header == 2 || c.header == 3) && b == '\n'
	if err := c.acceptByte(b); err != nil {
		return err
	}
	if c.observe != nil {
		err := c.observe(b, literal, headerEnd)
		if err != nil {
			c.observerErr = err
			if errors.Is(err, errResponseTooLarge) {
				c.exceeded.Store(true)
			}
		}
		return err
	}
	return nil
}

func (c *boundedConn) acceptByte(b byte) error {
	if c.literal > 0 {
		c.literal--
		return nil
	}
	if c.expectLF && b != '\n' {
		return errInvalidFraming
	}
	c.expectLF = b == '\r'
	if c.header == 1 && b >= '0' && b <= '9' {
		c.size = c.size*10 + int(b-'0')
		if c.size > c.budget() {
			c.exceeded.Store(true)
			return errResponseTooLarge
		}
		return nil
	}
	if c.header == 1 && b == '}' {
		c.header = 2
		return nil
	}
	if c.header == 2 && b == '\r' {
		c.header = 3
		return nil
	}
	if (c.header == 2 || c.header == 3) && b == '\n' {
		c.literal, c.header = c.size, 0
		return nil
	}
	c.header = 0
	if b == '\n' {
		c.quoted, c.escaped = false, false
	} else if c.escaped {
		c.escaped = false
	} else if c.quoted && b == '\\' {
		c.escaped = true
	} else if b == '"' {
		c.quoted = !c.quoted
	} else if !c.quoted && b == '{' {
		c.header, c.size = 1, 0
	}
	return nil
}

func (c *boundedConn) SetReadDeadline(t time.Time) error {
	return c.Conn.SetReadDeadline(c.boundDeadline(t))
}

func (c *boundedConn) SetWriteDeadline(t time.Time) error {
	return c.Conn.SetWriteDeadline(c.boundDeadline(t))
}

func (c *boundedConn) boundDeadline(t time.Time) time.Time {
	if t.IsZero() || t.After(c.deadline) {
		return c.deadline
	}
	return t
}
