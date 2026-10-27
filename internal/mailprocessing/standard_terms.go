package mailprocessing

import (
	"context"
	"fmt"
	"time"

	"github.com/enjoydarts/cycrio/internal/airoutes"
)

// Terms describes what the published standard permits, not an account's
// individually negotiated contract or a guarantee of actual training use.
type Terms struct {
	ID                 string `json:"id"`
	Version            string `json:"version"`
	SourceURL          string `json:"source_url"`
	Summary            string `json:"summary"`
	ZDR                bool   `json:"zdr"`
	TrainingPermitted  bool   `json:"training_permitted"`
	FixedRetentionDays *int   `json:"fixed_retention_days"`
}

func CurrentPrimaryTerms() Terms {
	return Terms{ID: "liquid-standard-2026-09-30", Version: "2026-09-30", SourceURL: "https://www.liquid.ai/terms-conditions", Summary: "Liquid AI標準規約（2026-09-30、§6）では、提供コンテンツが保持され、モデルの学習・微調整・評価・改善へ利用され得るとされています。固定の保持日数は記載されていません。一次D1はZDRではありません。別途の個別契約が優先する場合があります。", TrainingPermitted: true}
}

type StandardPolicyInput struct {
	FullEmailAllowed   *bool  `json:"full_email_allowed,omitempty" nonnull:"true"`
	ExpectedVersion    int64  `json:"expected_version"`
	RuntimeVersion     int64  `json:"runtime_version"`
	ProfileID          string `json:"profile_id"`
	Enabled            bool   `json:"enabled"`
	PrimaryMode        string `json:"primary_mode"`
	PrimaryTermsID     string `json:"primary_terms_id"`
	PrimaryRetention   string `json:"primary_retention"`
	PrimaryConfirmed   bool   `json:"primary_confirmed"`
	SecondaryRouteID   string `json:"secondary_route_id"`
	SecondaryConfirmed bool   `json:"secondary_confirmed"`
}
type StandardProof struct {
	FullEmailAllowed   bool           `json:"full_email_allowed"`
	ProfileID          string         `json:"profile_id"`
	PolicyVersion      int64          `json:"policy_version"`
	PrimaryMode        string         `json:"primary_mode"`
	PrimaryTerms       *Terms         `json:"primary_terms"`
	PrimaryRetention   string         `json:"primary_retention"`
	SecondaryRoute     airoutes.Proof `json:"secondary_route"`
	SecondaryRetention string         `json:"secondary_retention"`
	ActorID            string         `json:"actor_id"`
	ConfirmedAt        time.Time      `json:"confirmed_at"`
}
type StandardTermsState struct {
	Primary        Terms           `json:"primary"`
	SecondaryRoute *airoutes.Proof `json:"secondary_route"`
	// Ready concerns a new standard-policy save. Existing processing permission
	// is evaluated separately, and does not expire with route metadata age.
	Ready         bool   `json:"ready"`
	BlockedReason string `json:"blocked_reason"`
}
type StandardPolicyRepository interface {
	SaveStandardPolicy(context.Context, string, StandardPolicyInput) (PolicyState, error)
}

func ValidStandardPolicy(in StandardPolicyInput) bool {
	if !version(in.RuntimeVersion) || in.RuntimeVersion == 0 || !ValidID(in.SecondaryRouteID) || !in.PrimaryConfirmed || !in.SecondaryConfirmed {
		return false
	}
	primary := in.PrimaryRetention
	switch in.PrimaryMode {
	case "standard":
		if in.PrimaryTermsID != CurrentPrimaryTerms().ID || primary != "" {
			return false
		}
		primary = CurrentPrimaryTerms().Summary
	case "custom":
		if in.PrimaryTermsID != "" {
			return false
		}
	default:
		return false
	}
	return ValidPolicy(PolicyInput{ExpectedVersion: in.ExpectedVersion, ProfileID: in.ProfileID, Enabled: true, PrimaryRetention: primary, SecondaryRetention: "Verified OpenRouter ZDR", PrimaryConfirmed: in.PrimaryConfirmed, SecondaryConfirmed: in.SecondaryConfirmed})
}
func SecondaryRetention(route airoutes.Proof) string {
	return fmt.Sprintf("OpenRouterのZDR配信先（%s）。確認日時 %s。取得元 %s。実行時はZDR必須・データ収集拒否・別配信先への切替禁止。", route.Tag, route.CapturedAt.UTC().Format(time.RFC3339), airoutes.SourceURL)
}
func StandardProofCurrent(p Policy) bool {
	proof := p.StandardProof
	if proof == nil {
		return true // Existing custom policies have no standard confirmation.
	}
	if proof.ProfileID != p.ProfileID || proof.PolicyVersion != p.Version || proof.FullEmailAllowed != p.FullEmailAllowed {
		return false
	}
	if proof.PrimaryMode == "custom" {
		return true
	}
	current := CurrentPrimaryTerms()
	return proof.PrimaryMode == "standard" && proof.PrimaryTerms != nil && proof.PrimaryTerms.ID == current.ID && proof.PrimaryTerms.Version == current.Version
}
func (s *Service) SaveStandardPolicy(ctx context.Context, actor string, in StandardPolicyInput) (PolicyState, error) {
	if !ValidID(actor) || !ValidStandardPolicy(in) {
		return PolicyState{}, ErrInvalid
	}
	repo, ok := s.repo.(StandardPolicyRepository)
	if !ok {
		return PolicyState{}, ErrUnavailable
	}
	out, e := repo.SaveStandardPolicy(ctx, actor, in)
	return out, safe(e)
}
