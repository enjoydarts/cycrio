# Cycrio

Cycrio（サイクリオ）は、日本語の請求・更新メールからサブスクリプション情報を整理する、個人利用向けの管理サービスです。

メール原文を永続保存せず、必要な構造化データから契約一覧、請求履歴、請求周期、次回請求予定日を表示します。メールの明記情報・自動推定・ユーザーによる確認を区別し、不確かな判定は手動で訂正できる設計を目指します。

## 開発状況

Docker Composeによる開発環境に、Clerkログイン、利用許可・管理者権限、DBマイグレーションを追加しています。メール取得・AI解析・契約画面・キー管理画面は次の実装段階です。

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

`.env`の作成やHatchetアカウントの準備は不要です。DB初期化・Hatchet起動・Worker接続用トークンの発行を自動実行します。初回はイメージ・依存関係の取得とGoビルドに時間がかかります。

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
| `devcheck` | `make smoke`から起動する疎通確認CLI |

Clerkは次の手順で開発用アカウントへ接続します。Resend・外部AIは後続段階で接続し、起動・疎通確認では外部AIを呼び出しません。

## 初期ログイン設定

1. Cycrio専用のClerk開発インスタンスを作成し、Google接続だけを有効にします。メールOTP・パスワード・電話・他のSNS接続はClerk側でも無効にしてください。
2. `make up` の後に `make configure-auth` を実行し、Clerkの **Publishable Key** とフロントのorigin（通常 `http://localhost:3100`）を入力します。Secret Keyは入力しません。
3. Webを開いてGoogleでログインします。初回は利用許可待ちとなり、本人のClerkユーザーIDが表示されます。
4. 運用者が `make grant-admin USER_ID=user_...` を実行して、対象者を初期管理者として許可します。画面を再確認すると本人情報と管理画面へのリンクが表示されます。

初期公開設定はGit対象外の`.local/bootstrap.json`へ保存されます。セッション検証は設定済みClerkインスタンスの公開JWKSを使い、APIはClerkのSecret Keyを要求しません。認証未設定でも開発環境は起動しますが、保護されたAPIは利用できません。GoogleログインはGmail取得の許可を意味しません。

一般利用者の許可は`make allow-user USER_ID=user_...`、管理者権限の解除は`make revoke-admin USER_ID=user_...`、利用停止は`make disable-user USER_ID=user_...`です。最初にアクセスした人を自動で管理者にする仕組みや、認証を省略する開発用ログインはありません。許可済みユーザーの状態・権限は毎APIリクエストでDBから確認します。

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
| `make test-integration` | 専用の一時schemaでmigration・本人識別・権限変更をDB検証 |
| `make smoke` | API・Web疎通と、Hatchet経由でのWorker実行・DB接続 |
| `make fmt` | Go / TypeScript / Svelteを整形 |
| `make db-shell` | migration用ロールでアプリDBへ接続 |
| `make hatchet-token` | 開発用Hatchet接続トークンを更新し、API・Workerを再起動 |
| `make build-production` | 本番用GoランタイムイメージとWeb静的成果物をビルド |
| `make preview-static` | 静的成果物をCloudflareローカルランタイムで配信（http://localhost:3199） |
| `make configure-auth` | Clerk公開設定を対話入力してAPIを再起動 |

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

アプリDB、Hatchet DB、Hatchetの署名・暗号設定、接続トークンは別々のnamed volumeへ保存します。GoキャッシュとWeb依存もvolumeで再利用します。`docker compose down --volumes`はこれらを削除するため、データを消す意図があるときだけ使用してください。

環境変数はポートなど起動に必要なものへ絞ります。トークン・モデル・運用設定は可能な限り管理者専用画面で扱い、秘密値は暗号化保存する方針です。今回のHatchetローカルトークンは自動生成した専用volume、将来の管理画面が必要とするDB接続・復号鍵などは基盤設定として管理します。AI APIキーを`.env`やCIへ追加しないでください。

## 本番向け構成

`compose.production.yaml`は開発用とは独立したAPI・Workerのみの構成です。Goは非rootユーザーで実行し、読み取り専用ファイルシステム・secretファイル・TLSを使用します。PostgreSQLとHatchet Liteを本番用定義から起動せず、専用DB・最小権限ロールとHatchet Cloudを接続先とします。

デプロイ時はmigration用接続ファイルを用意し、`docker compose -f compose.production.yaml run --rm migrate`をAPI・Worker更新より先に実行します。通常のAPIにDDL権限を与えません。APIには本番Clerkの公開設定を`.local/production/bootstrap.json`（`CYCRIO_BOOTSTRAP_FILE`で変更可）から読み取り専用secretとして渡します。ファイルはコンテナのUID/GID 10001が読み取れる所有者・権限にしてください。

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
