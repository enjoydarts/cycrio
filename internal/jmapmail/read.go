package jmapmail

import (
	"context"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailcontent"
)

type bodyPart struct {
	PartID      string     `json:"partId"`
	Type        string     `json:"type"`
	Disposition string     `json:"disposition"`
	Name        *string    `json:"name"`
	Parts       []bodyPart `json:"subParts"`
}
type email struct {
	ID                 string
	Mailboxes          map[string]bool `json:"mailboxIds"`
	ReceivedAt         *string         `json:"receivedAt"`
	Structure          *bodyPart       `json:"bodyStructure"`
	TextBody, HTMLBody []bodyPart
	Values             map[string]struct {
		Value     *string `json:"value"`
		Truncated bool    `json:"isTruncated"`
		Problem   bool    `json:"isEncodingProblem"`
	} `json:"bodyValues"`
}

func (c *Client) ReadText(ctx context.Context, login string, password []byte, source mailcontent.Source) ([]byte, error) {
	m, e := c.ReadMessage(ctx, login, password, source)
	return m.Text, e
}
func (c *Client) ReadMessage(ctx context.Context, _ string, _ []byte, source mailcontent.Source) (mailcontent.Message, error) {
	var result mailcontent.Message
	if !valid(source.RemoteID) || !valid(source.Folder) || source.MaxTextBytes <= 0 {
		return result, failure("invalid_response")
	}
	s, id, e := c.session(ctx)
	if e != nil {
		return result, e
	}
	var v struct {
		List     []email
		NotFound []string `json:"notFound"`
	}
	e = c.call(ctx, s, id, "Email/get", map[string]any{"ids": []string{source.RemoteID}, "properties": []string{"id", "mailboxIds", "receivedAt", "bodyStructure", "textBody", "htmlBody", "bodyValues"}, "bodyProperties": []string{"partId", "type", "disposition", "name", "subParts"}, "fetchTextBodyValues": true, "fetchHTMLBodyValues": true, "maxBodyValueBytes": mailcontent.MaxWireBytes}, &v)
	if e != nil {
		return result, e
	}
	if len(v.List) == 0 && len(v.NotFound) == 1 && v.NotFound[0] == source.RemoteID {
		return result, failure("source_missing")
	}
	if len(v.List) != 1 || len(v.NotFound) != 0 || v.List[0].ID != source.RemoteID {
		return result, failure("invalid_response")
	}
	m := v.List[0]
	allowed := source.AllowedFolders
	if len(allowed) == 0 {
		allowed = []string{source.Folder}
	}
	member := false
	for _, f := range allowed {
		if !valid(f) {
			return result, failure("invalid_response")
		}
		member = member || m.Mailboxes[f]
	}
	if !member {
		return result, failure("source_missing")
	}
	if m.ReceivedAt != nil {
		received, e := time.Parse(time.RFC3339Nano, *m.ReceivedAt)
		if e != nil || received.Year() < 1 {
			return result, failure("invalid_response")
		}
		received = received.UTC()
		result.ReceivedAt = &received
	}
	if m.Structure == nil {
		return result, failure("unsupported_content")
	}
	// Walk the MIME structure so a text part nested in an attachment or forwarded
	// message can never be selected even if it appears in textBody/htmlBody.
	eligible := map[string]string{}
	count := 0
	var walk func(bodyPart, int) bool
	walk = func(p bodyPart, depth int) bool {
		count++
		if depth > 30 || count > 1000 {
			return false
		}
		typ := strings.ToLower(p.Type)
		if strings.EqualFold(p.Disposition, "attachment") || p.Name != nil && *p.Name != "" || typ == "message/rfc822" || typ == "message/global" {
			return true
		}
		if len(p.Parts) == 0 && (typ == "text/plain" || typ == "text/html") {
			if !valid(p.PartID) || eligible[p.PartID] != "" {
				return false
			}
			eligible[p.PartID] = typ
		}
		for _, child := range p.Parts {
			if !walk(child, depth+1) {
				return false
			}
		}
		return true
	}
	if !walk(*m.Structure, 0) {
		return result, failure("invalid_response")
	}
	var chosen string
	for _, parts := range [][]bodyPart{m.TextBody, m.HTMLBody} {
		for _, p := range parts {
			if eligible[p.PartID] != "" {
				if chosen != "" {
					return result, failure("unsupported_content")
				}
				chosen = p.PartID
			}
		}
		if chosen != "" {
			break
		}
	}
	if chosen == "" {
		return result, failure("unsupported_content")
	}
	value, ok := m.Values[chosen]
	if !ok || value.Value == nil || value.Problem {
		return result, failure("invalid_response")
	}
	if value.Truncated {
		return result, failure("input_too_large")
	}
	result.Text, e = mailcontent.NormalizeSource([]byte(*value.Value), eligible[chosen], "utf-8", "", source)
	return result, e
}
