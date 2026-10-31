package promptquality

import "github.com/enjoydarts/cycrio/internal/ai"

// Fictional edge cases from the review; no original mail or real identities.
func lifecycleCases() []Case {
	prorated := money(recurring(event("HazelBoard", "invoice"), "month"), "1000", "JPY")
	prorated.RecurringAmount, prorated.RecurringCurrency, prorated.ContractKind = str("1500"), str("JPY"), str("recurring")
	cancel := event("MapleRoom", "cancellation")
	cancel.EffectiveDate, cancel.AccessEndDate = str("2026-11-01"), str("2026-11-30")
	fixed := money(event("WillowGuild", "invoice"), "120", "EUR")
	fixed.ContractKind, fixed.AccessEndDate = str("fixed_term"), str("2027-10-31")
	unnamed := event("", "cancellation")
	unnamed.ContractKind = str("recurring")
	unnamed.AccessEndDate = str("2026-12-15")
	maintenance := money(event("CloverTools", "payment"), "4800", "JPY")
	maintenance.ContractKind, maintenance.AccessEndDate = str("fixed_term"), str("2027-09-30")
	reminder := event("RookStudio", "payment_reminder")
	reminder.ContractKind = str("recurring")
	failed := event("桔梗ノート", "payment_failed")
	suspended := event("LarchCloud", "suspension")
	suspended.ContractKind = str("recurring")
	suspended.EffectiveDate = str("2026-10-30")
	unnamedFailure := event("", "payment_failed")
	unnamedFailure.ContractKind = str("recurring")
	return []Case{
		{ID: "r01", Split: "development", Text: "HazelBoardの月額契約の請求書です。通常の継続料金は月額JPY 1500。今回の日割り調整は-500、今回請求合計はJPY 1000です。", Label: "subscription_related", Events: []ai.Event{prorated}},
		{ID: "r02", Split: "development", Text: "MapleRoom: cancellation of your membership contract accepted. Cancellation takes effect 2026-11-01. Access remains available until 2026-11-30.", Label: "subscription_related", Events: []ai.Event{cancel}},
		{ID: "r03", Split: "development", Text: "WillowGuild: invoice issued for your fixed-term membership, total EUR 120. Access ends 2027-10-31. There is no automatic renewal.", Label: "subscription_related", Events: []ai.Event{fixed}},
		{ID: "r04", Split: "development", Text: "Your subscription cancellation has been accepted. You can access your account until 2026-12-15.", Label: "subscription_related", Events: []ai.Event{unnamed}},
		{ID: "r05", Split: "development", Text: "桔梗ノート：ご契約の決済に失敗しました。カード情報を更新してください。", Label: "subscription_related", Events: []ai.Event{failed}},
		{ID: "r06", Split: "development", Text: "RookStudio: payment reminder for your subscription. Your invoice remains unpaid. Please update your payment method.", Label: "subscription_related", Events: []ai.Event{reminder}},
		{ID: "r07", Split: "development", Text: "LarchCloud: your subscription service was suspended on 2026-10-30 due to non-payment. Your account has not been cancelled.", Label: "subscription_related", Events: []ai.Event{suspended}},
		{ID: "r08", Split: "development", Text: "CloverTools保守契約の支払いが完了しました。今回の総額JPY 4800、有効期限は2027-09-30。自動更新はありません。", Label: "subscription_related", Events: []ai.Event{maintenance}},
		{ID: "r09", Split: "holdout", Text: "FlintStage performance ticket: admission on 2026-11-10, total USD 40. Thank you for your purchase.", Label: "not_related"},
		{ID: "r10", Split: "holdout", Text: "30日間の新会員コースをご用意しました。まだお申し込みでない方も、今すぐご入会ください。", Label: "not_related"},
		{ID: "r11", Split: "holdout", Text: "Your subscription may be about to expire. See our plans and offers.", Label: "uncertain"},
		{ID: "r12", Split: "holdout", Text: "Your subscription payment failed. Update the payment method in your account.", Label: "subscription_related", Events: []ai.Event{unnamedFailure}},
	}
}
