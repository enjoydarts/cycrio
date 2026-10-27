package airuntime

type TestFailure struct {
	HTTPStatus int    `json:"http_status"`
	Reason     string `json:"reason"`
	Hint       string `json:"hint"`
}

// Only fixed status-derived guidance is exposed; provider response text is
// never accepted here or used as a display value.
func TestFailureForStatus(status int) *TestFailure {
	out := &TestFailure{HTTPStatus: status}
	switch status {
	case 401:
		out.Reason = "credentials_rejected"
		out.Hint = "この段階のプロバイダーのAPIキーが拒否されました。APIキー設定で登録内容と有効状態を確認してください。"
	case 402:
		out.Reason = "payment_required"
		out.Hint = "この段階のプロバイダーの残高・支払設定・利用上限を確認してください。"
	case 404:
		out.Reason = "model_route_unavailable"
		out.Hint = "この段階のモデルまたは配信先を利用できません。モデルと配信先の対応条件を更新して確認してください。"
	case 429:
		out.Reason = "rate_limited"
		out.Hint = "この段階のプロバイダーの利用制限に達しました。上限を確認し、時間をおいて明示的に再テストしてください。"
	default:
		return nil
	}
	return out
}
