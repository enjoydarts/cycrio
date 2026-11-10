package notifications

import "testing"

func TestGeneratedEmailMatcherDoesNotExcludeMerchantEventsOrQuotedNotifications(t *testing.T) {
	for _, text := range []string{
		"", "Cycrio", "サブスクの更新日が近づいています",
		"Syntheticの更新が完了しました。2026-11-12に980 JPYを請求しました。",
		"Cycrio Proの決済が完了しました。980 JPY。",
		"Auto-Submitted: auto-generated\nSynthetic 請求書 980 JPY",
		"請求書\n今回の請求 980 JPY\n\nサブスクの更新日が近づいています\nSynthetic\n登録された次回請求日: 2026-11-12\n通常継続料金は未確認です。\n実際の請求日や契約内容を確認してください。",
		"このメールはCycrioの登録情報に基づく自動通知です。\n別の本文",
	} {
		if IsGeneratedEmailText([]byte(text)) {
			t.Fatal("通常のメールや引用を除外した", text)
		}
	}
}
