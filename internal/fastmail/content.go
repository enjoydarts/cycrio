package fastmail

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
)

var _ mailcontent.Reader = (*Client)(nil)

func (c *Client) ReadText(ctx context.Context, login string, password []byte, source mailcontent.Source) ([]byte, error) {
	invalid := mailaccounts.AdapterError{Code: "invalid_response"}
	if !validName(source.Folder) || source.UIDValidity == 0 || source.UID == 0 || source.MaxTextBytes <= 0 {
		return nil, invalid
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
		return nil, checkError(ctx, nil, err, code)
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
	guard := &contentResponseGuard{uid: source.UID, uidValidity: source.UIDValidity}
	wire := &boundedConn{Conn: conn, deadline: deadline, maxBytes: mailcontent.MaxWireBytes, observe: guard.accept}
	client := imapclient.New(wire, &imapclient.Options{DebugWriter: nil})
	defer client.Close()
	fail := func(err error) ([]byte, error) {
		if guard.sourceMissing.Load() {
			return nil, mailaccounts.AdapterError{Code: "source_missing"}
		}
		if wire.exceeded.Load() {
			return nil, mailaccounts.AdapterError{Code: "input_too_large"}
		}
		return nil, checkError(ctx, wire, err, "invalid_response")
	}
	if err := client.WaitGreeting(); err != nil {
		return fail(err)
	}
	if client.State() != imap.ConnStateNotAuthenticated {
		return nil, invalid
	}
	if err := client.Login(login, string(password)).Wait(); err != nil {
		var status *imap.Error
		if errors.As(err, &status) && status.Type == imap.StatusResponseTypeNo {
			return nil, checkError(ctx, wire, err, "authentication_failed")
		}
		return fail(err)
	}
	selected, err := client.Select(source.Folder, &imap.SelectOptions{ReadOnly: true}).Wait()
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
	if selected.UIDValidity != source.UIDValidity {
		return nil, mailaccounts.AdapterError{Code: "source_missing"}
	}
	guard.phase.Store(1)
	structure, _, err := readContentFetch(client, conn, source.UID, nil)
	if err != nil {
		return fail(err)
	}
	if structure == nil {
		return nil, mailaccounts.AdapterError{Code: "source_missing"}
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
	_, raw, err := readContentFetch(client, conn, source.UID, section)
	if err != nil {
		return fail(err)
	}
	if raw == nil {
		return nil, mailaccounts.AdapterError{Code: "source_missing"}
	}
	if uint64(len(raw)) != uint64(part.body.Size) {
		return nil, invalid
	}
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	text, err := mailcontent.Normalize(raw, part.body.MediaType(), partCharset(part.body.Params), part.body.Encoding, source.MaxTextBytes)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	return text, nil
}

// Stream items explicitly rather than Collect, which accepts duplicate fields
// and buffers any unsolicited body section the server chooses to return.
func readContentFetch(client *imapclient.Client, conn io.Closer, uid uint32, section *imap.FetchItemBodySection) (imap.BodyStructure, []byte, error) {
	options := &imap.FetchOptions{}
	if section == nil {
		options.BodyStructure = &imap.FetchItemBodyStructure{Extended: true}
	} else {
		options.BodySection = []*imap.FetchItemBodySection{section}
	}
	cmd := client.Fetch(imap.UIDSetNum(imap.UID(uid)), options)
	defer cmd.Close()
	invalid := func() (imap.BodyStructure, []byte, error) {
		conn.Close()
		return nil, nil, mailaccounts.AdapterError{Code: "invalid_response"}
	}
	var structure imap.BodyStructure
	var raw []byte
	count := 0
	for msg := cmd.Next(); msg != nil; msg = cmd.Next() {
		count++
		if count > 1 {
			return invalid()
		}
		seenUID, seenBody := false, false
		for item := msg.Next(); item != nil; item = msg.Next() {
			switch item := item.(type) {
			case imapclient.FetchItemDataUID:
				if seenUID || uint32(item.UID) != uid {
					return invalid()
				}
				seenUID = true
			case imapclient.FetchItemDataBodyStructure:
				if section != nil || seenBody || !item.IsExtended {
					return invalid()
				}
				seenBody = true
				structure = item.BodyStructure
			case imapclient.FetchItemDataBodySection:
				if section == nil || seenBody || !item.MatchCommand(section) || item.Literal == nil {
					return invalid()
				}
				seenBody = true
				var err error
				raw, err = io.ReadAll(io.LimitReader(item.Literal, mailcontent.MaxWireBytes+1))
				if err != nil {
					conn.Close()
					return nil, nil, err
				}
				if len(raw) > mailcontent.MaxWireBytes {
					conn.Close()
					return nil, nil, mailaccounts.AdapterError{Code: "input_too_large"}
				}
			default:
				return invalid()
			}
		}
		if !seenUID || !seenBody {
			return invalid()
		}
	}
	if err := cmd.Close(); err != nil {
		return nil, nil, err
	}
	return structure, raw, nil
}

type contentPart struct {
	path []int
	body *imap.BodyStructureSinglePart
}

func partCharset(params map[string]string) string {
	for name, value := range params {
		if strings.EqualFold(name, "charset") {
			return value
		}
	}
	return ""
}

func attachedContent(bs imap.BodyStructure) bool {
	if d := bs.Disposition(); d != nil {
		if strings.EqualFold(d.Value, "attachment") {
			return true
		}
		for name := range d.Params {
			if strings.EqualFold(name, "filename") || strings.HasPrefix(strings.ToLower(name), "filename*") {
				return true
			}
		}
	}
	var params map[string]string
	switch bs := bs.(type) {
	case *imap.BodyStructureSinglePart:
		params = bs.Params
	case *imap.BodyStructureMultiPart:
		if bs.Extended != nil {
			params = bs.Extended.Params
		}
	}
	for name := range params {
		if strings.EqualFold(name, "name") || strings.HasPrefix(strings.ToLower(name), "name*") {
			return true
		}
	}
	return false
}

func selectContentPart(root imap.BodyStructure) (contentPart, error) {
	count := 0
	var walk func(imap.BodyStructure, []int, int, bool) (*contentPart, error)
	walk = func(bs imap.BodyStructure, path []int, depth int, excluded bool) (*contentPart, error) {
		count++
		if count > 64 || depth > 16 {
			return nil, mailaccounts.AdapterError{Code: "input_too_large"}
		}
		if bs == nil {
			return nil, mailaccounts.AdapterError{Code: "invalid_response"}
		}
		excluded = excluded || attachedContent(bs)
		switch bs := bs.(type) {
		case *imap.BodyStructureMultiPart:
			var selected *contentPart
			for i, child := range bs.Children {
				childPath := append(append([]int(nil), path...), i+1)
				candidate, err := walk(child, childPath, depth+1, excluded)
				if err != nil {
					return nil, err
				}
				if candidate == nil {
					continue
				}
				if selected == nil {
					selected = candidate
					continue
				}
				if !strings.EqualFold(bs.Subtype, "alternative") {
					return nil, mailaccounts.AdapterError{Code: "unsupported_content"}
				}
				if selected.body.MediaType() != "text/plain" && candidate.body.MediaType() == "text/plain" {
					selected = candidate
				}
			}
			return selected, nil
		case *imap.BodyStructureSinglePart:
			if bs.MessageRFC822 != nil {
				if _, err := walk(bs.MessageRFC822.BodyStructure, path, depth+1, true); err != nil {
					return nil, err
				}
				return nil, nil
			}
			if excluded {
				break
			}
			switch bs.MediaType() {
			case "text/plain", "text/html":
				return &contentPart{path: append([]int(nil), path...), body: bs}, nil
			}
		default:
			return nil, mailaccounts.AdapterError{Code: "invalid_response"}
		}
		return nil, nil
	}
	selected, err := walk(root, nil, 1, false)
	if err != nil {
		return contentPart{}, err
	}
	if selected != nil {
		return *selected, nil
	}
	return contentPart{}, mailaccounts.AdapterError{Code: "unsupported_content"}
}

// The SDK intentionally drops duplicate and unrelated UID FETCH responses.
// Validate their non-literal syntax before delivery so these cannot silently
// turn into a successful result or a missing source. Literal bytes stay opaque.
type contentResponseGuard struct {
	uid             uint32
	uidValidity     uint32
	sourceMissing   atomic.Bool
	phase           atomic.Int32
	counts          [3]int
	line            []byte
	depth           int
	quoted, escaped bool
}

func (g *contentResponseGuard) accept(b byte, literal, headerEnd bool) error {
	if literal {
		return nil
	}
	if g.escaped {
		g.escaped = false
	} else if g.quoted && b == '\\' {
		g.escaped = true
	} else if b == '"' {
		g.quoted = !g.quoted
	} else if !g.quoted {
		if b == '(' {
			g.depth++
		}
		if b == ')' {
			g.depth--
		}
		if g.depth > 20 {
			return errResponseTooLarge
		}
	}
	g.line = append(g.line, b)
	if b != '\n' {
		return nil
	}
	if headerEnd {
		// Refuse an unrequested message before the SDK starts consuming its
		// literal, including peers that never send the declared payload.
		tokens, err := contentTokens(g.line)
		if err != nil {
			return err
		}
		return g.validateFetch(tokens, false)
	}
	line := g.line
	g.line = nil
	g.depth, g.quoted, g.escaped = 0, false, false
	tokens, err := contentTokens(line)
	if err != nil {
		return err
	}
	return g.validateFetch(tokens, true)
}

func (g *contentResponseGuard) validateFetch(tokens []string, complete bool) error {
	if len(tokens) >= 4 && strings.EqualFold(tokens[1], "OK") && strings.EqualFold(tokens[2], "[UIDVALIDITY") {
		if !strings.HasSuffix(tokens[3], "]") {
			return errInvalidFraming
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(tokens[3], "]"), 10, 32)
		if err != nil || n == 0 {
			return errInvalidFraming
		}
		if g.uidValidity != 0 && uint32(n) != g.uidValidity {
			g.sourceMissing.Store(true)
			return errInvalidFraming
		}
	}
	if len(tokens) < 3 || tokens[0] != "*" || !strings.EqualFold(tokens[2], "FETCH") {
		return nil
	}
	phase := g.phase.Load()
	if phase < 1 || phase > 2 {
		return errInvalidFraming
	}
	if g.counts[phase] > 0 {
		return errInvalidFraming
	}
	if complete {
		g.counts[phase]++
	}
	depth, seen := 0, false
	for i := 3; i < len(tokens); i++ {
		switch tokens[i] {
		case "(":
			depth++
		case ")":
			depth--
		default:
			if depth == 1 && strings.EqualFold(tokens[i], "UID") {
				if seen || i+1 == len(tokens) {
					return errInvalidFraming
				}
				n, err := strconv.ParseUint(tokens[i+1], 10, 32)
				if err != nil || uint32(n) != g.uid {
					return errInvalidFraming
				}
				seen = true
				i++
			}
		}
	}
	if !seen {
		return errInvalidFraming
	}
	return nil
}

func contentTokens(line []byte) ([]string, error) {
	var tokens []string
	depth := 0
	for i := 0; i < len(line); {
		switch line[i] {
		case ' ', '\t', '\r', '\n':
			i++
		case '(', ')':
			if line[i] == '(' {
				depth++
			} else {
				depth--
			}
			// Bound recursive SDK parsing before it sees an adversarial tree.
			if depth > 20 {
				return nil, errResponseTooLarge
			}
			tokens = append(tokens, string(line[i]))
			i++
		case '"':
			i++
			for i < len(line) {
				if line[i] == '\\' {
					i += 2
					continue
				}
				if line[i] == '"' {
					i++
					break
				}
				i++
			}
			tokens = append(tokens, "")
		default:
			start := i
			for i < len(line) && !strings.ContainsRune(" \t\r\n()\"", rune(line[i])) {
				i++
			}
			tokens = append(tokens, string(line[start:i]))
		}
	}
	return tokens, nil
}
