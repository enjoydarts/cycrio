package mailcontent

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime/quotedprintable"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"golang.org/x/net/html"
	"golang.org/x/text/encoding/ianaindex"
)

// Normalize decodes one selected MIME body. All buffers are transient and no
// decoder or server error text is returned to callers.
func Normalize(raw []byte, mediaType, charset, encoding string, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, contentError("invalid_response")
	}
	if maxBytes > MaxTextBytes {
		maxBytes = MaxTextBytes
	}
	if len(raw) > MaxWireBytes {
		return nil, contentError("input_too_large")
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != "text/plain" && mediaType != "text/html" {
		return nil, contentError("unsupported_content")
	}
	var reader io.Reader = bytes.NewReader(raw)
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "7bit", "8bit", "binary":
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, reader)
	case "quoted-printable":
		reader = quotedprintable.NewReader(reader)
	default:
		return nil, contentError("unsupported_content")
	}
	decoded, err := boundedRead(reader, MaxWireBytes)
	if err != nil {
		return nil, err
	}
	charset = strings.ToLower(strings.TrimSpace(charset))
	switch charset {
	case "", "us-ascii", "ascii":
		for _, b := range decoded {
			if b > 127 {
				return nil, contentError("invalid_response")
			}
		}
	case "utf-8", "utf8":
	default:
		enc, err := ianaindex.MIME.Encoding(charset)
		if err != nil || enc == nil {
			return nil, contentError("unsupported_content")
		}
		decoded, err = boundedRead(enc.NewDecoder().Reader(bytes.NewReader(decoded)), MaxWireBytes)
		if err != nil {
			return nil, err
		}
		// x/text substitutes RuneError for malformed legacy byte sequences.
		// Refuse that lossy result instead of sending corrupted text to inference.
		if bytes.Contains(decoded, []byte("\uFFFD")) {
			return nil, contentError("invalid_response")
		}
	}
	if !utf8.Valid(decoded) {
		return nil, contentError("invalid_response")
	}
	if mediaType == "text/html" {
		return htmlText(decoded, maxBytes)
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(decoded), "\r\n", "\n"), "\r", "\n")
	if len(text) > maxBytes {
		return nil, contentError("input_too_large")
	}
	return []byte(text), nil
}

func contentError(code string) error { return mailaccounts.AdapterError{Code: code} }

func boundedRead(reader io.Reader, maximum int) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(reader, int64(maximum)+1))
	if len(b) > maximum {
		return nil, contentError("input_too_large")
	}
	if err != nil {
		return nil, contentError("invalid_response")
	}
	return b, nil
}

func htmlText(raw []byte, maximum int) ([]byte, error) {
	document, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, contentError("invalid_response")
	}
	var text strings.Builder
	space, newline := false, false
	appendText := func(s string) bool {
		for _, r := range s {
			if unicode.IsSpace(r) {
				space = true
				continue
			}
			if text.Len() > 0 {
				if newline {
					text.WriteByte('\n')
				} else if space {
					text.WriteByte(' ')
				}
			}
			space, newline = false, false
			text.WriteRune(r)
			if text.Len() > maximum {
				return false
			}
		}
		return true
	}
	type visit struct {
		node *html.Node
		end  bool
	}
	stack := []visit{{node: document}}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		node := v.node
		if node.Type == html.ElementNode {
			switch node.Data {
			case "head", "script", "style", "template", "noscript", "iframe", "object", "embed", "svg", "math":
				continue
			}
			switch node.Data {
			case "address", "article", "aside", "blockquote", "br", "dd", "div", "dl", "dt", "fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "li", "main", "nav", "ol", "p", "pre", "section", "table", "td", "th", "tr", "ul":
				newline = true
			}
		}
		if v.end {
			continue
		}
		if node.Type == html.TextNode && !appendText(node.Data) {
			return nil, contentError("input_too_large")
		}
		stack = append(stack, visit{node: node, end: true})
		for child := node.LastChild; child != nil; child = child.PrevSibling {
			stack = append(stack, visit{node: child})
		}
	}
	return []byte(text.String()), nil
}
