package fastmail

import (
	"context"
	"crypto/tls"
	"errors"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailsync"
)

// Discover reads one bounded UID window without fetching or changing messages.
func (c *Client) Discover(ctx context.Context, login string, password []byte, input mailsync.PageRequest) (mailsync.Page, error) {
	invalid := mailaccounts.AdapterError{Code: "invalid_response"}
	since, e1 := time.Parse(time.DateOnly, input.Since)
	before, e2 := time.Parse(time.DateOnly, input.Before)
	if !validName(input.Folder) || e1 != nil || e2 != nil || since.Year() < 1900 || before.Year() < 1900 || !before.After(since) || before.Sub(since) > 366*24*time.Hour || input.Limit < 1 || input.Limit > 100 || (input.UIDValidity == 0 && (input.AfterUID != 0 || input.UpperUID != 0)) || (input.UpperUID != 0 && input.AfterUID > input.UpperUID) {
		return mailsync.Page{}, invalid
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	conn, err := c.dial(ctx, "imap.fastmail.com:993", &tls.Config{ServerName: "imap.fastmail.com", MinVersion: tls.VersionTLS12})
	if err != nil {
		code := "connection_failed"
		var handshake *tlsHandshakeError
		var cert *tls.CertificateVerificationError
		var record tls.RecordHeaderError
		if errors.As(err, &handshake) || errors.As(err, &cert) || errors.As(err, &record) {
			code = "tls_failed"
		}
		return mailsync.Page{}, checkError(ctx, nil, err, code)
	}
	defer conn.Close()
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { conn.Close(); close(closed) })
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	deadline, _ := ctx.Deadline()
	wire := &boundedConn{Conn: conn, deadline: deadline}
	client := imapclient.New(wire, &imapclient.Options{DebugWriter: nil})
	defer client.Close()
	fail := func(err error) (mailsync.Page, error) {
		return mailsync.Page{}, checkError(ctx, wire, err, "invalid_response")
	}
	if err := client.WaitGreeting(); err != nil {
		return fail(err)
	}
	if client.State() != imap.ConnStateNotAuthenticated {
		return mailsync.Page{}, invalid
	}
	if err := client.Login(login, string(password)).Wait(); err != nil {
		code := "invalid_response"
		var status *imap.Error
		if errors.As(err, &status) && status.Type == imap.StatusResponseTypeNo {
			code = "authentication_failed"
		}
		return mailsync.Page{}, checkError(ctx, wire, err, code)
	}
	selected, err := client.Select(input.Folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return fail(err)
	}
	if selected.UIDValidity == 0 || selected.UIDNext == 0 {
		return mailsync.Page{}, invalid
	}
	maximum := uint32(selected.UIDNext) - 1
	p := mailsync.Page{UIDValidity: selected.UIDValidity, AfterUID: input.AfterUID, UpperUID: input.UpperUID, UIDs: []uint32{}}
	if input.UIDValidity != selected.UIDValidity {
		p.AfterUID = 0
		p.UpperUID = maximum
	} else if input.UpperUID == 0 {
		p.UpperUID = maximum
	}
	if p.UpperUID > maximum || p.AfterUID > p.UpperUID {
		return mailsync.Page{}, invalid
	}
	if p.AfterUID == p.UpperUID {
		if ctx.Err() != nil {
			return fail(ctx.Err())
		}
		p.Done = true
		return p, nil
	}
	low := p.AfterUID + 1
	high := uint64(p.AfterUID) + 4096
	if high > uint64(p.UpperUID) {
		high = uint64(p.UpperUID)
	}
	data, err := client.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{{{Start: imap.UID(low), Stop: imap.UID(high)}}}, Since: since, Before: before}, nil).Wait()
	if err != nil {
		return fail(err)
	}
	set, ok := data.All.(imap.UIDSet)
	// IMAP4rev2 omits ALL when an ESEARCH result has no matching UIDs.
	if data.All == nil && data.Count == 0 && data.Min == 0 && data.Max == 0 {
		set = imap.UIDSet{}
		ok = true
	}
	if !ok || set.Dynamic() {
		return mailsync.Page{}, invalid
	}
	// Validate compressed ranges before expansion, including results the server
	// sends outside the requested UID window. Never expand an unbounded set.
	for _, r := range set {
		if r.Start == 0 || r.Stop < r.Start || uint32(r.Start) < low || uint64(r.Stop) > high {
			return mailsync.Page{}, invalid
		}
	}
	for _, r := range set {
		for uid := uint64(r.Start); uid <= uint64(r.Stop); uid++ {
			if len(p.UIDs) < input.Limit {
				p.UIDs = append(p.UIDs, uint32(uid))
			} else {
				p.AfterUID = p.UIDs[len(p.UIDs)-1]
				p.Done = p.AfterUID >= p.UpperUID
				if ctx.Err() != nil {
					return fail(ctx.Err())
				}
				return p, nil
			}
		}
	}
	p.AfterUID = uint32(high)
	p.Done = p.AfterUID >= p.UpperUID
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	return p, nil
}
