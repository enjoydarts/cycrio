package priceresearch

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

const openAIStandardSource = "https://developers.openai.com/api/docs/pricing"

var standardModelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var standardBoundLabel = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*) \(<([0-9]+)K context length\)$`)
var standardShortBound = regexp.MustCompile(`Short context: ≤([0-9]+)K input tokens\.`)

// ParseOpenAIStandardPrices reads only the first three explicitly named Standard
// data sections. Missing columns, changed layouts and conflicting rows fail closed.
// The caller supplies the official document; this pure parser performs no fetches.
func ParseOpenAIStandardPrices(raw string) map[string]Quote {
	prices := map[string]Quote{}
	if len(raw) > 4*1024*1024 {
		return prices
	}
	var globalBound int64
	for _, match := range standardShortBound.FindAllStringSubmatch(raw, -1) {
		bound := standardTokenBound(match[1])
		if bound == 0 || globalBound != 0 && globalBound != bound {
			return prices
		}
		globalBound = bound
	}
	poisoned := map[string]bool{}
	blocks, active, forbidden := 0, false, false
	var header []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
			lower := strings.ToLower(heading)
			if strings.Contains(lower, "fine-tun") || strings.Contains(lower, "fine tun") {
				forbidden = true
			}
			active = heading == "Standard pricing data" && !forbidden
			if active {
				blocks++
				active = blocks <= 3
			}
			header = nil
			continue
		}
		if line == "Batch" || line == "Flex" || line == "Fast" || line == "Ultrafast" {
			active, header = false, nil
			continue
		}
		if !active || !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
			if line != "" {
				header = nil
			}
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if cells[0] == "Model" {
			header = make([]string, len(cells))
			for i, cell := range cells {
				header[i] = strings.ToLower(cell)
			}
			continue
		}
		if len(header) == 0 || strings.HasPrefix(cells[0], "---") {
			continue
		}
		id, labelBound := cells[0], int64(0)
		if match := standardBoundLabel.FindStringSubmatch(id); match != nil {
			id, labelBound = match[1], standardTokenBound(match[2])
			if labelBound == 0 {
				poisoned[id] = true
				delete(prices, id)
				continue
			}
		}
		if !standardModelID.MatchString(id) {
			continue
		}
		q, ok := standardPriceRow(id, header, cells, labelBound, globalBound)
		if !ok {
			poisoned[id] = true
			delete(prices, id)
			continue
		}
		if poisoned[id] {
			continue
		}
		if previous, exists := prices[id]; exists {
			if !sameStandardPrice(previous, q) {
				poisoned[id] = true
				delete(prices, id)
			}
			continue
		}
		prices[id] = q
	}
	return prices
}

func standardTokenBound(k string) int64 {
	n, err := strconv.ParseInt(k, 10, 64)
	if err != nil || n < 1 || n > 1000000 {
		return 0
	}
	return n * 1000
}

func standardPriceRow(id string, header, cells []string, labelBound, globalBound int64) (Quote, bool) {
	q := Quote{ModelID: id, Provider: "openai", ModelKind: "text_generation", Currency: "USD", Conditions: "standard_global", SourceURL: openAIStandardSource}
	if len(header) != len(cells) {
		return q, false
	}
	columns := map[string]string{}
	for i, name := range header {
		if _, exists := columns[name]; exists {
			return q, false
		}
		columns[name] = cells[i]
	}
	_, short := columns["short context input"]
	prefix := ""
	if short {
		prefix = "short context "
		bound := globalBound
		if labelBound != 0 {
			if bound != 0 && bound != labelBound {
				return q, false
			}
			bound = labelBound
		}
		if bound == 0 {
			return q, false
		}
		q.MaxInputTokens = &bound
	} else if labelBound != 0 {
		q.MaxInputTokens = &labelBound
	}
	for name := range columns {
		switch name {
		case "model", prefix + "input", prefix + "output", prefix + "cached input", prefix + "30m write", prefix + "12h write":
		default:
			if !short || name != "long context input" && name != "long context output" && name != "long context cached input" && name != "long context 30m write" && name != "long context 12h write" {
				return q, false
			}
		}
	}
	input, ok := standardDollarAmount(columns[prefix+"input"])
	if !ok {
		return q, false
	}
	output, ok := standardDollarAmount(columns[prefix+"output"])
	if !ok {
		return q, false
	}
	q.InputPerMillion, q.OutputPerMillion = input, output
	for name, dst := range map[string]**string{prefix + "cached input": &q.CacheReadPerMillion, prefix + "30m write": &q.CacheWrite30mPerMillion, prefix + "12h write": &q.CacheWrite12hPerMillion} {
		value, exists := columns[name]
		if !exists || value == "-" {
			continue
		}
		amount, ok := standardDollarAmount(value)
		if !ok {
			return q, false
		}
		*dst = &amount
	}
	return q, true
}

func standardDollarAmount(value string) (string, bool) {
	if !strings.HasPrefix(value, "$") {
		return "", false
	}
	amount := strings.TrimPrefix(value, "$")
	return amount, modelcatalog.ValidDecimal(amount)
}

func sameStandardDecimal(a, b string) bool {
	left, leftErr := modelcatalog.PerMillion(a)
	right, rightErr := modelcatalog.PerMillion(b)
	return leftErr == nil && rightErr == nil && left == right
}

func sameStandardBound(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func sameStandardPrice(a, b Quote) bool {
	if !sameStandardDecimal(a.InputPerMillion, b.InputPerMillion) || !sameStandardDecimal(a.OutputPerMillion, b.OutputPerMillion) || !sameStandardBound(a.MaxInputTokens, b.MaxInputTokens) {
		return false
	}
	for _, values := range [][2]*string{{a.CacheReadPerMillion, b.CacheReadPerMillion}, {a.CacheWrite30mPerMillion, b.CacheWrite30mPerMillion}, {a.CacheWrite12hPerMillion, b.CacheWrite12hPerMillion}} {
		if values[0] == nil && values[1] == nil {
			continue
		}
		if values[0] == nil || values[1] == nil || !sameStandardDecimal(*values[0], *values[1]) {
			return false
		}
	}
	return true
}

// MatchOpenAIStandardQuote requires exact identity, context and ordinary pricing.
// Optional cache prices may be omitted but may never contradict the table.
func MatchOpenAIStandardQuote(q Quote, official map[string]Quote) bool {
	ref, ok := official[q.ModelID]
	if !ok || q.Provider != "openai" || q.ModelKind != "text_generation" || q.Currency != "USD" || q.Conditions != "standard_global" || !sameStandardBound(q.MaxInputTokens, ref.MaxInputTokens) || !sameStandardDecimal(q.InputPerMillion, ref.InputPerMillion) || !sameStandardDecimal(q.OutputPerMillion, ref.OutputPerMillion) {
		return false
	}
	if _, ok := officialURL(q.SourceURL, "openai"); !ok {
		return false
	}
	for _, values := range [][2]*string{{q.CacheReadPerMillion, ref.CacheReadPerMillion}, {q.CacheWritePerMillion, ref.CacheWritePerMillion}, {q.CacheWrite30mPerMillion, ref.CacheWrite30mPerMillion}, {q.CacheWrite1hPerMillion, ref.CacheWrite1hPerMillion}, {q.CacheWrite12hPerMillion, ref.CacheWrite12hPerMillion}} {
		if values[0] != nil && (values[1] == nil || !sameStandardDecimal(*values[0], *values[1])) {
			return false
		}
	}
	return true
}
