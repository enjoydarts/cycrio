package apimail

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/enjoydarts/cycrio/internal/mailcontent"
)

// ReadText fetches transient MIME data, selects a single body, and excludes
// attachments and embedded messages before applying the common text normalizer.
func (c *Client) ReadText(ctx context.Context, _ string, _ []byte, s mailcontent.Source) ([]byte, error) {
	if !validID(s.RemoteID) || !validID(s.Folder) {
		return nil, failure("source_missing")
	}
	folders := s.AllowedFolders
	if len(folders) == 0 {
		folders = []string{s.Folder}
	}
	if len(folders) > 1000 {
		return nil, failure("invalid_response")
	}
	allowed := make(map[string]bool, len(folders))
	for _, folder := range folders {
		if !validID(folder) {
			return nil, failure("invalid_response")
		}
		allowed[folder] = true
	}
	var raw []byte
	if c.isGmail() {
		var v struct {
			Raw    string   `json:"raw"`
			Labels []string `json:"labelIds"`
		}
		if _, e := c.getJSON(ctx, gmailBase+"messages/"+url.PathEscape(s.RemoteID)+"?format=raw", &v); e != nil {
			return nil, e
		}
		member := false
		for _, label := range v.Labels {
			if allowed[label] {
				member = true
				break
			}
		}
		if !member {
			return nil, failure("source_missing")
		}
		var e error
		raw, e = base64.RawURLEncoding.DecodeString(strings.TrimRight(v.Raw, "="))
		if e != nil {
			return nil, failure("invalid_response")
		}
	} else {
		var e error
		raw, _, e = c.get(ctx, graphBase+"messages/"+url.PathEscape(s.RemoteID)+"/$value", mailcontent.MaxWireBytes)
		if e != nil {
			return nil, e
		}
		// Immutable message IDs survive moves. Confirm the selected membership
		// after fetching MIME and before returning any body to the AI pipeline.
		var metadata struct {
			Folder string `json:"parentFolderId"`
		}
		if _, e = c.getJSON(ctx, graphBase+"messages/"+url.PathEscape(s.RemoteID)+"?$select=parentFolderId", &metadata); e != nil {
			return nil, e
		}
		if !allowed[metadata.Folder] {
			return nil, failure("source_missing")
		}
	}
	if len(raw) > mailcontent.MaxWireBytes {
		return nil, failure("input_too_large")
	}
	msg, e := mail.ReadMessage(bytes.NewReader(raw))
	if e != nil {
		return nil, failure("invalid_response")
	}
	count := 0
	parts, e := mimeText(textproto.MIMEHeader(msg.Header), msg.Body, 0, &count)
	if e != nil {
		return nil, e
	}
	if len(parts) != 1 {
		return nil, failure("unsupported_content")
	}
	b := parts[0]
	return mailcontent.NormalizeSource(b.raw, b.media, b.charset, b.encoding, s)
}

type bodyPart struct {
	raw                      []byte
	media, charset, encoding string
}

func mimeText(h textproto.MIMEHeader, r io.Reader, depth int, count *int) ([]bodyPart, error) {
	*count++
	if depth > 20 || *count > 200 {
		return nil, failure("input_too_large")
	}
	disposition, params, e := mime.ParseMediaType(h.Get("Content-Disposition"))
	if e != nil && h.Get("Content-Disposition") != "" {
		return nil, failure("invalid_response")
	}
	if disposition == "attachment" || params["filename"] != "" {
		return nil, nil
	}
	ct := h.Get("Content-Type")
	if ct == "" {
		ct = "text/plain"
	}
	media, params, e := mime.ParseMediaType(ct)
	if e != nil {
		return nil, failure("invalid_response")
	}
	if params["name"] != "" || media == "message/rfc822" {
		return nil, nil
	}
	if strings.HasPrefix(media, "multipart/") {
		if params["boundary"] == "" {
			return nil, failure("invalid_response")
		}
		reader := multipart.NewReader(r, params["boundary"])
		var all []bodyPart
		for {
			part, e := reader.NextRawPart()
			if e == io.EOF {
				break
			}
			if e != nil {
				return nil, failure("invalid_response")
			}
			selected, e := mimeText(part.Header, part, depth+1, count)
			part.Close()
			if e != nil {
				return nil, e
			}
			if media == "multipart/alternative" {
				if len(selected) > 0 && (len(all) == 0 || selected[0].media == "text/plain") {
					all = selected
				}
			} else {
				all = append(all, selected...)
			}
			if len(all) > 200 {
				return nil, failure("input_too_large")
			}
		}
		return all, nil
	}
	if media != "text/plain" && media != "text/html" {
		return nil, nil
	}
	raw, e := io.ReadAll(io.LimitReader(r, mailcontent.MaxWireBytes+1))
	if len(raw) > mailcontent.MaxWireBytes {
		return nil, failure("input_too_large")
	}
	if e != nil {
		return nil, failure("invalid_response")
	}
	return []bodyPart{{raw: raw, media: media, charset: params["charset"], encoding: h.Get("Content-Transfer-Encoding")}}, nil
}
