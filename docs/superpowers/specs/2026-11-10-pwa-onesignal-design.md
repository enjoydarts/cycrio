# CycrioのPWA・OneSignal Push通知

状態: 設計案。実装開始前の確認用。

## 目的と対象

スマートフォンのホーム画面からCycrioを起動し、更新予定や新しい確認待ちをPushで知れるようにする。既存のアプリ内通知・Resendメール通知と同じ本人設定・配信管理を使い、通知から認証済みの確認画面へ移動できるようにする。

利用者が選択した配信サービスはOneSignal。その他の仕様は以下の提案を確認して確定する。

## 検討した方式

| 方式 | 利点 | 負担・選択理由 |
| --- | --- | --- |
| 標準Web PushとVAPID | 追加通知SaaSが不要 | 端末・配信管理を自前で実装する必要がある |
| OneSignal | 配信サービスと管理画面を利用できる | SDKと外部サービスへの依存が増える。今回ユーザーが選択 |
| Firebase FCM | FCM自体は無料、ネイティブアプリにも利用可能 | Firebaseプロジェクト・SDKの導入が必要 |

OneSignalは配信だけを担当し、対象の判定、予定時刻、本人同意、重複防止はCycrioを正とする。OneSignal側の自動キャンペーンと重複して送信しない。

## 既存の構成

- SvelteKitの静的フロント、Go API・Worker、PostgreSQL、Hatchetを継続利用する。
- 現在のmanifestは`display: browser`。アイコンはあるが、アプリ用Service Workerは未導入。
- 通知は更新、年払い、無料期間終了、値上げ候補、取り込みエラーの5種類。一般の新しい確認待ちは未対応。
- `notification_events`と`notification_deliveries`を拡張し、Push専用の別スケジューラーを作らない。
- メール通知の本人同意はPushの許可と独立して扱う。

## PWAと画面

- manifestに安定したアプリIDとstandalone表示を設定し、既存アイコンを利用する。
- OneSignalのService Workerを同一originで配信し、初回はPush受信に使う。独自のAPI・HTMLキャッシュは追加しない。
- iPhoneではホーム画面への追加後に起動して通知を有効にする手順を表示する。対応可否は機能検出し、非対応・拒否・未設定を区別する。
- 通知設定への入口を通常の設定画面にも追加する。ベル内の既存入口も維持する。
- 本人の操作で「この端末のPushを有効にする」を実行し、その操作に応じてブラウザーの許可を要求する。起動時の許可要求やOneSignalの自動ウェルカム通知は無効にする。
- 種類別にアプリ内・メール・Pushを設定でき、端末の登録状態、受信確認、解除を表示する。
- ロック画面の初期文面は「新しい確認待ちがあります」「サブスクの予定があります」などの最小限の案とする。詳細は認証後に表示する。

## 端末と利用者の分離

- OneSignal Web SDKは現時点でIdentity Verificationに未対応。推測可能な内部ユーザーIDを`login(external_id)`に渡すだけの紐付けは採用しない。
- 匿名のOneSignal Push Subscriptionを端末単位で扱い、Cycrioの認証済みAPIで本人に関連付ける。APIの所有者は認証主体から決定する。
- 購読IDの申告だけでは送信先として有効化しない。サーバーが短命・一回限りのランダムな受信チャレンジを指定端末へ送り、同じ本人の認証下で受信確認してから有効化する。チャレンジの保存はハッシュ、期限・試行回数・本人・購読IDに束縛し、ログへ出さない。
- チャレンジは有効化操作時の確認通知として表示する。SDKの受信またはクリックイベントを用いる確認手段は、実SDKの検証で成立を確認してから本実装する。無音Pushには依存しない。
- 他人に紐付いた購読を申告だけで奪えないようにする。アカウント切替時は旧登録を解除し、新しい本人で受信確認をやり直す。
- 通知停止・ログアウト・退会では対応する端末を無効化する。ブラウザーで許可を取り消した場合も次回同期で反映する。
- 複数端末は個別に許可・解除できる。OneSignalへの送信対象はDBに保存された有効な購読IDだけから作り、全員向けセグメント配信は使わない。

## 設定と配信

- 管理者画面でApp IDとApp API Keyを登録する。公開App IDと秘密キーを分離し、秘密キーは既存の資格情報・暗号化基盤で保存する。新しい秘密環境変数は追加しない。
- 本番originは`https://cycrio.com`。開発・自動テストは合成SDKと合成配信を使い、本番の端末やOneSignal Appへ送信しない。
- Workerは配信ID・lease IDを受け、送信前に本人の有効状態、最新設定、通知対象、端末、資格情報を再確認する。
- 種類別Push設定を既存の通知設定へ追加する。既存データのPushは初期OFFで、メールやアプリ内の設定を変えない。
- 複数端末の配信状態を保存し、受付と実際の端末到達を区別する。OneSignalへリクエストを送る前に配信状態と同一試行の冪等キーを確定する。
- 再試行は同じ冪等キーの扱いを公式API仕様で確認したうえで行う。結果不明時に新しいキーで再送せず、期限切れ・抑止・無効購読を区別する。
- 外部障害でメール取り込み・解析・契約の操作を失敗させない。送信停止と未配信の扱いを配信台帳へ残す。

## 新しい確認待ちの通知

先行するメール通知の要望も維持し、新規候補・契約変更など、確認画面の対象となる新しい確認待ちを通知できるようにする。

現時点の提案は「解析完了ごとに新着分をまとめる」。発生ごとか日次かの選択はユーザーへ確認中。採用した方式をメールとPushの双方へ適用する。

- 通知を有効にする前の既存の確認待ちを一括送信しない。
- 同じ候補の再解析や再試行で同じ通知を重複送信しない。
- 通知前に全件が確認済みになった場合は抑止する。
- 初回・大量取り込みでも通知の件数を抑え、確認画面を開くと実際の未確認内容を確認できるようにする。

## セキュリティと検証

- 元メール本文、件名、メールアドレス、OAuthトークン、Clerk識別子、APIキーをOneSignalのタグや通知本文へ渡さない。契約の個人情報もPushに含めない。
- 通知クリックのURLは同一originの許可済み画面に限定し、認証と所有者認可を改めて行う。
- SDKは導入時点の公式安定版を確認し、具体版・配布ハッシュを固定できる配布物を使用する。SDK/SWの更新と既存PWAの更新を検証する。
- 新しいmigrationは既存schemaで行い、既存設定・配信履歴の保持、適用、版番号を検証する。
- TDDで生成、本人・端末の分離、許可/解除、重複防止、同時設定変更、再解析、失敗/結果不明を検証する。
- `make check`、関連DB統合、PC・モバイルのブラウザー検証、PWA/SWのビルド・実配信URLを確認する。
- 合成検証とは別に、本人が許可した端末でOneSignalの実Pushを確認する。iPhoneはホーム画面追加後の起動・通知許可・画面遷移を受入確認する。

## 実装前に確定する点

1. 新しい確認待ちを通知する頻度（ユーザーへ確認中）。
2. 本設計案の確認。その後、実SDKの受信確認方式・公式SDKの固定配布物の確認を先行項目として実装計画を作る。
3. 運用設定に必要なOneSignalのCycrio用App IDとApp API Key。OneSignal側はWebのCustom Code、originを本番ドメインに設定する。秘密キーは完成した管理画面から登録する。

## 公式資料

- [Web SDK setup](https://documentation.onesignal.com/docs/en/web-sdk-setup)
- [Web SDK reference](https://documentation.onesignal.com/docs/en/web-sdk-reference)
- [OneSignal service worker](https://documentation.onesignal.com/docs/en/onesignal-service-worker)
- [Identity verification](https://documentation.onesignal.com/docs/en/identity-verification)
- [Sending messages with the API](https://documentation.onesignal.com/reference/create-message)
- [Keys & IDs](https://documentation.onesignal.com/docs/en/keys-and-ids)
