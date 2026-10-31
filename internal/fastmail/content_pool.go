package fastmail

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
)

// ContentScope binds an authenticated connection to its current owner and
// credential version. Neither credentials nor their fingerprint leave memory.
type ContentScope struct {
	OwnerID, AccountID, CredentialID string
	CredentialVersion                int64
	Provider, Host                   string
	Port                             int
}

const (
	contentPoolSize    = 8
	contentIdle        = 30 * time.Second
	contentLifetime    = 2 * time.Minute
	contentUses        = 128
	contentTotalWire   = 64 << 20
	contentMetadataTTL = 5 * time.Second
)

type contentKey struct {
	scope        ContentScope
	host, folder string
	port         int
	generation   uint32
	auth         [32]byte
}

type contentSession struct {
	key           contentKey
	busy          bool               // pool mutex
	created, idle time.Time          // pool mutex
	uses          int                // pool mutex
	wire          *pooledContentConn // only the session's exclusive operation uses these
	client        *imapclient.Client
	observed      uint32
	observedAt    time.Time
	missing       map[uint32]struct{}
	missingAt     time.Time
}

// ContentPool holds at most eight authenticated, read-only IMAP sessions.
// Operations sharing a key serialize; all other sessions remain independent.
type ContentPool struct {
	mu          sync.Mutex
	sessions    map[contentKey]*contentSession
	notify      chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc
	closed      bool
	wg          sync.WaitGroup
	janitorDone chan struct{}
}

func NewContentPool() *ContentPool {
	ctx, cancel := context.WithCancel(context.Background())
	return &ContentPool{sessions: make(map[contentKey]*contentSession), notify: make(chan struct{}), ctx: ctx, cancel: cancel}
}

func (p *ContentPool) Reader(scope ContentScope, client *Client) mailcontent.Reader {
	return &pooledContentReader{pool: p, scope: scope, client: client}
}

func (p *ContentPool) signal() { close(p.notify); p.notify = make(chan struct{}) }

func (p *ContentPool) reap() {
	defer close(p.janitorDone)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case now := <-ticker.C:
			p.mu.Lock()
			for key, s := range p.sessions {
				if !s.busy && (now.Sub(s.idle) >= contentIdle || now.Sub(s.created) >= contentLifetime) {
					delete(p.sessions, key)
					s.close()
					p.signal()
				}
			}
			p.mu.Unlock()
		}
	}
}

func (p *ContentPool) acquire(ctx context.Context, key contentKey) (*contentSession, error) {
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, context.Canceled
		}
		if ctx.Err() != nil {
			p.mu.Unlock()
			return nil, ctx.Err()
		}
		now := time.Now()
		if s := p.sessions[key]; s != nil && !s.busy {
			if now.Sub(s.idle) >= contentIdle || now.Sub(s.created) >= contentLifetime || s.uses >= contentUses || s.wire.totalBytes() >= contentTotalWire {
				delete(p.sessions, key)
				s.close()
			} else {
				s.busy = true
				s.uses++
				p.mu.Unlock()
				return s, nil
			}
		}
		if p.sessions[key] == nil {
			if len(p.sessions) >= contentPoolSize {
				var oldest *contentSession
				for _, s := range p.sessions {
					if !s.busy && (oldest == nil || s.idle.Before(oldest.idle)) {
						oldest = s
					}
				}
				if oldest != nil {
					delete(p.sessions, oldest.key)
					oldest.close()
				}
			}
			if len(p.sessions) < contentPoolSize {
				s := &contentSession{key: key, busy: true, created: now, uses: 1}
				p.sessions[key] = s
				if p.janitorDone == nil {
					p.janitorDone = make(chan struct{})
					go p.reap()
				}
				p.mu.Unlock()
				return s, nil
			}
		}
		wake := p.notify
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-wake:
		}
	}
}

func (s *contentSession) close() {
	if s.client != nil {
		s.client.Close()
		// SDK Close may return early for an unusual socket Close error.
		// Closed always signals completion of its background decoder.
		<-s.client.Closed()
	} else if s.wire != nil {
		s.wire.Close()
	}
	s.missing = nil
}

func (p *ContentPool) release(s *contentSession, healthy bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !healthy || p.closed {
		delete(p.sessions, s.key)
		s.close()
	} else {
		s.busy = false
		s.idle = time.Now()
	}
	p.signal()
}

// Close cancels waiters and active socket operations, then joins SDK decoders.
func (p *ContentPool) Close() error {
	p.mu.Lock()
	p.closed = true
	p.cancel()
	p.signal()
	done := p.janitorDone
	p.mu.Unlock()
	p.wg.Wait()
	if done != nil {
		<-done
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, s := range p.sessions {
		s.close()
		delete(p.sessions, key)
	}
	return nil
}

type pooledContentReader struct {
	pool   *ContentPool
	scope  ContentScope
	client *Client
}

func (r *pooledContentReader) ReadText(ctx context.Context, login string, password []byte, source mailcontent.Source) ([]byte, error) {
	invalid := mailaccounts.AdapterError{Code: "invalid_response"}
	if r.client == nil || !validName(source.Folder) || source.UIDValidity == 0 || source.UID == 0 || (!source.FullEmail && source.MaxTextBytes <= 0) {
		return nil, invalid
	}
	p := r.pool
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, mailaccounts.AdapterError{Code: "cancelled"}
	}
	p.wg.Add(1)
	p.mu.Unlock()
	defer p.wg.Done()
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	stopPool := context.AfterFunc(p.ctx, cancel)
	defer stopPool()
	h := sha256.New()
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(login)))
	h.Write(size[:])
	h.Write([]byte(login))
	h.Write(password)
	var auth [32]byte
	copy(auth[:], h.Sum(nil))
	key := contentKey{scope: r.scope, host: r.client.host, port: r.client.port, folder: source.Folder, generation: source.UIDValidity, auth: auth}
	s, err := p.acquire(ctx, key)
	if err != nil {
		return nil, checkError(ctx, nil, err, "invalid_response")
	}
	healthy := false
	defer func() { p.release(s, healthy && ctx.Err() == nil) }()
	generationError := func() error {
		if source.StrictGeneration {
			return mailaccounts.AdapterError{Code: "uid_validity_changed"}
		}
		return mailaccounts.AdapterError{Code: "source_missing"}
	}
	deadline, _ := ctx.Deadline()
	if expiry := s.created.Add(contentLifetime); deadline.After(expiry) {
		deadline = expiry
	}
	guard := &contentResponseGuard{uid: source.UID}
	if s.wire == nil {
		conn, err := r.client.connect(ctx)
		if err != nil {
			code := "connection_failed"
			var handshake *tlsHandshakeError
			var cert *tls.CertificateVerificationError
			var record tls.RecordHeaderError
			if errors.As(err, &handshake) || errors.As(err, &cert) || errors.As(err, &record) {
				code = "tls_failed"
			}
			return nil, checkError(ctx, nil, err, code)
		}
		s.wire = newPooledContentConn(conn, deadline, guard)
		s.wire.expires = s.created.Add(contentLifetime)
		s.client = imapclient.New(s.wire, &imapclient.Options{DebugWriter: nil})
	} else if err := s.wire.begin(deadline, guard); err != nil {
		return nil, checkError(ctx, &s.wire.parser, err, "invalid_response")
	}
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { s.wire.Close(); close(closed) })
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	fail := func(err error) ([]byte, error) {
		if guard.sourceMissing.Load() {
			return nil, generationError()
		}
		if s.wire.parser.exceeded.Load() {
			return nil, mailaccounts.AdapterError{Code: "input_too_large"}
		}
		return nil, checkError(ctx, &s.wire.parser, err, "invalid_response")
	}
	finish := func(body []byte, err error) ([]byte, error) {
		if ctx.Err() != nil {
			return fail(ctx.Err())
		}
		if !s.wire.end() {
			return fail(errInvalidFraming)
		}
		healthy = true
		return body, err
	}
	if s.observed == 0 {
		if err := s.client.WaitGreeting(); err != nil {
			return fail(err)
		}
		if s.client.State() != imap.ConnStateNotAuthenticated {
			return nil, invalid
		}
		if err := s.client.Login(login, string(password)).Wait(); err != nil {
			var status *imap.Error
			if errors.As(err, &status) && status.Type == imap.StatusResponseTypeNo {
				return nil, checkError(ctx, &s.wire.parser, err, "authentication_failed")
			}
			return fail(err)
		}
	}
	// A mismatching generation was observed on this authenticated session.
	// Cache only that metadata, never a body or a successful content response.
	if s.observed != 0 && s.observed != source.UIDValidity && time.Since(s.observedAt) < contentMetadataTTL {
		return finish(nil, generationError())
	}
	selected, err := s.client.Select(source.Folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		var status *imap.Error
		if errors.As(err, &status) && status.Type == imap.StatusResponseTypeNo {
			return nil, mailaccounts.AdapterError{Code: "source_missing"}
		}
		return fail(err)
	}
	if selected.UIDValidity == 0 {
		return nil, invalid
	}
	if s.observed != selected.UIDValidity {
		s.missing = nil
	}
	s.observed = selected.UIDValidity
	s.observedAt = time.Now()
	if selected.UIDValidity != source.UIDValidity {
		return finish(nil, generationError())
	}
	s.wire.generation(source.UIDValidity)
	if _, missing := s.missing[source.UID]; missing && time.Since(s.missingAt) < contentMetadataTTL {
		return finish(nil, mailaccounts.AdapterError{Code: "source_missing"})
	}
	high := uint64(source.UID) + 255
	if high > math.MaxUint32 {
		high = math.MaxUint32
	}
	s.wire.search(source.UID, uint32(high))
	data, err := s.client.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{{{Start: imap.UID(source.UID), Stop: imap.UID(high)}}}}, nil).Wait()
	searchSeen := s.wire.searchDone()
	if err != nil {
		return fail(err)
	}
	if !searchSeen {
		return nil, invalid
	}
	set, ok := data.All.(imap.UIDSet)
	if !ok && data.All != nil {
		return nil, invalid
	}
	missing := make(map[uint32]struct{}, 256)
	for uid := uint64(source.UID); uid <= high; uid++ {
		if !set.Contains(imap.UID(uid)) {
			missing[uint32(uid)] = struct{}{}
		}
	}
	s.missing = missing
	s.missingAt = time.Now()
	if _, absent := missing[source.UID]; absent {
		return finish(nil, mailaccounts.AdapterError{Code: "source_missing"})
	}
	guard.phase.Store(1)
	structure, _, err := readContentFetch(s.client, s.wire, source.UID, nil)
	if err != nil {
		return fail(err)
	}
	if structure == nil {
		return finish(nil, mailaccounts.AdapterError{Code: "source_missing"})
	}
	part, err := selectContentPart(structure)
	if err != nil {
		return nil, err
	}
	if part.body.Size > mailcontent.MaxWireBytes {
		return nil, mailaccounts.AdapterError{Code: "input_too_large"}
	}
	section := &imap.FetchItemBodySection{Part: part.path, Peek: true}
	if len(part.path) == 0 {
		section.Specifier = imap.PartSpecifierText
	}
	guard.phase.Store(2)
	_, raw, err := readContentFetch(s.client, s.wire, source.UID, section)
	if err != nil {
		return fail(err)
	}
	if raw == nil {
		return finish(nil, mailaccounts.AdapterError{Code: "source_missing"})
	}
	if uint64(len(raw)) != uint64(part.body.Size) {
		return nil, invalid
	}
	text, err := mailcontent.NormalizeSource(raw, part.body.MediaType(), partCharset(part.body.Params), part.body.Encoding, source)
	if err != nil {
		return nil, err
	}
	return finish(text, nil)
}
