package notifications

import (
	"html"
	"strconv"
	"strings"
)

// RenderEmail accepts enabled, structured notification facts only. It does not
// authorize delivery: verified ownership, current preferences, and delivery
// receipts must still be checked by the sending transaction.
func RenderEmail(c Candidate) (EmailContent, error) {
	if !c.Email || c.ScheduledAt.IsZero() || !validDate(c.DueDate) || !validMoney(c.Amount, c.Currency) {
		return EmailContent{}, ErrInvalid
	}
	var title, message string
	if c.Kind != PendingReview && (c.ReviewRunID != nil || c.ReviewCount != nil) {
		return EmailContent{}, ErrInvalid
	}
	switch c.Kind {
	case PendingReview:
		if c.ReviewRunID == nil || c.ReviewCount == nil || *c.ReviewCount < 1 || c.ContractID != nil || c.AccountID != nil || c.ResultID != nil || c.EventIndex != nil || c.EpisodeID != nil || c.Service != "" || c.DueDate != nil || c.Amount != nil || c.Currency != nil || c.OldAmount != nil || c.NewAmount != nil || c.DateSource != "" {
			return EmailContent{}, ErrInvalid
		}
		id, e := normalizeID(*c.ReviewRunID)
		if e != nil || c.SourceKey != string(PendingReview)+":"+id {
			return EmailContent{}, ErrInvalid
		}
		title = "新しい確認待ちがあります"
		message = "解析後の通知作成時点で" + strconv.Itoa(*c.ReviewCount) + "件の新しい確認待ちがありました。Cycrioで現在の確認待ちを確認してください。"
	case Renewal, AnnualCharge, TrialEnd, PriceIncrease:
		if c.ContractID == nil || !label(c.Service) {
			return EmailContent{}, ErrInvalid
		}
		id, e := normalizeID(*c.ContractID)
		if e != nil {
			return EmailContent{}, e
		}
		if c.AccountID != nil || c.EpisodeID != nil {
			return EmailContent{}, ErrInvalid
		}
		if c.Kind == PriceIncrease {
			if c.ResultID == nil || c.EventIndex == nil || *c.EventIndex < 0 || *c.EventIndex > 31 {
				return EmailContent{}, ErrInvalid
			}
			result, e := normalizeID(*c.ResultID)
			if e != nil {
				return EmailContent{}, e
			}
			increased, e := NativePriceIncrease(c.OldAmount, c.Currency, c.NewAmount, c.Currency)
			if e != nil || !increased || c.DueDate != nil || c.SourceKey != priceKey(id, result, *c.EventIndex) {
				return EmailContent{}, ErrInvalid
			}
			title = "通常継続料金の変更を確認してください"
			message = c.Service + "の通常継続料金に値上げ候補があります。\n現在の登録料金: " + *c.OldAmount + " " + *c.Currency + "\n抽出された変更候補: " + *c.NewAmount + " " + *c.Currency + "\n契約内容は承認するまで変更されません。"
		} else {
			if c.DueDate == nil || c.SourceKey != occurrenceKey(c.Kind, id, *c.DueDate) || c.ResultID != nil || c.EventIndex != nil {
				return EmailContent{}, ErrInvalid
			}
			if c.Kind == TrialEnd {
				if c.DateSource != "explicit_trial_end" {
					return EmailContent{}, ErrInvalid
				}
				title = "無料期間の終了日が近づいています"
				message = c.Service + "の登録された無料期間終了日: " + *c.DueDate + "\n終了後の料金や契約内容を確認してください。"
			} else {
				if c.DateSource != "manual" && c.DateSource != "receipt_estimate" {
					return EmailContent{}, ErrInvalid
				}
				title = "サブスクの更新日が近づいています"
				if c.Kind == AnnualCharge {
					title = "年払いの日付が近づいています"
				}
				dateLabel := "登録された次回請求日"
				if c.DateSource == "receipt_estimate" {
					dateLabel = "メール受信日を起点にした参考日"
				}
				money := "通常継続料金は未確認です。"
				if c.Amount != nil && c.Currency != nil {
					money = "登録された通常継続料金: " + *c.Amount + " " + *c.Currency
				}
				message = c.Service + "\n" + dateLabel + ": " + *c.DueDate + "\n" + money + "\n実際の請求日や契約内容を確認してください。"
			}
		}
	case ImportError:
		if c.AccountID == nil || c.EpisodeID == nil || c.ContractID != nil || c.ResultID != nil || c.EventIndex != nil || c.DueDate != nil || c.Service != "" {
			return EmailContent{}, ErrInvalid
		}
		account, e := normalizeID(*c.AccountID)
		if e != nil {
			return EmailContent{}, e
		}
		episode, e := normalizeID(*c.EpisodeID)
		if e != nil || c.SourceKey != episodeKey(account, episode) {
			return EmailContent{}, ErrInvalid
		}
		title = "メールの取り込み状況を確認してください"
		message = "メールの取り込みで確認が必要なエラーが発生しています。アプリで接続設定と取り込み状況を確認してください。"
	default:
		return EmailContent{}, ErrInvalid
	}
	text := EmailNotice + "\n\n" + title + "\n\n" + message
	body := "<p>" + html.EscapeString(EmailNotice) + "</p><p>" + html.EscapeString(title) + "</p><p>" + strings.ReplaceAll(html.EscapeString(message), "\n", "<br>") + "</p>"
	return EmailContent{Subject: "Cycrio: " + title, Text: text, HTML: body}, nil
}
