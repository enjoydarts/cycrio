package notifications

import (
	"regexp"
	"strings"
)

// EmailNotice identifies reminders based on registered facts, not new merchant events.
const EmailNotice = "このメールはCycrioの登録情報に基づく自動通知です。請求や契約変更の確定通知ではありません。"

// Keep legacy templates recognizable as well: previously delivered mail has no
// identifying notice. Match the entire normalized body, never a subject, brand,
// generic auto-generated header, or a quoted reminder within another message.
var generatedEmailBody = regexp.MustCompile(`\A(?:` +
	`(?:サブスクの更新日が近づいています|年払いの日付が近づいています) .{1,200} (?:登録された次回請求日|メール受信日を起点にした参考日): [0-9]{4}-[0-9]{2}-[0-9]{2} (?:登録された通常継続料金: [0-9]+(?:\.[0-9]+)? [A-Z]{3}|通常継続料金は未確認です。) 実際の請求日や契約内容を確認してください。|` +
	`無料期間の終了日が近づいています .{1,200}の登録された無料期間終了日: [0-9]{4}-[0-9]{2}-[0-9]{2} 終了後の料金や契約内容を確認してください。|` +
	`通常継続料金の変更を確認してください .{1,200}の通常継続料金に値上げ候補があります。 現在の登録料金: [0-9]+(?:\.[0-9]+)? [A-Z]{3} 抽出された変更候補: [0-9]+(?:\.[0-9]+)? [A-Z]{3} 契約内容は承認するまで変更されません。|` +
	`新しい確認待ちがあります 解析後の通知作成時点で[0-9]+件の新しい確認待ちがありました。Cycrioで現在の確認待ちを確認してください。|` +
	`メールの取り込み状況を確認してください メールの取り込みで確認が必要なエラーが発生しています。アプリで接続設定と取り込み状況を確認してください。` +
	`)\z`)

// IsGeneratedEmailText works on the same transient body that readers return for
// plain text and HTML. Neither headers nor personal content are retained.
func IsGeneratedEmailText(text []byte) bool {
	body := strings.Join(strings.Fields(string(text)), " ")
	body = strings.TrimPrefix(body, EmailNotice+" ")
	return generatedEmailBody.MatchString(body)
}
