# Cycrio

Cycrio（サイクリオ）は、日本語の請求・更新メールからサブスクリプション情報を整理する、個人利用向けの管理サービスです。

メール原文を永続保存せず、必要な構造化データから契約一覧、請求履歴、請求周期、次回請求予定日を表示します。メールの明記情報・自動推定・ユーザーによる確認を区別し、不確かな判定は手動で訂正できる設計を目指します。

## 開発状況

Docker Composeによる開発用起動基盤を用意しています。APIのヘルスチェック、独立WorkerのHatchet接続、DB疎通ジョブ、SvelteKitの静的SPA骨格が対象です。ログイン・メール解析・管理画面などの業務機能はこれから実装します。

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

Clerk・Resend・外部AIサービスは、業務機能の実装段階で開発用アカウントへ接続します。起動基盤の検証では外部AIを呼び出さず、AI APIキーも必要ありません。

## 開発・検証コマンド

全ての言語ツールはDocker Compose内で実行します。

| コマンド | 内容 |
|---|---|
| `make up` | 全サービスをビルドして起動し、healthyになるまで待機 |
| `make down` | 停止・コンテナ削除。DB・Hatchet設定のvolumeは保持 |
| `make ps` / `make logs` | 起動状態 / ログを確認 |
| `make restart` | API・Workerをソースから再ビルドして再起動 |
| `make check` | Goテスト・vet、Web型検査・整形検査・静的ビルド |
| `make smoke` | API・Web疎通と、Hatchet経由でのWorker実行・DB接続 |
| `make fmt` | Go / TypeScript / Svelteを整形 |
| `make db-shell` | migration用ロールでアプリDBへ接続 |
| `make hatchet-token` | 開発用Hatchet接続トークンを更新し、API・Workerを再起動 |
| `make build-production` | 本番用GoランタイムイメージとWeb静的成果物をビルド |
| `make preview-static` | 静的成果物をCloudflareローカルランタイムで配信（http://localhost:3199） |

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
```

アプリDB、Hatchet DB、Hatchetの署名・暗号設定、接続トークンは別々のnamed volumeへ保存します。GoキャッシュとWeb依存もvolumeで再利用します。`docker compose down --volumes`はこれらを削除するため、データを消す意図があるときだけ使用してください。

環境変数はポートなど起動に必要なものへ絞ります。トークン・モデル・運用設定は可能な限り管理者専用画面で扱い、秘密値は暗号化保存する方針です。今回のHatchetローカルトークンは自動生成した専用volume、将来の管理画面が必要とするDB接続・復号鍵などは基盤設定として管理します。AI APIキーを`.env`やCIへ追加しないでください。

## 本番向け構成

`compose.production.yaml`は開発用とは独立したAPI・Workerのみの構成です。Goは非rootユーザーで実行し、読み取り専用ファイルシステム・secretファイル・TLSを使用します。PostgreSQLとHatchet Liteを本番用定義から起動せず、専用DB・最小権限ロールとHatchet Cloudを接続先とします。

起動時の基盤接続情報は、サーバー上のsecretファイルを`CYCRIO_DATABASE_URL_FILE` / `CYCRIO_HATCHET_TOKEN_FILE`で指定します。既定の配置先は`.local/production/database_url` / `.local/production/hatchet_token`です。秘密値をイメージやGitに含めないでください。管理画面で設定可能な運用トークンの扱いは、その機能の実装時にDB暗号化保存へ統合します。

Webの本番成果物は`web/build/`です。`web/wrangler.jsonc`でCloudflare Workers Static AssetsのSPAフォールバックを設定しています。Cloudflareへの配信にはDocker内のWranglerを使用し、認証・権限・ドメインの設定は本番デプロイの工程で確認します。

```sh
docker compose run --rm --no-deps web npm run build
```

現在は起動基盤のみのため、本番サービスとしての公開は業務機能・認証・バックアップ等の検証後に行います。

## コントリビューション

作業規約は [AGENTS.md](AGENTS.md) を参照してください。認証情報、メール原文、個人用の設計資料・評価データは公開リポジトリに含めないでください。
