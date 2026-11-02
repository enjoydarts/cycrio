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

// Version is the date these published terms were reviewed, not an account's
// negotiated retention setting. Neither direct API is assumed to have ZDR.
func CurrentSecondaryTerms(provider string) (Terms, bool) {
	switch provider {
	case "anthropic":
		return Terms{ID: "anthropic-api-standard-2026-11-02", Version: "2026-11-02確認", SourceURL: "https://privacy.claude.com/en/articles/7996866-how-long-do-you-store-my-organization-s-data", Summary: "Anthropic商用APIの入力・出力は通常30日以内に削除すると案内されています。モデル・機能・法令・安全性対応・個別契約による例外があります。標準では学習に利用せず、明示的な同意・フィードバック等は例外です。アカウント固有のZDR適用を確認したものではありません。"}, true
	case "openai":
		return Terms{ID: "openai-api-standard-2026-11-02", Version: "2026-11-02確認", SourceURL: "https://developers.openai.com/api/docs/guides/your-data", Summary: "OpenAI APIの入力・出力は標準では学習に利用されません（明示的な同意がある場合を除きます）。不正利用監視ログは通常最大30日保持され、法令・安全性対応や利用機能による例外があります。store:falseだけでは全ログの無保持にはなりません。アカウント固有のZDR適用を確認したものではありません。"}, true
	default:
		return Terms{}, false
	}
}

func secondaryTermsByID(id string) (Terms, bool) {
	for _, provider := range []string{"anthropic", "openai"} {
		if terms, ok := CurrentSecondaryTerms(provider); ok && terms.ID == id {
			return terms, true
		}
	}
	return Terms{}, false
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
	SecondaryMode      string `json:"secondary_mode,omitempty"`
	SecondaryTermsID   string `json:"secondary_terms_id,omitempty"`
	SecondaryRetention string `json:"secondary_retention,omitempty"`
}
type StandardProof struct {
	FullEmailAllowed   bool            `json:"full_email_allowed"`
	ProfileID          string          `json:"profile_id"`
	PolicyVersion      int64           `json:"policy_version"`
	PrimaryMode        string          `json:"primary_mode"`
	PrimaryTerms       *Terms          `json:"primary_terms"`
	PrimaryRetention   string          `json:"primary_retention"`
	SecondaryRoute     *airoutes.Proof `json:"secondary_route"`
	SecondaryMode      string          `json:"secondary_mode,omitempty"`
	SecondaryTerms     *Terms          `json:"secondary_terms,omitempty"`
	SecondaryRetention string          `json:"secondary_retention"`
	ActorID            string          `json:"actor_id"`
	ConfirmedAt        time.Time       `json:"confirmed_at"`
}
type StandardTermsState struct {
	Primary        Terms           `json:"primary"`
	SecondaryRoute *airoutes.Proof `json:"secondary_route"`
	Secondary      *Terms          `json:"secondary,omitempty"`
	// Ready concerns a new standard-policy save. Existing processing permission
	// is evaluated separately, and does not expire with route metadata age.
	Ready         bool   `json:"ready"`
	BlockedReason string `json:"blocked_reason"`
}
type StandardPolicyRepository interface {
	SaveStandardPolicy(context.Context, string, StandardPolicyInput) (PolicyState, error)
}

func (in StandardPolicyInput) SecondaryPolicyMode() string {
	if in.SecondaryMode == "" {
		return "route" // Existing clients confirm an OpenRouter route.
	}
	return in.SecondaryMode
}

func ValidStandardPolicy(in StandardPolicyInput) bool {
	if !version(in.RuntimeVersion) || in.RuntimeVersion == 0 || !in.PrimaryConfirmed || !in.SecondaryConfirmed {
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
	secondary := in.SecondaryRetention
	switch in.SecondaryPolicyMode() {
	case "route":
		if !ValidID(in.SecondaryRouteID) || in.SecondaryTermsID != "" || secondary != "" {
			return false
		}
		secondary = "Verified OpenRouter ZDR"
	case "standard":
		terms, ok := secondaryTermsByID(in.SecondaryTermsID)
		if !ok || in.SecondaryRouteID != "" || secondary != "" {
			return false
		}
		secondary = terms.Summary
	case "custom":
		if in.SecondaryRouteID != "" || in.SecondaryTermsID != "" {
			return false
		}
	default:
		return false
	}
	return ValidPolicy(PolicyInput{ExpectedVersion: in.ExpectedVersion, ProfileID: in.ProfileID, Enabled: true, PrimaryRetention: primary, SecondaryRetention: secondary, PrimaryConfirmed: in.PrimaryConfirmed, SecondaryConfirmed: in.SecondaryConfirmed})
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
	if proof.PrimaryMode != "custom" {
		current := CurrentPrimaryTerms()
		if proof.PrimaryMode != "standard" || proof.PrimaryTerms == nil || proof.PrimaryTerms.ID != current.ID || proof.PrimaryTerms.Version != current.Version {
			return false
		}
	}
	switch proof.SecondaryMode {
	case "", "route", "custom":
		return true
	case "standard":
		if proof.SecondaryTerms == nil {
			return false
		}
		current, ok := secondaryTermsByID(proof.SecondaryTerms.ID)
		return ok && proof.SecondaryTerms.Version == current.Version
	default:
		return false
	}
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
