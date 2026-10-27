package airuntime

import (
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/google/uuid"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*)?$`)
var monthPattern = regexp.MustCompile(`^(20[0-9]{2}|21[0-9]{2})-(0[1-9]|1[0-2])$`)

func validID(id string) bool    { _, err := uuid.Parse(id); return err == nil }
func validVersion(v int64) bool { return v >= 0 && v < math.MaxInt64 }
func ValidateConfig(in ConfigInput) error {
	if in.RouteID != "" && (!validID(in.RouteID) || in.OpenRouterProvider != "") {
		return ErrInvalid
	}
	if !validVersion(in.ExpectedVersion) || !validID(in.CatalogSettingsID) || in.MaxInputBytes < 1024 || in.MaxInputBytes > 65536 || in.MaxOutputTokens < 64 || in.MaxOutputTokens > 16384 || in.TimeoutSeconds < 1 || in.TimeoutSeconds > 120 || in.OpenRouterProvider != "" && (len(in.OpenRouterProvider) > 128 || !slugPattern.MatchString(in.OpenRouterProvider)) {
		return ErrInvalid
	}
	return nil
}
func MonthRange(month string) (time.Time, time.Time, error) {
	if !monthPattern.MatchString(month) {
		return time.Time{}, time.Time{}, ErrInvalid
	}
	year, _ := strconv.Atoi(month[:4])
	m, _ := strconv.Atoi(month[5:])
	jst := time.FixedZone("Asia/Tokyo", 9*3600)
	from := time.Date(year, time.Month(m), 1, 0, 0, 0, 0, jst)
	return from.UTC(), from.AddDate(0, 1, 0).UTC(), nil
}
func ValidateFilter(f CostFilter) (CostFilter, error) {
	if _, _, err := MonthRange(f.Month); err != nil {
		return f, err
	}
	if f.Limit == 0 {
		f.Limit = 50
	}
	if f.Limit < 1 || f.Limit > 100 || f.Offset < 0 || f.Offset > 1000000 || f.UserID != "" && !validID(f.UserID) || f.ModelID != "" && !validID(f.ModelID) || f.Provider != "" && !modelcatalog.ValidProvider(f.Provider) {
		return f, ErrInvalid
	}
	if f.Scope != "" && f.Scope != "user" && f.Scope != "admin_test" && f.Scope != "system" {
		return f, ErrInvalid
	}
	if f.Stage != "" && f.Stage != "classification" && f.Stage != "extraction" && f.Stage != "match" && f.Stage != "repair" {
		return f, ErrInvalid
	}
	return f, nil
}
