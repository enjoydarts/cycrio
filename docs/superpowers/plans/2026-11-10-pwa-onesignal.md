# PWA・OneSignal Push通知の実装計画

> **実装担当向け:** 作業分担または計画実行のスキルに沿って、各タスクを順に実装する。進捗はチェックボックス（`- [ ]`）で記録する。

**目標:** CycrioをPWAとして利用できるようにし、本人が許可した端末へOneSignalで更新予定と新しい確認待ちを通知する。

**構成:** 既存の通知生成・Hatchet配信・暗号化資格情報を拡張する。OneSignalの匿名購読を受信チャレンジで確認し、認証済み利用者にだけ関連付ける。アプリ内・メール・Pushは独立した設定と配信台帳を持つ。

**使用技術:** Go、PostgreSQL、Hatchet、SvelteKit、OneSignal Web SDK安定版160800（公式公開2026-11-10）、標準HTTP API。

**設計書:** `docs/superpowers/specs/2026-11-10-pwa-onesignal-design.md`

## 共通の制約

- OneSignalは配信だけを担当し、対象の判定、予定時刻、本人同意、重複防止はCycrioを正とする。
- メール送信への同意とPushの許可は独立。既存設定・過去監査は変更せず、新しいPush設定は初期OFF。
- 新しい確認待ちは解析完了ごとに新着分をまとめ、導入前の既存分は遡及配信しない。
- 秘密は既存DB暗号化・資格情報管理。新規の秘密環境変数、外部ユーザーIDへのメール/Clerk識別子の利用は禁止。
- ロック画面は一般的な文面と認証済み画面へのリンク。元メールや契約の個人情報はOneSignalへ渡さない。
- 新しいschemaを作らず既存schemaでmigrationを適用。API・個人情報・HTMLの独自SWキャッシュは追加しない。
- 実行・整形・Go/NodeテストはDocker Compose/make経由。手編集はapply_patch。TDDでREDを確認してから実装。

## レビューで重点確認する点

- 他人の購読IDの申告や同一ブラウザーでの所有者切替によって、通知先を奪えないこと（Task 2/5）。
- 旧版設定JSON・不変監査snapshot・Push無効の既存ユーザーが壊れないこと（Task 3/4/5）。
- 退会、設定停止、端末解除、送信開始が競合しても、無効な宛先へ後から送信しないこと（Task 2/3）。
- 送信応答の消失やジョブ再実行で、新しい冪等キーによる重複送信が起きないこと（Task 1/3）。
- 再解析、確認済み化、初回大量取り込みで確認待ち通知が重複・遡及・大量送信されないこと（Task 4）。

---

### タスク1: 固定SDK・PWA資産とOneSignal HTTP送信

**対象ファイル:** `internal/pushnotifications/{types.go,onesignal.go,onesignal_test.go,credentials.go,credentials_test.go}`、`web/vendor/onesignal/160800/`、`web/scripts/prepare-push-assets.mjs`と対応テストを作成する。`web/package.json`、`web/.prettierignore`、`web/static/site.webmanifest`を変更する。

**入出力の定義:** `pushnotifications.Credentials{AppID,APIKey string}`、`NormalizeCredential(string)(string,error)`、`ParseCredential([]byte)(Credentials,error)`。`Message{AppID string,SubscriptionIDs []string,IdempotencyKey string,Kind string,ChallengeID string,ChallengeSecret string,URL string}`、`SendResult{State string,ProviderID string,InvalidSubscriptionIDs []string}`、`Sender.Send(context.Context,[]byte,Message)(SendResult,error)`、`NewOneSignalSender(*http.Client,string) Sender`。Kindはregistration/pending_review/reminder/import_error、Stateはaccepted/failed/unknown。originは生成済みの同一origin URL検証に用い、送信APIのorigin変更には用いない。

- [ ] **RED:** GoテストでApp ID不正、キー漏洩防止、同一origin以外のリンク拒否、外部送信の固定endpoint、UUID冪等キー、複数購読、受付/拒否/通信結果不明を検証する。NodeテストでSDKハッシュ不一致・非standalone manifest・個人データをcacheするSWを拒否する。
- [ ] **RED実行:** `docker compose -p cycrio run --rm --no-deps api go test ./internal/pushnotifications`、Webは同じComposeのwebサービスで追加testを実行し、意図した失敗を確認する。
- [ ] **実装:** 標準HTTPのみでOneSignalへ配信し、固定の一般文面と許可済みdataだけを生成する。公式APIの認証方式と冪等性を確認する。生の応答本文や秘密をエラーへ含めない。
- [ ] **資産:** 公式SDKのpage.es6とswを版160800で保存し、LICENSE・取得元・SHA256を記録する。prebuildでhash検証し、sourceMappingURLだけを除去した固定資産とrootのOneSignalSDKWorker.jsを生成する。SDK自動prompt/自動welcomeは利用側で無効化する。
- [ ] **GREEN/commit:** 上記Go/Node testを成功させ、PWA/SDKの配布ファイルとtransportを独立コミットする。

### タスク2: Push設定・端末の受信確認・解除API

**対象ファイル:** `internal/database/{push_notifications.go,push_notifications_integration_test.go}`、`internal/httpapi/{push_notifications.go,push_notifications_test.go}`、`internal/database/migrations/00049_push_devices.sql`を作成する。`internal/credentials/service.go`とテスト、`internal/database/credentials.go`、`internal/database/erasure_cleanup.go`、`internal/httpapi/{credentials.go,server.go}`、`cmd/api/main.go`を変更する。

**入出力の定義:** 共通型として `Device{ID,State,CreatedAt,UpdatedAt string}`、`Page{AppID string,Available bool,Reason string,Devices []Device}`、`RegistrationInput{SubscriptionID,RequestID string}`、`Registration{DeviceID,ChallengeID,State string}`、`ConfirmationInput{ChallengeID,Secret string}`、`Confirmed{Device Device,RevocationToken string}` を追加する。リポジトリに `PushSettings(ctx,owner)`、`RegisterPushDevice(ctx,owner,input,sender)`、`ConfirmPushDevice(ctx,owner,input)`、`RevokePushDevice(ctx,owner,id)`、`RevokePushDeviceWithToken(ctx,id,token)` を実装し、HTTP `GET /v1/push-devices`、`POST /v1/push-devices`、`POST /v1/push-devices/confirm`、`DELETE /v1/push-devices/{id}` と解除専用トークンの受付APIへ接続する。

- [ ] **RED:** 認証・管理者認可、他人の購読・nonceの申告拒否、期限切れ/再利用/試行超過、資格情報未設定、停止/退会との競合、同意操作の再試行、所有者切替用の解除tokenが解除以外に使えないことをテストする。
- [ ] **RED実行:** 関連HTTP単体と、既存一時schemaを使うDB integrationを実行し失敗を確認する。
- [ ] **実装:** provider `onesignal` に正規化JSONのApp ID/API Keyを暗号化保存する。App IDの読み取りは認証済み本人APIに限定し秘密値を返さない。production判定は既存CYCRIO_ENVを利用する。
- [ ] **端末確認:** 暗号化した購読ID・安定fingerprint・pending/active/disabled状態をDBへ保存する。5分・最大5回のチャレンジを購読ID/本人/世代へ束縛し、nonceはhashのみ保存、実データは返さず確認通知で指定端末へ送る。確認成功まで通常配信対象にしない。
- [ ] **解除:** 旧認証tokenが無効化された場合にも解除だけできる256bitのcapabilityを確認成功時に返し、DBにはhashだけ保存する。再登録/所有者切替で旧capabilityが新登録を無効化できないようにする。外部通信には退会共有fenceを適用し、退会で端末・challengeを削除する。
- [ ] **GREEN/commit:** 関連integration成功とmigration版を確認し、秘密の公開がないことをレビューしてコミットする。

### タスク3: 既存通知のPushチャネルとWorker配信

**対象ファイル:** `internal/notifications/{types.go,json.go,validation.go,facts.go,generate.go,api.go}` とテスト、`internal/database/{notifications.go,notifications_generate.go,notifications_delivery.go,telemetry_jobs.go,erasure_cleanup.go}`、`internal/notificationruntime/`、`cmd/worker/main.go`を変更する。`internal/database/notifications_push.go`とDB統合テスト、migration `00050_notification_push.sql`を作成する。

**入出力の定義:** Channels/Candidateに `Push bool`、Preferencesに `PushOptIn bool`、SettingsPageにPush可用性を追加する。旧版の保存済みJSONはPush初期OFFで読め、更新APIは現行の全項目を検証する。既存 `notificationruntime.Task{DeliveryID,LeaseID}` は変更しない。実行処理はリポジトリで通知手段を判定し、既存Resendの送信処理とタスク1の送信処理を選択できる構成にする。

- [ ] **RED:** 旧JSONの読取・監査snapshot不変、Pushだけ有効な生成、メール同意との独立、現在設定/端末/App ID/鍵変更・退会との競合、unknownの再claim不可、複数端末と0端末、送信応答消失をテストする。
- [ ] **RED実行:** notifications/runtime単体、通知配信DB integrationを実行して失敗を確認する。
- [ ] **実装:** 既存ledgerへchannel pushを追加し、端末snapshot/状態は子target表に持つ。本人/通知/channelの一意性と同一event重複防止を維持する。未知状態の永続commit前には送信せず、送信前の再検証と共有fenceを維持する。秘密をHatchet payloadへ渡さない。
- [ ] **実装:** 既存ClaimNotificationsとWorker task/pollへ統合し、Push senderはOneSignal、メールsenderは既存Resendを使う。受付と到達を区別し、無効購読を停止する。
- [ ] **GREEN/commit:** 単体と関連DB integration、移行後の既存メール/アプリ内通知を確認してコミットする。

### タスク4: 解析完了ごとの新確認待ち通知

**対象ファイル:** `internal/database/notifications_review.go`とDB統合テスト、migration `00051_notification_review.sql`を作成する。`internal/notifications/{types.go,json.go,generate.go,render.go,facts.go}` とテスト、`internal/database/{notifications_generate.go,notifications_delivery.go,erasure_cleanup.go}`を変更する。

**入出力の定義:** Kind `pending_review`、Candidateへ `ReviewRunID *string` と `ReviewCount *int` を追加する。`GenerationInput`へ確認待ちの集約を追加し、重複判定キーは解析実行と安定した通知エピソードから生成する。DB取得は既存確認待ち画面と同じ候補CTEを再利用する。

- [ ] **RED:** 解析run完了前の非生成、有効化前の既存分の非送信、新着を1通知へ集約、再解析の重複防止、送信前に全件確認済みなら抑止、他所有者/旧版の除外、大量取り込み時の件数制限をテストする。
- [ ] **RED実行:** 純粋生成/描画と確認待ちDB integrationで失敗を確認する。
- [ ] **実装:** 通知有効化時点と既知の安定eventを保存し、過去分へ遡及しない。設定ON/OFFやメール/Push間の切替でも同じ出来事を再送しない。既存生成poll・送信前再検証で同じ母集団を使い、メール/Pushの新着まとめ通知を生成する。
- [ ] **GREEN/commit:** 単体/DB integrationで件数と冪等性を確認しコミットする。

### タスク5: PWA起動・Push端末UI・通常設定への導線

**対象ファイル:** `web/src/lib/{push-api.ts,push-browser.ts,push-controller.ts,PushSettings.svelte}`とテストを作成する。`notifications-api.ts`、`notification-settings-controller.ts`、`NotificationPreferenceFields.svelte`、`NotificationSettings.svelte`、`NotificationCenter.svelte`、`notification-text.ts`、`api.ts`、`AdminCredentials.svelte`、`auth.ts`、`app-context.ts`、`settings.ts`、`SettingsOverview.svelte`、ルートのlayout/settings、既存の関連テストデータとテストを変更する。

**入出力の定義:** タスク2の端末APIを呼ぶ`createPushAPI`とSDK接続処理を分離する。SDKの読込・通信は合成テストで差し替え可能にし、端末の制御処理は認証の範囲と世代へ束縛する。API契約はタスク2/3/4の確定した型を正とする。

- [ ] **RED:** 初回mount/設定表示でSDK呼出0、本人操作でのみロード/許可/確認、拒否/非対応/iPhoneホーム追加案内、nonce受信ACK、SDK遅着/他所有者/ログアウト/解除失敗・再試行を検証する。strict parser・保存照合・旧設定fixtureを更新し失敗を確認する。
- [ ] **実装:** 固定SDKを遅延ロードし、自動prompt/welcomeを無効化する。確認通知の受信/クリックで登録を確定し、明示停止と認証scope切替で端末を無効化する。解除capabilityを旧token依存のない停止用途だけに使い、秘密をログへ出さない。
- [ ] **実装:** 通常設定へ通知sectionを追加し、ベル内と同じ設定部品を再利用する。種類ごとのPush欄と現在端末/登録一覧を共通UI部品で表示し、メール設定を独立して保持する。管理者のOneSignal App ID/Key登録欄を追加する。
- [ ] **GREEN/commit:** Web test/check/lint、合成ブラウザーのPC/モバイルを成功させ、SDK操作と所有者切替の証拠を保存してコミットする。

### タスク6: 統合検証・運用ガイド・公開

**対象ファイル:** `docs/notifications.md`、`docs/setup.md`、`README.md`、必要な`AGENTS.md`の実装済み記述を変更する。ブラウザー回帰は`web/browser-tests/`へ統合する。

- [ ] **検証:** `make check`、関連DB/HTTP integration、`make test-web-browser`を順に実行する。migration適用/version、root SWのMIME/実JS、manifest、SDKハッシュと個人情報非cacheを確認する。
- [ ] **検証:** 独立レビューで新旧設定・端末/所有者分離・解除/退会・結果不明/重複防止・初回/再解析を再確認し、重要な指摘は回帰テストから修正する。
- [ ] **文書:** OneSignalのCustom Code/Web設定、App ID/Keyの管理画面登録、本人による端末許可、iPhone実機受入、鍵更新/無効化、配信状態を記載する。秘密・個人パス・実メール評価情報を含めない。
- [ ] **公開:** 適切な単位のコミットをmainへ統合してpushし、CI/migration/本番公開を確認する。資格情報・実端末が未準備の場合は実送信を成功扱いせず、その設定だけを残課題として明記する。
