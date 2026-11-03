package reports

import (
	"math/big"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"
)

var (
	japaneseTime         = mustJapaneseTime()
	decimalPattern       = regexp.MustCompile(`^[0-9]{1,24}(\.[0-9]{1,12})?$`)
	actualDecimalPattern = regexp.MustCompile(`^[0-9]{1,64}(\.[0-9]{1,12})?$`)
	currencyPattern      = regexp.MustCompile(`^[A-Z]{3}$`)
	monthPattern         = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}$`)
)

func mustJapaneseTime() *time.Location {
	location, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		panic(err)
	}
	return location
}

// Period returns twelve Japanese calendar months ending after the selected month.
func Period(now time.Time, month string) (time.Time, time.Time, error) {
	if month == "" {
		month = now.In(japaneseTime).Format("2006-01")
	}
	selected, err := time.ParseInLocation("2006-01", month, japaneseTime)
	if err != nil || !monthPattern.MatchString(month) || selected.Year() < 2000 || selected.Year() > 2199 {
		return time.Time{}, time.Time{}, ErrInvalid
	}
	return selected.AddDate(0, -11, 0), selected.AddDate(0, 1, 0), nil
}

// Build derives estimates from current contracts and actual totals from ledger records.
// A missing amount remains nil; only an observed zero is represented as zero.
func Build(now time.Time, month string, contracts []Contract, records []Record) (Report, error) {
	start, end, err := Period(now, month)
	if err != nil {
		return Report{}, err
	}
	localNow := now.In(japaneseTime)
	date := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, japaneseTime)
	report := Report{AsOf: date.Format("2006-01-02"), GeneratedAt: now.UTC(), Timezone: "Asia/Tokyo", Month: end.AddDate(0, -1, 0).Format("2006-01"), HistoryStart: start.Format("2006-01-02"), HistoryEnd: end.Format("2006-01-02"), Contracts: []Contract{}, Currencies: []CurrencySummary{}, History: []HistoryBucket{}, MonthlyServices: []MonthlyService{}, Upcoming: []Due{}}
	summaries := map[string]*CurrencySummary{}
	ensureCurrency := func(currency string) *CurrencySummary {
		if summaries[currency] == nil {
			summaries[currency] = &CurrencySummary{Currency: currency}
		}
		return summaries[currency]
	}
	for _, input := range contracts {
		c := input
		c.MonthlyAmount = nil
		c.AnnualAmount = nil
		c.ExclusionReason = ""
		if err := validateContract(c); err != nil {
			return Report{}, err
		}
		report.Coverage.TotalContracts++
		switch c.Status {
		case "active":
			report.Coverage.ActiveContracts++
		case "unknown":
			report.Coverage.UnknownContracts++
		case "ended":
			report.Coverage.EndedContracts++
		}
		if c.ContractKind == "fixed_term" {
			report.Coverage.FixedTermContracts++
		}
		switch {
		case c.Status == "ended":
			c.ExclusionReason = "ended"
		case c.ContractKind == "fixed_term":
			c.ExclusionReason = "fixed_term"
		case c.Amount == nil:
			c.ExclusionReason = "missing_amount"
		case c.Currency == nil:
			c.ExclusionReason = "missing_currency"
		case c.IntervalUnit == nil || c.IntervalCount == nil:
			c.ExclusionReason = "missing_interval"
		default:
			annual := rational(*c.Amount)
			count := new(big.Rat).SetInt64(int64(*c.IntervalCount))
			switch *c.IntervalUnit {
			case "month":
				annual.Mul(annual, big.NewRat(12, 1))
			case "week":
				annual.Mul(annual, big.NewRat(365, 7))
			case "day":
				annual.Mul(annual, big.NewRat(365, 1))
			}
			annual.Quo(annual, count)
			c.AnnualAmount = formatted(annual)
			c.MonthlyAmount = formatted(new(big.Rat).Quo(annual, big.NewRat(12, 1)))
			summary := ensureCurrency(*c.Currency)
			add(&summary.MonthlyAmount, *c.MonthlyAmount)
			add(&summary.AnnualAmount, *c.AnnualAmount)
			summary.ContractCount++
			report.Coverage.NormalizedContracts++
		}
		if c.Currency != nil {
			ensureCurrency(*c.Currency)
		}
		if c.Status != "ended" {
			report.Coverage.OpenContracts++
		}
		if c.Status != "ended" && c.ContractKind != "fixed_term" {
			if c.MonthlyAmount == nil {
				report.Coverage.UnpricedContracts++
			}
			if c.NextBillingDate == nil {
				report.Coverage.MissingDateContracts++
			} else {
				due, _ := time.ParseInLocation("2006-01-02", *c.NextBillingDate, japaneseTime)
				if due.Before(date) {
					report.Coverage.OverdueContracts++
				} else if due.Before(date.AddDate(0, 0, 365)) {
					report.Upcoming = append(report.Upcoming, Due{DisplayName: c.DisplayName, NextBillingMode: c.NextBillingMode, SubscriptionID: c.ID, Service: c.Service, Plan: c.Plan, Date: *c.NextBillingDate, Amount: c.Amount, Currency: c.Currency, AnnualRenewal: c.IntervalUnit != nil && *c.IntervalUnit == "year", Status: c.Status})
					if c.Currency != nil && due.Before(date.AddDate(0, 0, 30)) {
						summary := ensureCurrency(*c.Currency)
						summary.Next30DaysCount++
						if c.Amount != nil {
							add(&summary.Next30DaysAmount, *c.Amount)
						}
					}
				}
			}
		}
		report.Contracts = append(report.Contracts, c)
	}
	history := map[string]map[string]*ActualCurrency{}
	services := map[string]*MonthlyService{}
	for _, record := range records {
		recordMonth, err := time.ParseInLocation("2006-01", record.Month, japaneseTime)
		if err != nil || !monthPattern.MatchString(record.Month) || !currencyPattern.MatchString(record.Currency) || !actualDecimalPattern.MatchString(record.Amount) || record.Count < 1 || !slices.Contains([]string{"payment", "refund", "invoice"}, record.Kind) {
			return Report{}, ErrInvalid
		}
		if recordMonth.Before(start) || !recordMonth.Before(end) {
			continue
		}
		ensureCurrency(record.Currency)
		if history[record.Month] == nil {
			history[record.Month] = map[string]*ActualCurrency{}
		}
		if history[record.Month][record.Currency] == nil {
			history[record.Month][record.Currency] = &ActualCurrency{Currency: record.Currency}
		}
		aggregate(history[record.Month][record.Currency], record)
		report.Coverage.ActualRecordCount += record.Count
		if record.Month == report.Month {
			key := record.SubscriptionID + "\x00" + record.Currency
			if services[key] == nil {
				services[key] = &MonthlyService{DisplayName: record.DisplayName, SubscriptionID: record.SubscriptionID, Service: record.Service, Plan: record.Plan, ActualCurrency: ActualCurrency{Currency: record.Currency}}
			}
			aggregate(&services[key].ActualCurrency, record)
		}
	}
	currencies := make([]string, 0, len(summaries))
	for currency := range summaries {
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)
	for _, currency := range currencies {
		report.Currencies = append(report.Currencies, *summaries[currency])
	}
	for cursor := start; cursor.Before(end); cursor = cursor.AddDate(0, 1, 0) {
		bucket := HistoryBucket{Month: cursor.Format("2006-01"), Currencies: []ActualCurrency{}}
		for _, currency := range currencies {
			actual := ActualCurrency{Currency: currency}
			if found := history[bucket.Month][currency]; found != nil {
				actual = *found
			}
			bucket.Currencies = append(bucket.Currencies, actual)
		}
		report.History = append(report.History, bucket)
	}
	for _, service := range services {
		report.MonthlyServices = append(report.MonthlyServices, *service)
	}
	sort.Slice(report.Contracts, func(i, j int) bool {
		a, b := report.Contracts[i], report.Contracts[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		return a.ID < b.ID
	})
	sort.Slice(report.MonthlyServices, func(i, j int) bool {
		a, b := report.MonthlyServices[i], report.MonthlyServices[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.SubscriptionID != b.SubscriptionID {
			return a.SubscriptionID < b.SubscriptionID
		}
		return a.Currency < b.Currency
	})
	sort.Slice(report.Upcoming, func(i, j int) bool {
		a, b := report.Upcoming[i], report.Upcoming[j]
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		return a.SubscriptionID < b.SubscriptionID
	})
	return report, nil
}

func validateContract(c Contract) error {
	if !slices.Contains([]string{"active", "unknown", "ended"}, c.Status) || !slices.Contains([]string{"recurring", "unknown", "fixed_term"}, c.ContractKind) {
		return ErrInvalid
	}
	if c.Amount != nil && !decimalPattern.MatchString(*c.Amount) || c.Currency != nil && !currencyPattern.MatchString(*c.Currency) {
		return ErrInvalid
	}
	if c.IntervalUnit != nil && !slices.Contains([]string{"day", "week", "month", "year"}, *c.IntervalUnit) || c.IntervalCount != nil && (*c.IntervalCount < 1 || *c.IntervalCount > 10000) {
		return ErrInvalid
	}
	if c.NextBillingDate != nil {
		d, err := time.Parse("2006-01-02", *c.NextBillingDate)
		if err != nil || d.Year() < 1 || d.Format("2006-01-02") != *c.NextBillingDate {
			return ErrInvalid
		}
	}
	return nil
}

func rational(value string) *big.Rat { number, _ := new(big.Rat).SetString(value); return number }

func formatted(value *big.Rat) *string {
	return formatDecimal(value, 8)
}

func formatDecimal(value *big.Rat, digits int) *string {
	result := strings.TrimRight(strings.TrimRight(value.FloatString(digits), "0"), ".")
	if result == "-0" {
		result = "0"
	}
	return &result
}

func add(target **string, value string) {
	sum := rational(value)
	if *target != nil {
		sum.Add(sum, rational(**target))
	}
	*target = formatDecimal(sum, 12)
}

func aggregate(target *ActualCurrency, record Record) {
	switch record.Kind {
	case "payment":
		add(&target.PaymentAmount, record.Amount)
		target.PaymentCount += record.Count
	case "refund":
		add(&target.RefundAmount, record.Amount)
		target.RefundCount += record.Count
	case "invoice":
		add(&target.InvoiceAmount, record.Amount)
		target.InvoiceCount += record.Count
	}
	if target.PaymentAmount != nil || target.RefundAmount != nil {
		net := new(big.Rat)
		if target.PaymentAmount != nil {
			net.Add(net, rational(*target.PaymentAmount))
		}
		if target.RefundAmount != nil {
			net.Sub(net, rational(*target.RefundAmount))
		}
		target.NetAmount = formatDecimal(net, 12)
	}
}
