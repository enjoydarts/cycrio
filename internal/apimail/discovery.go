package apimail

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailsync"
)

func (c *Client) profile(ctx context.Context) (string, error) {
	var v struct {
		History string `json:"historyId"`
	}
	if _, e := c.getJSON(ctx, gmailBase+"profile", &v); e != nil {
		return "", e
	}
	if !validID(v.History) {
		return "", failure("invalid_response")
	}
	return v.History, nil
}
func (c *Client) gmailDiscover(ctx context.Context, r mailsync.PageRequest, mode string, s cursor) (mailsync.Page, error) {
	p := mailsync.Page{Initialized: true, RemoteIDs: []string{}}
	if mode == "bootstrap" {
		h, e := c.profile(ctx)
		if e != nil {
			return p, e
		}
		s.History = h
		p.Done = true
		p.Cursor, e = pack(s)
		return p, e
	}
	var ids []string
	if len(s.Pending) > 0 {
		ids = s.Pending
		s.Pending = nil
	} else {
		q := url.Values{"maxResults": {strconv.Itoa(r.Limit)}}
		if s.Next != "" {
			q.Set("pageToken", s.Next)
		}
		if mode == "incremental" && !s.Fallback && s.History != "" {
			q.Set("startHistoryId", s.History)
			q.Set("labelId", r.Folder)
			q.Set("historyTypes", "messageAdded")
			var v struct {
				HistoryID string `json:"historyId"`
				Next      string `json:"nextPageToken"`
				History   []struct {
					Added []struct {
						Message struct {
							ID string `json:"id"`
						} `json:"message"`
					} `json:"messagesAdded"`
				} `json:"history"`
			}
			status, e := c.getJSON(ctx, gmailBase+"history?"+q.Encode(), &v)
			if status == 404 {
				s.Fallback = true
				s.Next = ""
				s.Checkpoint = ""
				s.Visited = nil
				return c.gmailDiscover(ctx, r, mode, s)
			}
			if e != nil {
				return p, e
			}
			if !validID(v.HistoryID) {
				return p, failure("invalid_response")
			}
			for _, h := range v.History {
				for _, a := range h.Added {
					ids = append(ids, a.Message.ID)
					if len(ids) > 10000 {
						return p, failure("invalid_response")
					}
				}
			}
			if e := advance(&s, v.Next); e != nil {
				return p, e
			}
			s.Checkpoint = v.HistoryID
		} else {
			if mode == "incremental" && s.Checkpoint == "" {
				h, e := c.profile(ctx)
				if e != nil {
					return p, e
				}
				s.Checkpoint = h
				s.Fallback = true
			}
			q.Set("labelIds", r.Folder)
			q.Set("includeSpamTrash", "true")
			if r.Since != "" && r.Before != "" {
				zone := time.FixedZone("Asia/Tokyo", 9*3600)
				a, e := time.ParseInLocation(time.DateOnly, r.Since, zone)
				b, f := time.ParseInLocation(time.DateOnly, r.Before, zone)
				if e != nil || f != nil || !b.After(a) {
					return p, failure("invalid_response")
				}
				q.Set("q", "after:"+strconv.FormatInt(a.Unix()-1, 10)+" before:"+strconv.FormatInt(b.Unix(), 10))
			}
			var v struct {
				Messages []struct {
					ID string `json:"id"`
				} `json:"messages"`
				Next string `json:"nextPageToken"`
			}
			if _, e := c.getJSON(ctx, gmailBase+"messages?"+q.Encode(), &v); e != nil {
				return p, e
			}
			if len(v.Messages) > 100 {
				return p, failure("invalid_response")
			}
			for _, m := range v.Messages {
				ids = append(ids, m.ID)
			}
			if e := advance(&s, v.Next); e != nil {
				return p, e
			}
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !validID(id) {
			return p, failure("invalid_response")
		}
		if !seen[id] {
			seen[id] = true
			if len(p.RemoteIDs) < r.Limit {
				p.RemoteIDs = append(p.RemoteIDs, id)
			} else {
				s.Pending = append(s.Pending, id)
			}
		}
	}
	p.Done = s.Next == "" && len(s.Pending) == 0
	if p.Done && mode == "incremental" {
		s.History = s.Checkpoint
		s.Checkpoint = ""
		s.Fallback = false
	}
	var e error
	p.Cursor, e = pack(s)
	return p, e
}
func (c *Client) graphDiscover(ctx context.Context, r mailsync.PageRequest, mode string, s cursor) (mailsync.Page, error) {
	p := mailsync.Page{Initialized: true, RemoteIDs: []string{}}
	path := "/v1.0/me/mailFolders/" + url.PathEscape(r.Folder) + "/messages"
	delta := mode == "bootstrap" || mode == "incremental"
	if delta {
		path += "/delta"
	}
	q := url.Values{"$top": {strconv.Itoa(r.Limit)}, "$select": {"id"}}
	if !delta {
		zone := time.FixedZone("Asia/Tokyo", 9*3600)
		a, e := time.ParseInLocation(time.DateOnly, r.Since, zone)
		b, f := time.ParseInLocation(time.DateOnly, r.Before, zone)
		if e != nil || f != nil || !b.After(a) {
			return p, failure("invalid_response")
		}
		q.Set("$filter", "receivedDateTime ge "+a.UTC().Format(time.RFC3339)+" and receivedDateTime lt "+b.UTC().Format(time.RFC3339))
	}
	next := "https://graph.microsoft.com" + path + "?" + q.Encode()
	if s.Next != "" {
		next = s.Next
	} else if delta && s.History != "" && mode == "incremental" {
		next = s.History
	}
	decodedPath, e := url.PathUnescape(path)
	if e != nil || !graphLink(next, decodedPath) {
		return p, failure("invalid_response")
	}
	var v struct {
		Value []struct {
			ID      string    `json:"id"`
			Removed *struct{} `json:"@removed"`
		} `json:"value"`
		Next  string `json:"@odata.nextLink"`
		Delta string `json:"@odata.deltaLink"`
	}
	status, e := c.getJSON(ctx, next, &v)
	if (status == 410 || status == 404) && delta && s.History != "" {
		s.History = ""
		s.Next = ""
		s.Visited = nil
		return c.graphDiscover(ctx, r, mode, s)
	}
	if e != nil {
		return p, e
	}
	if len(v.Value) > 100 || v.Next != "" && v.Delta != "" {
		return p, failure("invalid_response")
	}
	seen := map[string]bool{}
	for _, m := range v.Value {
		if !validID(m.ID) {
			return p, failure("invalid_response")
		}
		if m.Removed == nil && !seen[m.ID] && mode != "bootstrap" {
			p.RemoteIDs = append(p.RemoteIDs, m.ID)
		}
		seen[m.ID] = true
	}
	if len(p.RemoteIDs) > r.Limit {
		return p, failure("invalid_response")
	}
	for _, link := range []string{v.Next, v.Delta} {
		if link != "" && (!graphLink(link, decodedPath) || link == next && link == v.Next) {
			return p, failure("invalid_response")
		}
	}
	if e := advance(&s, v.Next); e != nil {
		return p, e
	}
	p.Done = v.Next == ""
	if delta && p.Done {
		if v.Delta == "" {
			return p, failure("invalid_response")
		}
		s.History = v.Delta
	}
	p.Cursor, e = pack(s)
	return p, e
}
