package jmapmail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailsync"
)

// Cursor contents are opaque worker state and must be encrypted at rest.
type cursor struct {
	Version                                     int
	Account, Login, Folder, Mode, Since, Before string
	State, Checkpoint, Query                    string
	Position, Pages                             int
	Fallback                                    bool
	Seen                                        []string
}

func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func pack(s cursor) (string, error) {
	b, e := json.Marshal(s)
	if e != nil || len(b) > maxCursor {
		return "", failure("invalid_response")
	}
	return string(b), nil
}
func (c *Client) state(ctx context.Context, s session, id string) (string, error) {
	var v struct {
		State string `json:"state"`
	}
	e := c.call(ctx, s, id, "Email/get", map[string]any{"ids": []string{}, "properties": []string{"id"}}, &v)
	if e == nil && !valid(v.State) {
		e = failure("invalid_response")
	}
	return v.State, e
}
func (c *Client) Discover(ctx context.Context, _ string, _ []byte, r mailsync.PageRequest) (mailsync.Page, error) {
	p := mailsync.Page{Initialized: true, RemoteIDs: []string{}}
	mode := r.Mode
	if mode == "" {
		mode = "manual"
	}
	if !valid(r.Folder) || r.Limit < 1 || r.Limit > 100 || len(r.Cursor) > maxCursor {
		return p, failure("invalid_response")
	}
	if mode != "manual" && mode != "historical" && mode != "bootstrap" && mode != "incremental" {
		return p, failure("invalid_response")
	}
	filter := map[string]any{"inMailbox": r.Folder}
	if mode == "manual" || mode == "historical" {
		z := time.FixedZone("Asia/Tokyo", 9*3600)
		a, e := time.ParseInLocation(time.DateOnly, r.Since, z)
		b, f := time.ParseInLocation(time.DateOnly, r.Before, z)
		if e != nil || f != nil || !b.After(a) || b.Sub(a) > 366*24*time.Hour {
			return p, failure("invalid_response")
		}
		filter["after"] = a.UTC().Format(time.RFC3339)
		filter["before"] = b.UTC().Format(time.RFC3339)
	}
	var st cursor
	if r.Cursor != "" {
		if json.Unmarshal([]byte(r.Cursor), &st) != nil || st.Version != 1 || st.Folder != r.Folder || st.Login != hash(c.login) || st.Pages < 0 || st.Pages > 1000 || st.Position < 0 || len(st.Seen) > 1000 {
			return p, failure("invalid_response")
		}
		if st.Mode != mode && !(st.Mode == "bootstrap" && mode == "incremental") {
			return p, failure("invalid_response")
		}
		if (mode == "manual" || mode == "historical") && (st.Since != r.Since || st.Before != r.Before) {
			return p, failure("invalid_response")
		}
	}
	s, id, e := c.session(ctx)
	if e != nil {
		return p, e
	}
	if st.Account != "" && st.Account != id {
		return p, failure("invalid_response")
	}
	st.Version = 1
	st.Account = id
	st.Login = hash(c.login)
	st.Folder = r.Folder
	st.Mode = mode
	st.Since = r.Since
	st.Before = r.Before
	if mode == "bootstrap" {
		st.State, e = c.state(ctx, s, id)
		if e != nil {
			return p, e
		}
		p.Done = true
		p.Cursor, e = pack(st)
		return p, e
	}
	var ids []string
	if mode == "incremental" && !st.Fallback && st.State != "" {
		var v struct {
			Old, New                    string
			More                        bool
			Created, Updated, Destroyed []string
		}
		var raw struct {
			Old                         string `json:"oldState"`
			New                         string `json:"newState"`
			More                        bool   `json:"hasMoreChanges"`
			Created, Updated, Destroyed []string
		}
		e = c.call(ctx, s, id, "Email/changes", map[string]any{"sinceState": st.State, "maxChanges": r.Limit}, &raw)
		if e == methodError("cannotCalculateChanges") {
			st.Fallback = true
			st.Position = 0
			st.Query = ""
			st.Checkpoint = ""
			st.Seen = nil
			st.Pages = 0
		} else if e != nil {
			return p, e
		} else {
			v.Old = raw.Old
			v.New = raw.New
			v.More = raw.More
			v.Created = raw.Created
			v.Updated = raw.Updated
			v.Destroyed = raw.Destroyed
			if v.Old != st.State || !valid(v.New) || len(v.Created)+len(v.Updated)+len(v.Destroyed) > r.Limit || v.More && v.New == st.State {
				return p, failure("invalid_response")
			}
			ids = append(v.Created, v.Updated...)
			p.Done = !v.More
			if v.More {
				key := hash(v.New)
				for _, old := range st.Seen {
					if old == key {
						return p, failure("invalid_response")
					}
				}
				st.Seen = append(st.Seen, key)
				st.Pages++
				if st.Pages > 1000 {
					return p, failure("invalid_response")
				}
			} else {
				st.Seen = nil
				st.Pages = 0
			}
			st.State = v.New
		}
	}
	if mode != "incremental" || st.Fallback || st.State == "" {
		if mode == "incremental" && st.Checkpoint == "" {
			st.Checkpoint, e = c.state(ctx, s, id)
			if e != nil {
				return p, e
			}
			st.Fallback = true
		}
		var q struct {
			IDs      []string `json:"ids"`
			Position int      `json:"position"`
			State    string   `json:"queryState"`
			Limit    *int     `json:"limit"`
		}
		e = c.call(ctx, s, id, "Email/query", map[string]any{"filter": filter, "position": st.Position, "limit": r.Limit, "sort": []any{map[string]any{"property": "receivedAt", "isAscending": true}}}, &q)
		if e != nil {
			return p, e
		}
		if !valid(q.State) || q.Position != st.Position || q.IDs == nil || len(q.IDs) > r.Limit {
			return p, failure("invalid_response")
		}
		if st.Query != "" && st.Query != q.State {
			// Offset pagination across a changed result set could skip deliveries.
			// Restart safely, preserving the pre-scan state; durable RemoteID dedup
			// makes repeated candidates harmless. Return a continuation so the
			// 100-candidate per worker page budget remains intact.
			st.Position = 0
			st.Query = ""
			st.Seen = nil
			st.Pages = 0
			p.Cursor, e = pack(st)
			return p, e
		}
		st.Query = q.State
		ids = q.IDs
		// RFC 8620 section 5.5 allows the server to clamp the requested limit.
		// A full clamped page is a continuation, including during fallback.
		effectiveLimit := r.Limit
		if q.Limit != nil {
			if *q.Limit < 1 || *q.Limit > r.Limit {
				return p, failure("invalid_response")
			}
			effectiveLimit = *q.Limit
		}
		if len(ids) > effectiveLimit {
			return p, failure("invalid_response")
		}
		p.Done = len(ids) < effectiveLimit
		if !p.Done {
			encoded, _ := json.Marshal(ids)
			key := hash(string(encoded))
			for _, old := range st.Seen {
				if old == key {
					return p, failure("invalid_response")
				}
			}
			st.Seen = append(st.Seen, key)
			st.Pages++
			st.Position += len(ids)
			if st.Pages > 1000 {
				return p, failure("invalid_response")
			}
		} else {
			st.Position = 0
			st.Pages = 0
			st.Seen = nil
			st.Query = ""
			if mode == "incremental" {
				st.State = st.Checkpoint
				st.Checkpoint = ""
				st.Fallback = false
			}
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !valid(id) || seen[id] {
			return p, failure("invalid_response")
		}
		seen[id] = true
	}
	if len(ids) > 0 {
		var v struct {
			List []struct {
				ID         string
				MailboxIDs map[string]bool `json:"mailboxIds"`
			}
			NotFound []string `json:"notFound"`
		}
		if e = c.call(ctx, s, id, "Email/get", map[string]any{"ids": ids, "properties": []string{"id", "mailboxIds"}}, &v); e != nil {
			return p, e
		}
		if len(v.List)+len(v.NotFound) != len(ids) {
			return p, failure("invalid_response")
		}
		for _, m := range v.List {
			if !seen[m.ID] {
				return p, failure("invalid_response")
			}
			delete(seen, m.ID)
			if m.MailboxIDs[r.Folder] {
				p.RemoteIDs = append(p.RemoteIDs, m.ID)
			}
		}
		for _, missing := range v.NotFound {
			if !seen[missing] {
				return p, failure("invalid_response")
			}
			delete(seen, missing)
		}
	}
	p.Cursor, e = pack(st)
	return p, e
}
