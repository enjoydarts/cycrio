package priceresearch

import (
	"context"
	"io"
	"net/http"
	"time"
)

const openAIStandardURL = "https://developers.openai.com/api/docs/pricing.md"

// FetchOpenAIStandardPrices reads public documentation, without API credentials
// or model calls. A missing or changed table must never become an empty price list.
func FetchOpenAIStandardPrices(ctx context.Context) (map[string]Quote, error) {
	return NewClient(nil).openAIStandardPrices(ctx)
}

func (c *HTTPClient) openAIStandardPrices(ctx context.Context) (map[string]Quote, error) {
	c.officialMu.Lock()
	defer c.officialMu.Unlock()
	if len(c.officialPrices) > 0 && time.Now().Before(c.officialExpires) {
		return c.officialPrices, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openAIStandardURL, nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	req.Header.Set("Accept", "text/markdown")
	client := &http.Client{Transport: c.transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResearchBody+1))
	if err != nil || len(raw) > maxResearchBody {
		return nil, ErrUnavailable
	}
	prices := ParseOpenAIStandardPrices(string(raw))
	if len(prices) == 0 {
		return nil, ErrUnavailable
	}
	c.officialPrices = prices
	c.officialExpires = time.Now().Add(5 * time.Minute)
	return prices, nil
}
