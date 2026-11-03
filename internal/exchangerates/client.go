// Package exchangerates retrieves public ECB reference rates for display conversion.
package exchangerates

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"time"
)

const SourceURL = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"
const maxResponseBytes = 1 << 20

type Rate struct {
	Currency string `json:"currency"`
	Rate     string `json:"rate"`
}
type Snapshot struct {
	Source       string    `json:"source"`
	SourceURL    string    `json:"source_url"`
	AsOf         string    `json:"as_of"`
	FetchedAt    time.Time `json:"fetched_at"`
	BaseCurrency string    `json:"base_currency"`
	Rates        []Rate    `json:"rates"`
}
type Client struct {
	http *http.Client
	now  func() time.Time
}

// NewClient copies the supplied HTTP client and disallows redirects. The transport
// and clock can be supplied for deterministic tests; the source URL is always fixed.
func NewClient(client *http.Client, now func() time.Time) *Client {
	if client == nil {
		client = &http.Client{}
	}
	cloned := *client
	cloned.Timeout = 5 * time.Second
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if now == nil {
		now = time.Now
	}
	return &Client{http: &cloned, now: now}
}

var decimal = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
var currency = regexp.MustCompile(`^[A-Z]{3}$`)
var errUnavailable = errors.New("exchange rate source unavailable")

func (c *Client) Latest(ctx context.Context) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, SourceURL, nil)
	if err != nil {
		return Snapshot{}, errUnavailable
	}
	response, err := c.http.Do(req)
	if err != nil {
		return Snapshot{}, errUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Snapshot{}, errUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return Snapshot{}, errUnavailable
	}
	// Validate the entire XML document, including attributes that encoding/xml
	// otherwise silently overwrites and trailing additional documents.
	decoder := xml.NewDecoder(bytes.NewReader(body))
	depth, roots := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Snapshot{}, errUnavailable
		}
		switch token := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
			}
			depth++
			seen := map[xml.Name]bool{}
			for _, attr := range token.Attr {
				if seen[attr.Name] {
					return Snapshot{}, errUnavailable
				}
				seen[attr.Name] = true
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(token)) != 0 {
				return Snapshot{}, errUnavailable
			}
		}
	}
	if roots != 1 || depth != 0 {
		return Snapshot{}, errUnavailable
	}
	var document struct {
		XMLName xml.Name `xml:"Envelope"`
		Groups  []struct {
			Days []struct {
				Date  string `xml:"time,attr"`
				Rates []struct {
					Currency string `xml:"currency,attr"`
					Rate     string `xml:"rate,attr"`
				} `xml:"Cube"`
			} `xml:"Cube"`
		} `xml:"Cube"`
	}
	if xml.Unmarshal(body, &document) != nil || len(document.Groups) != 1 || len(document.Groups[0].Days) != 1 {
		return Snapshot{}, errUnavailable
	}
	day := document.Groups[0].Days[0]
	asOf, err := time.Parse(time.DateOnly, day.Date)
	now := c.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if err != nil || asOf.After(today) || asOf.Before(today.AddDate(0, 0, -14)) {
		return Snapshot{}, errUnavailable
	}
	values := map[string]*big.Rat{}
	for _, rate := range day.Rates {
		if !currency.MatchString(rate.Currency) || values[rate.Currency] != nil || len(rate.Rate) > 64 || !decimal.MatchString(rate.Rate) {
			return Snapshot{}, errUnavailable
		}
		value, ok := new(big.Rat).SetString(rate.Rate)
		if !ok || value.Sign() <= 0 {
			return Snapshot{}, errUnavailable
		}
		values[rate.Currency] = value
	}
	usd, jpy := values["USD"], values["JPY"]
	if usd == nil || jpy == nil {
		return Snapshot{}, errUnavailable
	}
	usdJPY := new(big.Rat).Quo(jpy, usd).FloatString(12)
	eurJPY := jpy.FloatString(12)
	if usdJPY == "0.000000000000" || eurJPY == "0.000000000000" {
		return Snapshot{}, errUnavailable
	}
	return Snapshot{Source: "ECB", SourceURL: SourceURL, AsOf: day.Date, FetchedAt: now, BaseCurrency: "JPY", Rates: []Rate{{"JPY", "1"}, {"USD", usdJPY}, {"EUR", eurJPY}}}, nil
}
