package priceresearch

import "strings"

// Keep related identifiers in the same pricing-page lookup. This only orders
// targets: names never establish a model's kind, price or snapshot equivalence.
func groupResearchFamilies(targets []Target) []Target {
	groups := map[string][]Target{}
	order := []string{}
	for _, target := range targets {
		parts := strings.Split(target.ModelID, "-")
		family := parts[0]
		if len(parts) > 1 && (parts[0] == "gpt" || parts[0] == "text") {
			family = strings.Join(parts[:2], "-")
		}
		if target.Provider == "anthropic" {
			for _, part := range parts {
				if part == "sonnet" || part == "haiku" || part == "opus" {
					family = part
					break
				}
			}
		}
		if _, ok := groups[family]; !ok {
			order = append(order, family)
		}
		groups[family] = append(groups[family], target)
	}
	out := make([]Target, 0, len(targets))
	for _, family := range order {
		out = append(out, groups[family]...)
	}
	return out
}
