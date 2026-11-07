# PostHogエラートラッキング

ブラウザーは公式 `posthog-js`、Go API・Hatchet Workerは公式 `posthog-go` を使用します。静的配信のSvelteKitにはNodeサーバーSDKを追加しません。初期状態ではエラートラッキングだけを有効にし、Product Analyticsは共通の送信境界へ将来追加できます。

## 設定

1. PostHogでプロジェクトを用意し、Project Tokenとリージョンを確認します。
2. Cycrioの管理者画面の資格情報設定でPostHogを選び、Project TokenとHostを登録します。Hostは `https://us.i.posthog.com` または `https://eu.i.posthog.com` を指定します。
3. 本番API・Workerに `CYCRIO_ENV=production` を設定します。`compose.production.yaml` と `compose.coolify.yaml` は設定済みです。未指定の開発環境ではDB設定が存在しても送信しません。
4. 本番用にビルドしたフロントを配信します。開発ビルドは送信しません。本番ビルドでもAPIから有効設定を取得できなければ送信しません。

Project TokenとHostは既存の資格情報管理によりDBへ暗号化保存し、更新・無効化を監査します。管理APIと画面は保存した値を再表示しません。**Project Tokenはブラウザーからの取り込みに使う公開用トークンです。** 有効な本番環境では `/v1/public/telemetry` がこのトークンとHostだけを公開します。個人APIキー・プロジェクト秘密APIキーは登録できず、ブラウザーへ渡しません。トークン用の環境変数は不要です。

無効化・更新はGoの30秒周期、ブラウザーの60秒周期で読み直します。両段階を合わせ、ブラウザーの新規収集への反映には通常最大約90秒を見込んでください。ブラウザーの設定取得失敗時は新規送信を停止します。すでに送信を開始したリクエストの取り消しや、受信済みイベントの削除は行いません。JavaScript SDKにはイベント再送を無効化する公開設定がないため、通信失敗後にSDKが保持する既送信イベントは、ログアウト・設定無効化後にも元の識別子で再送される場合があります。新しいログインユーザーへ識別子を付け替えず、同じイベントUUIDでの取り込み重複を避けます。

## 収集範囲とプライバシー

- フロント: 未処理例外、未処理Promise rejection、SvelteKitのクライアントエラー。共通経路で重複を抑止します。
- API: panicと5xx。監視の `/healthz`・`/readyz` の通常の異常応答は除外します。
- Worker: Hatchetタスクのerror・panic、処理後にDBへ保存された主要な失敗、pollerの取得・投入・実行失敗。認証拒否・設定不足・利用上限など既知の業務結果は除外します。

例外の種類・技術的なメッセージ・原因チェーン・関数名・ファイル・行番号を残します。すべてを固定メッセージや伏せ字へ置き換えることはしません。処理名・実行環境・内部ユーザーUUIDも記録し、未認証・システム処理は固定の匿名識別子で扱います。

メールアドレス、既知形式のAPIキー・JWT・Bearer認証値、認証・Cookie・メール本文や件名などの機微フィールド値、入力データの埋め込み部分を送信前にマスクします。通常の技術的な識別子、引用されたプロパティ名、複数行の原因説明は保持します。例外へメール本文・解析結果・資格情報を埋め込まない実装を維持し、任意のオブジェクトやリクエスト本文・ヘッダー・Cookie・DOM・consoleを自動添付しません。技術メッセージ内のURLは資格情報・query・fragmentを除き、スタックの公開アセットURLはそのまま照合に使います。

ブラウザーは公式SDKのパーサーでスタックを処理し、実際に読み込まれた同一originのコードアセットに一致するファイルURL・関数・行列と`chunk_id`を保持します。Goはアプリに加え標準ライブラリ・依存パッケージのフレームも残し、ローカル利用者名を含む絶対パスをパッケージ相対表記へ正規化します。リクエスト中の変数・メール内容をスタックへ追加することはありません。

### 非公開ソースマップ

本番ビルドではhidden source mapsを作成し、公式PostHog CLIでJSへchunk IDを注入します。mapの`mappings`・`sources`・`sourcesContent`を確認し、注入後のJSとmapを非公開のCI artifactへ保存します。公開用buildからmapを取り除き、JSに公開`sourceMappingURL`が残らないことも検査します。

アップロードには、取り込み用Project Tokenとは別のPersonal API key（Error tracking: Write、Organization: Read）が必要です。GitHub Actions Secretの`POSTHOG_CLI_API_KEY`に登録してください。このキーはCIのアップロード処理だけに渡し、DBの取り込み設定・ブラウザー・公開アセットへ混ぜません。

本番準備でソースマップをアップロードした後、同じJS bytesを公開します。アップロード前後と公開直前にSHA-256で一致を確認し、キー不足・アップロード失敗・アセット不一致時は公開を停止します。ソースマップ自体は公開しません。Project IDと接続リージョンはビルドスクリプト内のプロジェクト設定で管理します。

アップロードは公式CLIの`event`モードを使い、マップを特定のリリースへ固定しません。変更のないchunkを複数のコミットで再利用したときに、リリースIDだけの違いでアップロードが衝突することを防ぎます。ソースの復元は各フレームの`chunk_id`で行い、公開対象のコミットはCI artifactのmanifestと`release.txt`で照合します。

すでに内部で回収・DB保存済みのWorker失敗では、残っている技術的な理由コードと報告側のスタックを記録します。保存されていない過去の例外スタックを復元したり、Goの通常のerrorが保持していない発生元スタックを捏造したりはしません。

メール解析・管理者接続テストのAI失敗は、対象の失敗結果と所有者・試行に一致する原価台帳から、プロバイダーの固定名・処理段階・HTTPステータスも補足します。外部応答本文やモデルへの入力は読み取りません。HeartbeatはDB判定・Worker確認・API確認・資格情報取得・外部POSTの失敗段階と、timeout等の固定分類、HTTPステータス、検証済みSQLSTATEを安全な原因として残します。秘密のping URLや元の通信エラー文字列は保持しません。

ユーザー切替・ログアウトでフロントの識別子を解除します。Goは実際のHTTP送信を退会用共有ロック内で行い、停止・削除済みユーザーのUUID付きイベントを送信しません。PostHog上で受信済みの過去イベントはこのローカル削除処理の対象外です。

Session Replay、ページビュー、クリックなどの自動イベント、パフォーマンス計測、Web Vitals、サーベイ、人物プロファイル生成は無効です。PostHogプロジェクト側でもIP匿名化とReplay・自動イベントの無効化を設定してください。

## 障害時と検証

Goの送信は上限付き非同期キュー、2秒の送信期限、再送なしで処理します。同じユーザー・処理・分類・安全化済み例外とスタックが一致するときだけ1分間重複抑止し、異なる技術メッセージの例外をまとめて消しません。満杯や終了時の未送信イベントは破棄するbest effortであり、監査台帳には使えません。PostHogの通信失敗・設定取得失敗を本来のAPIやジョブへ返しません。

Workerの必須pollerでpanicが起きた場合は、例外を記録するだけで処理停止を隠しません。Worker全体のreadinessを不健康化し、通常の終了処理から終了コード1で停止して、既存のコンテナ再起動へ進みます。通常のジョブ失敗やPostHog自体の障害では、この停止処理を起動しません。

`make check`、`make test-integration`、`make test-web-browser` で単体・型・整形・ビルド・DB・画面を確認します。SDK送信境界のテストでは機密値を含む例外を使い、実際の送信JSONに残らないことを検証します。実プロジェクトへの疎通は個人データを含まない単発の合成例外で確認し、公開テストendpointや故意に失敗する処理を本番へ残しません。

処理プロセスの強制停止、DB内部で期限切れとして更新されるジョブ、通知生成内で保存後継続する個別失敗、Clerk削除の永続再試行結果は、現在のタスク境界だけではすべて収集できません。停止の検知にはHealthchecks.ioも併用します。

## 公式資料

- [Error Tracking導入](https://posthog.com/docs/error-tracking/start-here)
- [Svelteへの導入](https://posthog.com/docs/error-tracking/installation/svelte)
- [Goへの導入](https://posthog.com/docs/error-tracking/installation/go)
- [JavaScript SDK](https://posthog.com/docs/libraries/js)
- [CLIによるソースマップアップロード](https://posthog.com/docs/error-tracking/upload-source-maps/cli)
