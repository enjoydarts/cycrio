package reports

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }
func today() time.Time  { return time.Date(2026, 11, 1, 15, 30, 0, 0, time.UTC) }
func contract(id, amount, unit string, count int) Contract {
	return Contract{ID: id, Service: id, Status: "active", ContractKind: "recurring", Amount: ptr(amount), Currency: ptr("JPY"), IntervalUnit: ptr(unit), IntervalCount: ptr(count)}
}
func amount(t *testing.T, got *string, want string) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("amount = %v, want %s", got, want)
	}
}
func TestPeriodUsesJapaneseMonthAndTwelveMonths(t *testing.T) {
	start, end, err := Period(today(), "")
	if err != nil || start.Format("2006-01-02") != "2025-12-01" || end.Format("2006-01-02") != "2026-12-01" || start.Location().String() != "Asia/Tokyo" {
		t.Fatalf("period = %v %v %v", start, end, err)
	}
	for _, month := range []string{"1999-12", "2200-01", "2026-1", "2026-13", "x"} {
		if _, _, err := Period(today(), month); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %q", month)
		}
	}
}
func TestNormalizationPreservesDecimalsAndExclusions(t *testing.T) {
	contracts := []Contract{contract("monthly", "0.1", "month", 3), contract("yearly", "100", "year", 3), contract("weekly", "1", "week", 1), contract("daily", "1", "day", 1), contract("zero", "0", "month", 1), contract("ended", "999", "month", 1), contract("fixed", "999", "month", 1), {ID: "missing", Service: "missing", Status: "unknown", ContractKind: "unknown"}}
	contracts[5].Status = "ended"
	contracts[6].ContractKind = "fixed_term"
	report, err := Build(today(), "", contracts, nil)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Contract{}
	for _, c := range report.Contracts {
		byID[c.ID] = c
	}
	amount(t, byID["monthly"].MonthlyAmount, "0.03333333")
	amount(t, byID["monthly"].AnnualAmount, "0.4")
	amount(t, byID["weekly"].AnnualAmount, "52.14285714")
	amount(t, byID["daily"].MonthlyAmount, "30.41666667")
	amount(t, byID["zero"].MonthlyAmount, "0")
	if byID["ended"].ExclusionReason != "ended" || byID["fixed"].ExclusionReason != "fixed_term" || byID["missing"].ExclusionReason != "missing_amount" {
		t.Fatal(byID)
	}
	amount(t, report.Currencies[0].MonthlyAmount, "37.57301588")
	if report.Coverage.NormalizedContracts != 5 || report.Coverage.UnknownContracts != 1 || report.Coverage.EndedContracts != 1 || report.Coverage.UnpricedContracts != 1 {
		t.Fatal(report.Coverage)
	}
}
func TestUpcomingUsesSavedCalendarDatesAndExclusiveBounds(t *testing.T) {
	dates := []string{"2026-11-01", "2026-11-02", "2026-12-01", "2026-12-02", "2027-11-01", "2027-11-02"}
	var contracts []Contract
	for _, date := range dates {
		c := contract(date, "10", "year", 1)
		c.NextBillingDate = ptr(date)
		contracts = append(contracts, c)
	}
	contracts = append(contracts, contract("undated", "1", "month", 1))
	report, err := Build(today(), "", contracts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Upcoming) != 4 || report.Upcoming[0].Date != "2026-11-02" || !report.Upcoming[0].AnnualRenewal || report.Coverage.OverdueContracts != 1 || report.Coverage.MissingDateContracts != 1 {
		t.Fatal(report)
	}
	amount(t, report.Currencies[0].Next30DaysAmount, "20")
	if report.Currencies[0].Next30DaysCount != 2 {
		t.Fatal(report.Currencies)
	}
}
func TestActualsKeepInvoiceSeparateRefundNegativeAndEmptyNull(t *testing.T) {
	records := []Record{{SubscriptionID: "ended", Service: "Ended", Month: "2026-11", Currency: "USD", Kind: "payment", Amount: "9007199254740993.1", Count: 2}, {SubscriptionID: "ended", Service: "Ended", Month: "2026-11", Currency: "USD", Kind: "refund", Amount: "0.1", Count: 1}, {SubscriptionID: "ended", Service: "Ended", Month: "2026-11", Currency: "USD", Kind: "invoice", Amount: "50", Count: 1}, {SubscriptionID: "zero", Service: "Zero", Month: "2026-11", Currency: "JPY", Kind: "payment", Amount: "0", Count: 1}, {SubscriptionID: "invoice", Service: "Invoice", Month: "2026-10", Currency: "EUR", Kind: "invoice", Amount: "0", Count: 1}}
	report, err := Build(today(), "2026-11", nil, records)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.History) != 12 || len(report.Currencies) != 3 || len(report.MonthlyServices) != 2 || report.Coverage.ActualRecordCount != 6 {
		t.Fatal(report)
	}
	if report.History[0].Currencies[0].NetAmount != nil {
		t.Fatal("empty bucket became zero")
	}
	latest := report.History[11].Currencies
	amount(t, latest[2].NetAmount, "9007199254740993")
	amount(t, latest[2].InvoiceAmount, "50")
	amount(t, latest[1].NetAmount, "0")
	if report.History[10].Currencies[0].NetAmount != nil {
		t.Fatal("invoice became payment")
	}
	if report.Currencies[0].MonthlyAmount != nil {
		t.Fatal("actual-only currency became priced contract")
	}
}
func TestBuildIsDeterministicAndDoesNotMutateInputs(t *testing.T) {
	contracts := []Contract{contract("z", "1", "month", 1), contract("a", "2", "month", 1)}
	copyInput := append([]Contract(nil), contracts...)
	first, err := Build(today(), "", contracts, nil)
	if err != nil {
		t.Fatal(err)
	}
	contracts[0], contracts[1] = contracts[1], contracts[0]
	second, err := Build(today(), "", contracts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || copyInput[0].MonthlyAmount != nil {
		t.Fatal("unstable or input mutated")
	}
}

func TestActualsAndUpcomingRetainTwelveDigitPrecisionAndLargeSums(t *testing.T) {
	c := contract("tiny", "0.000000000001", "month", 1)
	c.NextBillingDate = ptr("2026-11-02")
	records := []Record{{SubscriptionID: "large", Service: "Large", Month: "2026-11", Currency: "USD", Kind: "payment", Amount: "1999999999999999999999998.000000000001", Count: 2}, {SubscriptionID: "large", Service: "Large", Month: "2026-11", Currency: "USD", Kind: "refund", Amount: "0.000000000001", Count: 1}}
	report, err := Build(today(), "", []Contract{c}, records)
	if err != nil {
		t.Fatal(err)
	}
	amount(t, report.Currencies[0].Next30DaysAmount, "0.000000000001")
	actual := report.History[11].Currencies[1]
	amount(t, actual.PaymentAmount, "1999999999999999999999998.000000000001")
	amount(t, actual.RefundAmount, "0.000000000001")
	amount(t, actual.NetAmount, "1999999999999999999999998")
}
func TestInvalidAmountsIntervalsDatesAndRecordsFailSafely(t *testing.T) {
	for _, mutate := range []func(*Contract){func(c *Contract) { c.Amount = ptr("NaN") }, func(c *Contract) { c.IntervalCount = ptr(0) }, func(c *Contract) { c.IntervalUnit = ptr("quarter") }, func(c *Contract) { c.NextBillingDate = ptr("2026-02-30") }, func(c *Contract) { c.Currency = ptr("bad") }} {
		c := contract("id", "1", "month", 1)
		mutate(&c)
		if _, err := Build(today(), "", []Contract{c}, nil); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid contract accepted: %+v %v", c, err)
		}
	}
	for _, record := range []Record{{Month: "2026-11", Currency: "USD", Kind: "payment", Amount: "-1", Count: 1}, {Month: "2026-11", Currency: "USD", Kind: "payment", Amount: "1", Count: 0}, {Month: "bad", Currency: "USD", Kind: "payment", Amount: "1", Count: 1}, {Month: "2026-11", Currency: "USD", Kind: "other", Amount: "1", Count: 1}} {
		if _, err := Build(today(), "", nil, []Record{record}); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid record accepted", record, err)
		}
	}
}
