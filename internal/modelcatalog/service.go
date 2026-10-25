package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type Service struct {
	repo    Repository
	fetcher *Fetcher
}

func NewService(repo Repository, fetcher *Fetcher) *Service {
	if fetcher == nil {
		fetcher = NewFetcher(nil, nil)
	}
	return &Service{repo: repo, fetcher: fetcher}
}
func (s *Service) GetModel(ctx context.Context, id string) (Model, error) {
	if !validUUID(id) {
		return Model{}, ErrInvalid
	}
	return s.repo.GetModel(ctx, id)
}
func validUUID(id string) bool  { _, err := uuid.Parse(id); return err == nil }
func validVersion(v int64) bool { return v >= 0 && v < math.MaxInt64 }
func (s *Service) List(ctx context.Context, f Filter) (ListResult, error) {
	if f.Limit == 0 {
		f.Limit = 100
	}
	if f.Limit < 1 || f.Limit > 100 || f.Offset < 0 || f.Provider != "" && !ValidProvider(f.Provider) || !ValidSearch(f.Search) {
		return ListResult{}, ErrInvalid
	}
	return s.repo.List(ctx, f)
}
func ValidSearch(value string) bool {
	return utf8.ValidString(value) && len(value) <= 400 && utf8.RuneCountInString(value) <= 100
}
func (s *Service) AddModel(ctx context.Context, actor string, in ManualModelInput) (Model, error) {
	if !validUUID(actor) || !ValidProvider(in.Provider) || !validText(in.ModelID, 256) || !validText(in.DisplayName, 512) || strings.TrimSpace(in.ModelID) != in.ModelID {
		return Model{}, ErrInvalid
	}
	return s.repo.AddModel(ctx, actor, in)
}
func (s *Service) UpdateModel(ctx context.Context, actor, id string, in ModelUpdate) (Model, error) {
	if !validUUID(actor) || !validUUID(id) || !validVersion(in.ExpectedVersion) || in.DisplayName != "" && !validText(in.DisplayName, 512) {
		return Model{}, ErrInvalid
	}
	return s.repo.UpdateModel(ctx, actor, id, in)
}
func (s *Service) Prices(ctx context.Context, id string) ([]Price, error) {
	if !validUUID(id) {
		return nil, ErrInvalid
	}
	return s.repo.Prices(ctx, id)
}

// ISO 4217 currencies with monetary use; XXX (no currency) is intentionally excluded.
const currencies = " AED AFN ALL AMD ANG AOA ARS AUD AWG AZN BAM BBD BDT BGN BHD BIF BMD BND BOB BOV BRL BSD BTN BWP BYN BZD CAD CDF CHE CHF CHW CLF CLP CNY COP COU CRC CUP CVE CZK DJF DKK DOP DZD EGP ERN ETB EUR FJD FKP GBP GEL GHS GIP GMD GNF GTQ GYD HKD HNL HTG HUF IDR ILS INR IQD IRR ISK JMD JOD JPY KES KGS KHR KMF KPW KRW KWD KYD KZT LAK LBP LKR LRD LSL LYD MAD MDL MGA MKD MMK MNT MOP MRU MUR MVR MWK MXN MXV MYR MZN NAD NGN NIO NOK NPR NZD OMR PAB PEN PGK PHP PKR PLN PYG QAR RON RSD RUB RWF SAR SBD SCR SDG SEK SGD SHP SLE SOS SRD SSP STN SVC SYP SZL THB TJS TMT TND TOP TRY TTD TWD TZS UAH UGX USD USN UYI UYU UYW UZS VED VES VND VUV WST XAF XCD XCG XDR XOF XPF XSU XUA YER ZAR ZMW ZWG "

func validSourceURL(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 512 {
		return false
	}
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}
func (s *Service) SavePrice(ctx context.Context, actor, id string, in ManualPriceInput) (Model, error) {
	if !validUUID(actor) || !validUUID(id) || !validVersion(in.ExpectedVersion) || len(in.Currency) != 3 || !strings.Contains(currencies, " "+in.Currency+" ") || in.SourceURL == "" || !validSourceURL(in.SourceURL) || in.EffectiveFrom == nil || in.EffectiveFrom.IsZero() || in.EffectiveFrom.Year() < 1 || in.EffectiveFrom.Year() > 9999 {
		return Model{}, ErrInvalid
	}
	p := Price{Source: "manual", Currency: in.Currency, Rates: map[string]Rate{}, EffectiveFrom: in.EffectiveFrom, SourceURL: in.SourceURL}
	values := map[string]*string{"input": in.InputPerMillion, "output": in.OutputPerMillion, "cache_read": in.CacheReadPerMillion, "cache_write": in.CacheWritePerMillion, "cache_write_30m": in.CacheWrite30mPerMillion, "cache_write_1h": in.CacheWrite1hPerMillion, "cache_write_12h": in.CacheWrite12hPerMillion, "request": in.PerRequest}
	for key, value := range values {
		if value == nil {
			continue
		}
		if !ValidDecimal(*value) {
			return Model{}, ErrInvalid
		}
		amount := *value
		unit := in.Currency + "/token"
		if key == "request" {
			unit = in.Currency + "/request"
		} else {
			amount, _ = PerMillion(*value)
		}
		p.Rates[key] = Rate{Amount: amount, Unit: unit}
	}
	if len(p.Rates) == 0 {
		return Model{}, ErrInvalid
	}
	p.RawPricing, _ = json.Marshal(in)
	return s.repo.SavePrice(ctx, actor, id, in.ExpectedVersion, p)
}
func (s *Service) ResetPrice(ctx context.Context, actor, id string, expected int64) (Model, error) {
	if !validUUID(actor) || !validUUID(id) || !validVersion(expected) {
		return Model{}, ErrInvalid
	}
	return s.repo.ResetPrice(ctx, actor, id, expected)
}
func (s *Service) SyncStates(ctx context.Context) ([]SyncState, error) { return s.repo.SyncStates(ctx) }
func (s *Service) RequestSync(ctx context.Context, actor, provider string) (SyncState, error) {
	if !validUUID(actor) || !ValidProvider(provider) {
		return SyncState{}, ErrInvalid
	}
	return s.repo.RequestSync(ctx, actor, provider)
}
func (s *Service) ConfigureSync(ctx context.Context, actor, provider string, in SyncConfigInput) (SyncState, error) {
	if !validUUID(actor) || !ValidProvider(provider) || !validVersion(in.ExpectedVersion) || in.IntervalHours < 1 || in.IntervalHours > 168 || !ValidWorkspace(provider, in.WorkspaceID) || provider == "liquid" && in.AutomaticEnabled {
		return SyncState{}, ErrInvalid
	}
	return s.repo.ConfigureSync(ctx, actor, provider, in)
}
func (s *Service) ClaimDue(ctx context.Context) ([]SyncLease, error) { return s.repo.ClaimDue(ctx) }
func (s *Service) SyncProvider(ctx context.Context, provider, leaseID string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if !ValidProvider(provider) || provider == "liquid" || !validUUID(leaseID) {
		return ErrInvalid
	}
	if err := s.repo.ValidateLease(ctx, provider, leaseID); err != nil {
		return err
	}
	states, err := s.repo.SyncStates(ctx)
	workspace := ""
	if err == nil {
		for _, state := range states {
			if state.Provider == provider {
				workspace = state.WorkspaceID
			}
		}
		var entries []CatalogEntry
		entries, err = s.fetcher.Fetch(ctx, provider, workspace)
		if err == nil {
			err = s.repo.ApplySync(ctx, provider, leaseID, entries)
			if err == nil {
				return nil
			}
		}
	}
	if errors.Is(err, ErrConflict) {
		return err
	}
	code := "provider_unavailable"
	if errors.Is(err, ErrCredentialsRequired) {
		code = "credentials_required"
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if finishErr := s.repo.FinishSync(cleanupCtx, provider, leaseID, code); finishErr != nil {
		return finishErr
	}
	if code == "credentials_required" {
		return ErrCredentialsRequired
	}
	return ErrUnavailable
}
func (s *Service) Settings(ctx context.Context) (SettingsState, error) { return s.repo.Settings(ctx) }
func (s *Service) SaveSettings(ctx context.Context, actor string, in SettingsInput) (SettingsState, error) {
	if !validUUID(actor) || !validVersion(in.ExpectedVersion) || !validUUID(in.PrimaryModelID) || !validUUID(in.SecondaryModelID) {
		return SettingsState{}, ErrInvalid
	}
	return s.repo.SaveSettings(ctx, actor, in)
}
