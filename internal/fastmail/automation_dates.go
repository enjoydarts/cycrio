package fastmail

import (
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// historicalUIDs fetches only bounded UID and INTERNALDATE metadata. Calendar
// SEARCH is deliberately broader because IMAP ignores a date's timezone.
func historicalUIDs(client *imapclient.Client, conn io.Closer, uids []uint32, start, end time.Time, guard *automaticDiscoveryGuard) ([]uint32, error) {
	requested := map[uint32]bool{}
	set := imap.UIDSet{}
	for _, uid := range uids {
		if uid == 0 || requested[uid] {
			return nil, mailaccounts.AdapterError{Code: "invalid_response"}
		}
		requested[uid] = true
		set.AddNum(imap.UID(uid))
	}
	guard.expected.Store(&automaticMetadataExpectation{requested: requested, seen: map[uint32]bool{}})
	command := client.Fetch(set, &imap.FetchOptions{UID: true, InternalDate: true})
	defer command.Close()
	invalid := func() ([]uint32, error) {
		conn.Close()
		return nil, mailaccounts.AdapterError{Code: "invalid_response"}
	}
	accepted := map[uint32]bool{}
	seen := map[uint32]bool{}
	count := 0
	for message := command.Next(); message != nil; message = command.Next() {
		count++
		if count > len(uids) {
			return invalid()
		}
		var uid uint32
		var date time.Time
		seenUID, seenDate := false, false
		for item := message.Next(); item != nil; item = message.Next() {
			switch value := item.(type) {
			case imapclient.FetchItemDataUID:
				if seenUID {
					return invalid()
				}
				seenUID = true
				uid = uint32(value.UID)
			case imapclient.FetchItemDataInternalDate:
				if seenDate {
					return invalid()
				}
				seenDate = true
				date = value.Time
			default:
				return invalid()
			}
		}
		if !seenUID || !seenDate || !requested[uid] || seen[uid] || date.IsZero() || date.Year() < 1900 || date.Year() > 9999 {
			return invalid()
		}
		seen[uid] = true
		accepted[uid] = !date.Before(start) && date.Before(end)
	}
	if e := command.Close(); e != nil {
		return nil, e
	}
	if count != len(uids) {
		return invalid()
	}
	result := []uint32{}
	for _, uid := range uids {
		if accepted[uid] {
			result = append(result, uid)
		}
	}
	return result, nil
}

type automaticMetadataExpectation struct{ requested, seen map[uint32]bool }

// The SDK can discard duplicate and unsolicited FETCH responses. Validate the
// bounded non-literal framing before SDK delivery, including those responses.
type automaticDiscoveryGuard struct {
	expected atomic.Pointer[automaticMetadataExpectation]
	line     []byte
}

func (g *automaticDiscoveryGuard) accept(b byte, literal, headerEnd bool) error {
	if literal || headerEnd {
		return errInvalidFraming
	}
	g.line = append(g.line, b)
	if len(g.line) > 8192 {
		return errResponseTooLarge
	}
	if b != '\n' {
		return nil
	}
	tokens, e := contentTokens(g.line)
	g.line = nil
	if e != nil {
		return e
	}
	if len(tokens) < 3 || tokens[0] != "*" || !strings.EqualFold(tokens[2], "FETCH") {
		return nil
	}
	expected := g.expected.Load()
	if expected == nil || len(tokens) != 9 || tokens[3] != "(" || tokens[8] != ")" {
		return errInvalidFraming
	}
	uid := uint32(0)
	seenDate := false
	for i := 4; i < 8; i += 2 {
		switch strings.ToUpper(tokens[i]) {
		case "UID":
			if uid != 0 {
				return errInvalidFraming
			}
			n, e := strconv.ParseUint(tokens[i+1], 10, 32)
			if e != nil || n == 0 {
				return errInvalidFraming
			}
			uid = uint32(n)
		case "INTERNALDATE":
			if seenDate || tokens[i+1] != "" {
				return errInvalidFraming
			}
			seenDate = true
		default:
			return errInvalidFraming
		}
	}
	if !seenDate || !expected.requested[uid] || expected.seen[uid] {
		return errInvalidFraming
	}
	expected.seen[uid] = true
	return nil
}
