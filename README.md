# Cycrio

Cycrio（サイクリオ）は、日本語の請求・更新メールからサブスクリプション情報を整理する、個人利用向けの管理サービスです。

メール原文を永続保存せず、必要な構造化データから契約一覧、請求履歴、請求周期、次回請求予定日を表示します。メールの明記情報・自動推定・ユーザーによる確認を区別し、不確かな判定は手動で訂正できる設計を目指します。

## 開発状況

Docker Composeによる開発環境に、Clerkログイン、利用許可・管理者権限、APIキーの暗号化保存、DBモデルカタログ・価格履歴、AI実行設定・接続テスト・有効化、使用ユーザー別の月次原価を実装しています。メール取得と実メールへのAI解析・契約画面は次の実装段階です。

個人利用MVPから始め、将来の商用SaaS化を見据えて所有者分離・管理者権限・AI原価管理を設計します。一般ユーザー向けの公開開始は別途判断します。

## 技術方針

- UI：Svelte 5 / SvelteKit / TypeScript
- フロント配信：Cloudflare Workers Static Assets
- API・バックグラウンド処理：Go（別プロセス）
- 永続化：PostgreSQL
- ジョブ制御：開発はローカルHatchet Lite、本番はHatchet Cloud
- アプリ認証：Clerk（Googleアカウントでのログインのみ）
- アプリからのメール送信：Resend
- メールの一次判定：Liquid AI D1
- メールの二次処理：OpenRouter / OpenAI / Anthropic
- AI設定：管理者専用画面で使用プロバイダー・モデルと各APIキーを設定。AI APIキーは環境変数で供給せず、DBへ暗号化保存
- AI原価：管理者専用画面で月別・使用ユーザー別・プロバイダー別・モデル別に確認

## ローカル起動

必要なものはDocker Engine / Docker Desktop、Docker Compose v2以降、Makeです。Go・Node・npmのホストへのインストールは不要です。

```sh
make up
```

`.env`の作成やHatchetアカウントの準備は不要です。DB初期化・Hatchet起動・Worker接続用トークンの発行・DB外の暗号化鍵の初期生成を自動実行します。初回はイメージ・依存関係の取得とGoビルドに時間がかかります。

| 接続先 | URL |
|---|---|
| Cycrio Web | http://localhost:3100 |
| API readiness | http://localhost:8090/readyz |
| Hatchet管理画面 | http://localhost:8888 |
| Hatchet gRPC | `localhost:7077`（アプリは内部の`hatchet:7077`へ接続） |

Hatchet管理画面の開発用ログインは`admin@example.com` / `Admin123!!`です。アプリのClerkログインとは別です。公開ポートはlocalhostに限定し、DB・Workerのポートはホストに公開しません。ポートを変更する場合のみ`.env.example`を`.env`へコピーして調整してください。

| サービス | 役割 |
|---|---|
| `web` | SvelteKit / Vite。ソース変更はHMRで反映 |
| `api` | Go HTTP API。`/healthz`は生存、`/readyz`はDB接続を確認 |
| `worker` | Hatchetタスク実行。自身のACTIVE状態・heartbeatとDBをreadinessで確認 |
| `postgres` | Cycrio専用DB。管理用・migration用・実行用ロールを分離 |
| `hatchet` | Hatchet LiteのAPI・Engine・管理画面 |
| `hatchet-postgres` | Hatchet専用DBとメッセージキュー |
| `hatchet-init` | 初回の接続トークン発行。正常終了後は停止状態でよい |
| `keyring-init` | migration後、暗号化鍵を初回だけ生成。既存の鍵は保持。正常終了後は停止状態でよい |
| `devcheck` | `make smoke`から起動する疎通確認CLI |

Clerkは次の手順で開発用アカウントへ接続します。起動・通常の疎通確認では外部AIを呼び出しません。管理画面からの接続テストは明示的な課金確認後に実行します。Resend送信は後続段階です。

## 初期ログイン設定

1. Cycrio専用のClerk開発インスタンスを作成し、Google接続だけを有効にします。メールOTP・パスワード・電話・他のSNS接続はClerk側でも無効にしてください。
2. `make up` の後に `make configure-auth` を実行し、Clerkの **Publishable Key** とフロントのorigin（通常 `http://localhost:3100`）を入力します。Secret Keyは入力しません。
3. Webを開いてGoogleでログインします。初回は利用許可待ちとなり、本人のClerkユーザーIDが表示されます。
4. 運用者が `make grant-admin USER_ID=user_...` を実行して、対象者を初期管理者として許可します。画面を再確認すると本人情報と管理画面へのリンクが表示されます。

初期公開設定はGit対象外の`.local/bootstrap.json`へ保存されます。セッション検証は設定済みClerkインスタンスの公開JWKSを使い、APIはClerkのSecret Keyを要求しません。認証未設定でも開発環境は起動しますが、保護されたAPIは利用できません。GoogleログインはGmail取得の許可を意味しません。

一般利用者の許可は`make allow-user USER_ID=user_...`、管理者権限の解除は`make revoke-admin USER_ID=user_...`、利用停止は`make disable-user USER_ID=user_...`です。最初にアクセスした人を自動で管理者にする仕組みや、認証を省略する開発用ログインはありません。許可済みユーザーの状態・権限は毎APIリクエストでDBから確認します。

## 管理者のAPIキー設定

1. 管理者としてGoogleログインし、`/admin`を開きます。
2. Liquid AI（D1）、OpenRouter、OpenAI、Anthropic、Resendのうち、利用するサービスのキーを入力して登録します。未使用サービスへの登録は不要です。
3. 変更するときは新しいキーを入力して更新します。保存済みの値は再表示できません。無効化すると、そのサービスの過去のキーも内部利用できなくなります。

画面に表示するのは登録状態、更新日時、版番号と最新50件の操作履歴です。APIキーの環境変数・URL・ブラウザー保存領域での設定経路はありません。入力は保存操作後に消去します。古い画面から更新した場合は競合として拒否し、再読み込み後に操作します。管理APIも毎回DBの現在の管理者権限を確認します。

キー登録はプロバイダーへの推論接続成功を意味しません。キーの登録・更新だけで有料推論やResend送信は行いません。

## モデルカタログと価格

管理者ページのモデルカタログで、モデル検索・追加・編集・利用候補への指定・価格履歴を管理します。AI設定ではDBに登録されたモデルから一次のLiquid AI D1と二次のモデルを選びます。モデルIDの自由入力はカタログ保守に限定し、設定の保存時にはカタログの内部IDと価格版を固定します。

| プロバイダー | 自動取得 | 手動で扱う項目 |
|---|---|---|
| OpenRouter | 公開Models APIのモデル情報・価格・対応パラメーター | 表示名・有効/無効・価格の上書き |
| OpenAI | 登録済みAPIキーでModels APIのモデル情報 | APIにない能力情報・価格 |
| Anthropic | 登録済みAPIキーでModels APIのモデル情報・能力・上限 | 価格。キーの種類に応じてWorkspace IDも管理画面で設定 |
| Liquid AI | 公開カタログAPIを確認できないため自動取得なし | モデル・能力・価格。初期モデルは指定済みの`d1`だけ、価格は未設定 |

OpenRouterは同期にAPIキーを要求しません。OpenAI・AnthropicのキーはAPIキー管理画面で登録します。自動同期の既定間隔は24時間で、管理画面から1〜168時間への変更・停止・手動同期ができます。取得結果はDBへ保存し、Hatchet Workerで処理します。キー未登録や取得失敗は同期状態へ表示し、アプリの起動を止めません。

全ページの取得・検証が成功した場合だけカタログを更新します。APIから消えたモデルは履歴を残して取得不能とし、手動モデルや管理者による表示名・有効/無効・価格上書きは保持します。価格が変わったときは新しい版を作り、過去の設定の価格参照は変更しません。手動価格から最新API価格へ戻す操作もできます。

料金は十進数の文字列で保存し、入力/出力/cacheのtoken料金、request・画像・検索単価を区別します。OpenRouterの条件別料金・割引・将来追加される価格項目も元データとして保持します。欠落や変動料金を0円として扱いません。OpenRouterのカタログ価格は参考値であり、実際の配信先の原価を保証するものではありません。カタログ同期・モデル選択の保存は、推論の接続確認や有効化とは別の操作です。

自動取得範囲は[OpenRouter Models API](https://openrouter.ai/docs/api/api-reference/models/list-all-models-and-their-properties)、[OpenAI Models API](https://developers.openai.com/api/reference/resources/models/methods/list)、[Anthropic Models API](https://platform.claude.com/docs/en/api/models/list)の公開仕様に基づきます。価格APIが公開契約にないサービスはHTMLスクレイピングへ切り替えません。

## AI実行設定と接続テスト

管理画面の「AI 設定」で次の順に操作します。

1. 「モデル選択」で一次のLiquid AI D1と二次のOpenRouter / OpenAI / Anthropicのモデルを保存します。
2. 「実行・接続」で入力上限・出力上限・制限時間を指定し、実行設定を保存します。モデル選択時の価格版と、現在のAPIキー版を固定した不変の下書きになります。OpenRouterでは承認する配信先slug（`provider`または`provider/variant`）も指定します。
3. 課金確認にチェックし「課金を確認して接続テスト」を押します。サーバー固定の合成メールで一次判定と構造化抽出を最大2回呼び出します。メール原文・生成本文・秘密値は台帳やHatchetへ保存しません。
4. 同じ下書きのテスト成功後に「テスト成功版を有効化」を押します。保存・テストだけでは有効化されません。「実行を停止」で有効版を解除できます。

通信結果が不明なテストは自動再実行しません。受付応答だけを受け取れなかった場合は、画面の「同じ要求 ID で受付を再確認」で重複実行を防ぎます。新しい有料テストは改めて確認して開始してください。APIキー更新後は実行設定の再保存・接続テスト・有効化が必要です。既に開始した処理は固定した旧キー版を使いますが、無効化されたキーは利用しません。

OpenRouterは指定モデルと承認配信先を固定し、フォールバック禁止・データ収集拒否・ZDR必須・応答キャッシュ無効で呼び出します。条件を満たす配信先がない場合は失敗として記録します。有効化は将来のメール処理が使う設定の準備であり、この段階ではメール収集や定期推論を開始しません。

## 月次・使用ユーザー別のAI原価

管理画面の「原価」で、JSTの月初以上・翌月初未満に開始した呼出しを集計します。実際の消費利用者と操作した管理者を分け、接続テストは`admin_test`、システム処理は`system`として利用者の原価と分けて表示します。プロバイダー・モデル・処理段階・利用者・スコープで絞り込めます。呼出し0件の利用者も確認できます。

金額は十進数文字列で保存・表示し、通貨を混ぜません。OpenRouterは取得できた利用料金報告を採用し、それ以外は固定した価格版と使用量から安全に計算できる範囲で推定します。未取得・不明・未対応の料金条件は不明とし、0として扱いません。失敗した呼出しも課金・不明件数を残します。

OpenRouterの「原価を再照合」は保存済み生成IDの料金取得だけを行い、推論は再実行しません。報告額は推定額を置き換え、版履歴を合計へ重ねて加算しません。月次額は後から変わる暫定集計です。上流には料金訂正の順序を示す時刻がないため、過去に採用した金額への再変更と古い応答の再送を区別できません。現在は過去額の再報告を履歴へ残し、最新採用額を維持します。商用請求の確定額として扱う機能は未実装です。

## 暗号化鍵の保管・復元

ローカルの暗号化鍵はGit対象外の`.local/secrets/keyring.json`です。APIキーはAES-256-GCMで暗号化してDBに保存し、プラットフォーム所有・用途・プロバイダー・資格情報ID・版番号を認証データへ束縛します。Go標準の[ランダムnonce付きGCM](https://pkg.go.dev/crypto/cipher#NewGCMWithRandomNonce)を使用します。DB更新と秘密を含まない監査履歴は同時に保存します。

鍵ファイルは通常の再起動や`make up`で変わりません。**DBのバックアップとは別に、鍵ファイルもアクセス制限した場所へバックアップしてください。** `.local/`を消すとDBのvolumeだけが残る場合があります。暗号化済みの資格情報があるのに鍵を失った場合、初期化処理は新しい鍵を作らず停止します。元の鍵を同じ場所へ復元し、権限を`0600`または`0400`にしてから`make up`を再実行してください。APIキーを登録し直すだけで失った鍵の暗号文を復号できるようにはなりません。

鍵の更新は、旧鍵を`keys`に保持したまま、新しいランダム32バイト鍵を一意のIDで追加して`active_key_id`を変更し、APIとWorkerを再起動します。ファイル形式は`{"active_key_id":"key-id","keys":{"key-id":"base64-encoded-32-byte-key"}}`です。旧暗号文の再暗号化・旧鍵の削除は、この段階では自動実行しません。旧鍵を使うデータが残っている間は、その鍵を削除しないでください。APIキーの通常更新と、暗号化鍵の更新は別の操作です。

Clerk設定の公式手順：[Google接続](https://clerk.com/docs/guides/configure/auth-strategies/social-connections/google)、[公開鍵によるJWT検証](https://clerk.com/docs/guides/sessions/manual-jwt-verification)。実際のGoogleログイン確認には本人のClerk設定が必要です。

## 開発・検証コマンド

全ての言語ツールはDocker Compose内で実行します。

| コマンド | 内容 |
|---|---|
| `make up` | 全サービスをビルドして起動し、healthyになるまで待機 |
| `make down` | 停止・コンテナ削除。DB・Hatchet設定のvolumeは保持 |
| `make ps` / `make logs` | 起動状態 / ログを確認 |
| `make restart` | API・Workerをソースから再ビルドして再起動 |
| `make check` | Goテスト・vet、Webテスト・型検査・整形検査・静的ビルド |
| `make test-integration` | 一時schemaでmigration・認可・秘密管理・AI設定・実行台帳・原価集計をDB検証 |
| `make smoke` | API・Web疎通と、Hatchet経由でのWorker実行・DB接続 |
| `make fmt` | Go / TypeScript / Svelteを整形 |
| `make db-shell` | migration用ロールでアプリDBへ接続 |
| `make hatchet-token` | 開発用Hatchet接続トークンを更新し、API・Workerを再起動 |
| `make build-production` | 本番用GoランタイムイメージとWeb静的成果物をビルド |
| `make preview-static` | 静的成果物をCloudflareローカルランタイムで配信（http://localhost:3199） |
| `make configure-auth` | Clerk公開設定を対話入力してAPIを再起動 |
| `make init-keyring` | 暗号化鍵を初回生成、または既存鍵を検証。通常は`make up`から自動実行 |

Goの特定テストを実行する例：

```sh
docker compose run --rm --no-deps api go test ./internal/health -v
```

機能追加・不具合修正はTDDで進め、失敗するテストの確認→実装→成功確認→リファクタリングの順に作業します。Compose等の設定変更は構文・ビルド・起動・再起動を検証します。

## 構成と永続化

```text
cmd/api/          HTTP API
cmd/worker/       Hatchet Worker
cmd/devcheck/     実ジョブによる疎通確認
internal/        共通処理と *_test.go
web/             SvelteKit、静的配信設定
deploy/docker/   Goの開発・本番Dockerfile
deploy/local/    開発DB・Hatchet初期化とGo起動処理
cmd/manage/      migration・初期ログイン設定・利用許可の運用CLI
```

アプリDB、Hatchet DB、Hatchetの署名・暗号設定、接続トークンは別々のnamed volumeへ保存します。アプリの暗号化鍵はvolumeとは別の`.local/secrets/keyring.json`です。GoキャッシュとWeb依存もvolumeで再利用します。`docker compose down --volumes`はvolume内のデータを削除するため、データを消す意図があるときだけ使用してください。

環境変数はポートなど起動に必要なものへ絞ります。トークン・モデル・運用設定は可能な限り管理者専用画面で扱い、秘密値は暗号化保存する方針です。今回のHatchetローカルトークンは自動生成した専用volume、将来の管理画面が必要とするDB接続・復号鍵などは基盤設定として管理します。AI APIキーを`.env`やCIへ追加しないでください。

## 本番向け構成

`compose.production.yaml`は開発用とは独立したAPI・Workerのみの構成です。Goは非rootユーザーで実行し、読み取り専用ファイルシステム・secretファイル・TLSを使用します。PostgreSQLとHatchet Liteを本番用定義から起動せず、専用DB・最小権限ロールとHatchet Cloudを接続先とします。

デプロイ時はmigration用接続ファイルを用意し、`docker compose -f compose.production.yaml run --rm migrate`をAPI・Worker更新より先に実行します。通常のAPIにDDL権限を与えません。APIには本番Clerkの公開設定を`.local/production/bootstrap.json`（`CYCRIO_BOOTSTRAP_FILE`で変更可）から読み取り専用secretとして渡します。暗号化鍵は`.local/production/keyring.json`を`/run/secrets/keyring`へ読み取り専用で渡します。ファイルはコンテナのUID/GID 10001が読み取れる所有者にし、鍵の権限は`0600`または`0400`にしてください。開発用の鍵を本番へコピーしないでください。

本番用の空DBにmigrationを適用した後、UID/GID 10001が書き込める専用の`.local/production/`を準備し、同じ本番接続先に対して初期鍵を生成できます。既存DBの場合は先に元の鍵を復元します。

```sh
docker compose -f compose.production.yaml run --rm --no-deps \
  -v "$PWD/.local/production:/keyring-output" \
  migrate manage init-keyring -keyring-file /keyring-output/keyring.json
```

作成した鍵は別保管先へバックアップしてからAPIを起動します。この操作は本番で自動再生成する仕組みではありません。

## 依存バージョン

導入・更新時に最新安定版を確認して固定します。現在はGo 1.27.3、Node 26.12.1、npm 12.2.1、PostgreSQL 18.6、Hatchet 0.114.0、Clerk Go 2.7.0 / JS 6.40.1 / UI 1.41.2です。TypeScriptは、最新版7にSvelteKit・svelte-checkが未対応のため、承認済みの6.0.3を使用します。

PostgreSQL 16からこの構成へ更新する場合は、[開発DBの移行手順](deploy/local/upgrade-postgres.md)に従って既存データを退避・復元してから起動してください。18用volumeは16用とは別で、旧volumeのデータを自動移行するものではありません。

起動時の基盤接続情報は、サーバー上のsecretファイルを`CYCRIO_DATABASE_URL_FILE` / `CYCRIO_HATCHET_TOKEN_FILE`で指定します。既定の配置先は`.local/production/database_url` / `.local/production/hatchet_token`です。秘密値をイメージやGitに含めないでください。管理画面で設定可能な運用トークンの扱いは、その機能の実装時にDB暗号化保存へ統合します。

Webの本番成果物は`web/build/`です。`web/wrangler.jsonc`でCloudflare Workers Static AssetsのSPAフォールバックを設定しています。Cloudflareへの配信にはDocker内のWranglerを使用し、認証・権限・ドメインの設定は本番デプロイの工程で確認します。

```sh
docker compose run --rm --no-deps web npm run build
```

現在は起動基盤のみのため、本番サービスとしての公開は業務機能・認証・バックアップ等の検証後に行います。

## コントリビューション

作業規約は [AGENTS.md](AGENTS.md) を参照してください。認証情報、メール原文、個人用の設計資料・評価データは公開リポジトリに含めないでください。
