package priceresearch

import (
	"strconv"
	"strings"
)

func officialPromptRows(targets []Target, prices map[string]Quote) string {
	var rows strings.Builder
	rows.WriteString("\nVerified Standard USD/1M rows from the official page (null=unpublished):\nmodel|input|output|cached|30m_write|12h_write|pricing_tier_max_input\n")
	value := func(v *string) string {
		if v == nil {
			return "null"
		}
		return *v
	}
	for _, target := range targets {
		q, ok := prices[target.ModelID]
		if !ok {
			continue
		}
		bound := "null"
		if q.MaxInputTokens != nil {
			bound = strconv.FormatInt(*q.MaxInputTokens, 10)
		}
		rows.WriteString(strings.Join([]string{q.ModelID, q.InputPerMillion, q.OutputPerMillion, value(q.CacheReadPerMillion), value(q.CacheWrite30mPerMillion), value(q.CacheWrite12hPerMillion), bound}, "|"))
		rows.WriteByte('\n')
	}
	rows.WriteString("Use these rows for listed models; search to classify other IDs. A pricing tier limit is not the model context window.")
	return rows.String()
}
