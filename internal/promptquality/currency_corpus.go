package promptquality

import "github.com/enjoydarts/cycrio/internal/ai"

// CurrencyCorpus is a separate synthetic suite; it never increases the default
// 176-call evaluation budget or includes real user mail.
func CurrencyCorpus() []Case {
	makeCase := func(id, text, amount, recurring string, currency, recurringCurrency *string) Case {
		e := recurringEvent(amount, recurring, currency, recurringCurrency)
		return Case{ID: id, Split: "currency", Text: text, Label: "subscription_related", Events: []ai.Event{e}}
	}
	return []Case{
		makeCase("currency-yen", "MapleBoxの月額契約。通常月額1,100円。2026-11-01のお支払いが完了しました。今回の支払総額1,100円（税込）。", "1100", "1100", str("JPY"), str("JPY")),
		makeCase("currency-us-dollar", "MapleBox monthly subscription. Regular monthly fee US$12.00. Payment completed 2026-11-01; total paid US$12.00.", "12", "12", str("USD"), str("USD")),
		makeCase("currency-context-cad", "MapleBox monthly subscription. Billing currency: CAD. Regular monthly fee $12.00. Payment completed 2026-11-01; total paid $12.00.", "12", "12", str("CAD"), str("CAD")),
		makeCase("currency-bare-dollar", "MapleBox monthly subscription. Regular monthly fee $12.00. Payment completed 2026-11-01; total paid $12.00.", "12", "12", nil, nil),
		makeCase("currency-bare-yen", "MapleBox monthly subscription. Regular monthly fee ¥1200. Payment completed 2026-11-01; total paid ¥1200.", "1200", "1200", nil, nil),
		makeCase("currency-context-cny", "MapleBox monthly subscription. Billing currency: CNY. Regular monthly fee ¥1200. Payment completed 2026-11-01; total paid ¥1200.", "1200", "1200", str("CNY"), str("CNY")),
		makeCase("currency-separate-scopes", "MapleBoxの月額契約。通常の継続料金は月額US$10。2026-11-01の今回決済は日本円で完了しました。支払総額1,100円。", "1100", "10", str("JPY"), str("USD")),
		makeCase("currency-euro", "MapleBox monthly subscription. Regular monthly fee €9.99. Payment completed 2026-11-01; total paid €9.99.", "9.99", "9.99", str("EUR"), str("EUR")),
		makeCase("currency-quoted-price", "MapleBoxの月額契約。通常月額1,100円。2026-11-01の支払総額は1,100円です。以下は引用した他社の広告です：月額USD 999の新プランを今すぐ申し込みませんか。", "1100", "1100", str("JPY"), str("JPY")),
		makeCase("currency-fullwidth-yen", "MapleBoxの月額契約。通常月額１，１００円。2026-11-01のお支払いが完了しました。今回の支払総額１，１００円（税込）。", "1100", "1100", str("JPY"), str("JPY")),
	}
}

func recurringEvent(amount, regular string, currency, regularCurrency *string) ai.Event {
	e := recurring(event("MapleBox", "payment"), "month")
	e.Amount, e.Currency = str(amount), currency
	e.RecurringAmount, e.RecurringCurrency = str(regular), regularCurrency
	e.ContractKind, e.BilledAt = str("recurring"), str("2026-11-01")
	return e
}
