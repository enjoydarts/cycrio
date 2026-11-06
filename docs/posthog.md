# PostHogエラートラッキング

ブラウザーは公式 `posthog-js`、Go API・Hatchet Workerは公式 `posthog-go` を使用します。静的配信のSvelteKitにはNodeサーバーSDKを追加しません。初期状態ではエラートラッキングだけを有効にし、Product Analyticsは共通の送信境界へ将来追加できます。

## 設定

1. PostHogでプロジェクトを用意し、Project Tokenとリージョンを確認します。
2. Cycrioの管理者画面の資格情報設定でPostHogを選び、Project TokenとHostを登録します。Hostは `https://us.i.posthog.com` または `https://eu.i.posthog.com` を指定します。
3. 本番API・Workerに `CYCRIO_ENV=production` を設定します。`compose.production.yaml` と `compose.coolify.yaml` は設定済みです。未指定の開発環境ではDB設定が存在しても送信しません。
4. 本番用にビルドしたフロントを配信します。開発ビルドは送信しません。本番ビルドでもAPIから有効設定を取得できなければ送信しません。

Project TokenとHostは既存の資格情報管理によりDBへ暗号化保存し、更新・無効化を監査します。管理APIと画面は保存した値を再表示しません。**Project Tokenはブラウザーからの取り込みに使う公開用トークンです。** 有効な本番環境では `/v1/public/telemetry` がこのトークンとHostだけを公開します。個人APIキー・プロジェクト秘密APIキーは登録できず、ブラウザーへ渡しません。トークン用の環境変数は不要です。

無効化・更新はGoで最大30秒、ブラウザーで最大60秒後に新規収集へ反映します。ブラウザーの設定取得失敗時は新規送信を停止します。すでに送信を開始したリクエストの取り消しや、受信済みイベントの削除は行いません。JavaScript SDKにはイベント再送を無効化する公開設定がないため、通信失敗後にSDKが保持する既送信イベントは、ログアウト・設定無効化後にも元の識別子で再送される場合があります。新しいログインユーザーへ識別子を付け替えず、同じイベントUUIDでの取り込み重複を避けます。

## 収集範囲とプライバシー

- フロント: 未処理例外、未処理Promise rejection、SvelteKitのクライアントエラー。共通経路で重複を抑止します。
- API: panicと5xx。監視の `/healthz`・`/readyz` の通常の異常応答は除外します。
- Worker: Hatchetタスクのerror・panic、処理後にDBへ保存された主要な失敗、pollerの取得・投入・実行失敗。認証拒否・設定不足・利用上限など既知の業務結果は除外します。

送信するのは固定のエラー分類・処理名・実行環境、安全なスタック位置、内部ユーザーUUIDです。未認証・システム処理は固定の匿名識別子で扱います。メール本文・件名・送受信者、解析結果、OAuthトークン、APIキー、Cookie、認証ヘッダー、リクエスト本文・URL・query、DOM、console、元のエラーメッセージ・panic値は送信しません。元のメッセージを正規表現で部分的に置換せず、安全な固定分類へ置き換えます。

スタックはリポジトリ内のファイル・関数・行番号、またはフロントの静的配信アセット位置に制限し、ローカルの絶対パスやソース本文・変数を送信しません。安全性を優先するため、元のエラーメッセージを使った細かな分類はできません。

フロントのソースマップアップロードは初期導入に含みません。ブラウザーのスタックは配信アセットの行・列までで、元のSvelte/TypeScriptソースへ復元されません。必要になった場合は、公開用Project Tokenと分けたCI専用資格情報で非公開アップロードを追加してください。内部で回収・DB保存済みのWorker失敗も、元の例外スタックではなく報告側の位置になる場合があります。

ユーザー切替・ログアウトでフロントの識別子を解除します。Goは実際のHTTP送信を退会用共有ロック内で行い、停止・削除済みユーザーのUUID付きイベントを送信しません。PostHog上で受信済みの過去イベントはこのローカル削除処理の対象外です。

Session Replay、ページビュー、クリックなどの自動イベント、パフォーマンス計測、Web Vitals、サーベイ、人物プロファイル生成は無効です。PostHogプロジェクト側でもIP匿名化とReplay・自動イベントの無効化を設定してください。

## 障害時と検証

Goの送信は上限付き非同期キュー、2秒の送信期限、再送なしで処理します。同じユーザー・処理・分類は1分間重複抑止します。満杯や終了時の未送信イベントは破棄するbest effortであり、監査台帳には使えません。PostHogの通信失敗・設定取得失敗を本来のAPIやジョブへ返しません。

Workerの必須pollerでpanicが起きた場合は、例外を記録するだけで処理停止を隠しません。Worker全体のreadinessを不健康化し、通常の終了処理から終了コード1で停止して、既存のコンテナ再起動へ進みます。通常のジョブ失敗やPostHog自体の障害では、この停止処理を起動しません。

`make check`、`make test-integration`、`make test-web-browser` で単体・型・整形・ビルド・DB・画面を確認します。SDK送信境界のテストでは機密値を含む例外を使い、実際の送信JSONに残らないことを検証します。実プロジェクトへの疎通は個人データを含まない単発の合成例外で確認し、公開テストendpointや故意に失敗する処理を本番へ残しません。

処理プロセスの強制停止、DB内部で期限切れとして更新されるジョブ、通知生成内で保存後継続する個別失敗、Clerk削除の永続再試行結果は、現在のタスク境界だけではすべて収集できません。停止の検知にはHealthchecks.ioも併用します。

## 公式資料

- [Error Tracking導入](https://posthog.com/docs/error-tracking/start-here)
- [Svelteへの導入](https://posthog.com/docs/error-tracking/installation/svelte)
- [Goへの導入](https://posthog.com/docs/error-tracking/installation/go)
- [JavaScript SDK](https://posthog.com/docs/libraries/js)
