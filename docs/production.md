# Coolifyへの本番デプロイ

[運用・復元ガイド](operations.md) / [初期設定ガイド](setup.md)

配置先・通信経路・権限分離の概要は[本番環境の構成](production-architecture.md)を参照してください。

Webは`https://cycrio.com`、APIは`https://api.cycrio.com`で公開します。API・WorkerはCoolifyのGit連携Docker Composeアプリ、DBは既存の別ホストのPostgreSQL、ジョブ基盤はHatchet Cloudを使います。本番DBは新規作成し、開発データ・開発用keyringをコピーしません。

## 1. DBを準備する

既存DBの管理者が[初期化SQL](../deploy/production/init-database.sql)をpsqlで一度だけ実行します。専用DB `cycrio`、所有・migration用ロール `cycrio_migrator`、実行用ロール `cycrio`を作成します。既存名があれば停止するので、そのまま再実行せず状態を確認してください。新規DBの作成権限が必要です。

作成後、psqlの`\password cycrio`と`\password cycrio_migrator`で別々のランダムパスワードを設定します。SQL・シェル履歴に平文を記載しません。パスワードを含む接続URLを作る際は、ユーザー名とパスワードをURLエンコードしてください。

画面用の一覧・集計SQLでJITコンパイル時間が応答を遅くしないよう、初期化SQLは実行用ロールの専用DB接続に限り`jit=off`を設定します。既存環境へ適用する場合は、管理者が次を実行します。

```sql
ALTER ROLE cycrio IN DATABASE cycrio SET jit TO off;
```

ロール設定は新しいPostgreSQL接続から有効です。PgBouncer経由では、管理コンソールを利用できる場合は`RECONNECT cycrio;`で専用プールの接続を更新します。現在のトランザクション完了後に更新されるため、PostgreSQLやPgBouncer全体の再起動は不要です。管理コンソールを利用できない場合は、実行中ジョブがないことを確認してCycrioのAPI・Workerを停止し、`cycrio` DBの`cycrio`ロールの接続だけを更新してから再開します。他のDB・ロールや、migration用ロールの設定は変更しません。反映後は通常処理の接続で`SHOW jit`が`off`になることを確認します。ロール設定の仕様は[PostgreSQLのALTER ROLE](https://www.postgresql.org/docs/16/sql-alterrole.html)、再接続の仕様は[PgBouncerの管理コマンド](https://www.pgbouncer.org/usage.html#reconnect-db)を参照してください。

PgBouncerに専用DBと実行用ユーザーを登録します。既存のDBマッピング・認証方式・HBA・TLS設定を確認し、既存ユーザーの接続条件を変更せず追加します。reload後、既存アプリとCycrioの両方で接続を確認します。通常処理のURL例:

```text
postgresql://cycrio:URL_ENCODED_PASSWORD@db.example.com:6432/cycrio?sslmode=require&default_query_exec_mode=cache_describe
```

`default_query_exec_mode=cache_describe`は型情報をキャッシュし、名前付きprepared statementの保持に依存しません。JSON等のパラメーターはサーバーが返した型に従ってエンコードします。型の事前取得を省く`exec`は、このアプリのJSON保存と互換性がないため使いません。接続先DBのロール設定で`search_path=cycrio,public`が適用されることも確認します。TLSの最低条件は`require`です。CAと証明書名を検証できる環境では`verify-full`を使い、必要なCAをコンテナへ読み取り専用で配置します。

migration用URLは、同じ専用DBへ接続する`cycrio_migrator`のURLにします。PostgreSQLへの直接接続、または専用のsession pooling接続を使います。直接接続のためにDBポートの公開範囲を広げる必要はありません。

PgBouncerを共用する場合は、例えば次の専用マッピングを追加します。通常処理とmigrationのロールをそれぞれ認証設定へ登録し、新しいロールの接続元と接続先だけを制限します。既存設定を保全し、reload後に両マッピングを使った実接続と既存アプリの稼働を確認します。

```ini
[databases]
cycrio = host=127.0.0.1 port=5432 dbname=cycrio pool_mode=transaction
cycrio_migration = host=127.0.0.1 port=5432 dbname=cycrio pool_mode=session
```

migration用URLのDB名部分は`cycrio_migration`です。実際には同じPostgreSQL DB `cycrio`へ接続します。接続の間は同じバックエンドを保持するsession poolingを使い、migration用URLを通常処理に使いません。SCRAM認証ではPostgreSQLとPgBouncerのverifierを一致させ、平文パスワードをSQLログへ送らない方式で登録します。

## 2. 永続する基盤設定を配置する

デプロイ先で、GitのcheckoutやCoolifyの一時buildディレクトリとは別に秘密ファイル用ディレクトリを用意します。例は`/opt/cycrio/secrets`です。コンテナはUID/GID `10001`で動作します。ディレクトリは同UID/GID・`0700`、ファイルは`0400`または`0600`とし、管理者以外へ読み取り権限を広げません。

| ファイル | 内容 |
| --- | --- |
| `database_url` | PgBouncer経由の実行用URL |
| `migration_database_url` | PostgreSQL直接接続または専用session接続のmigration用URL |
| `bootstrap.json` | 本番ClerkのPublishable Keyと許可origin |
| `hatchet_token` | Hatchet Cloud本番テナントの接続トークン |
| `keyring.json` | 初回migration後に生成する本番専用鍵 |

[bootstrapサンプル](../deploy/production/bootstrap.example.json)を置き換え、`allowed_origins`は`https://cycrio.com`にします。APIとWorkerは同じbootstrapとkeyringを使います。AI・Resend・メールOAuth・Clerk Backend API・Webhookの秘密値はここへ追加せず、起動後に管理画面へ登録します。

## 3. Coolifyを登録する

- Gitリポジトリを接続し、Docker Compose build packを選択。
- Composeファイルは`compose.coolify.yaml`、build contextはリポジトリ直下。
- [変数サンプル](../deploy/production/coolify.env.example)に従い、`CYCRIO_SECRETS_DIR`へ上記の絶対パスを設定。
- APIのドメインを`https://api.cycrio.com`、転送先の内部ポートを`8080`に設定。Workerにはドメインを設定しない。
- Auto Deployは無効にします。更新は後述のGitHub Actionsから、CI成功済みコミットを指定して実行します。

DB、HatchetはこのComposeに含みません。ホストへのAPIポート公開もありません。CoolifyのプロキシからAPIへ接続します。healthcheckはCompose定義を使用します。`manage`は`tools` profileなので通常起動しません。`migrate`は毎回起動し、同じコミットからビルドした`manage migrate`が未適用のSQLを適用します。API・Workerは`service_completed_successfully`で成功を待ちます。migration失敗時は新しいAPI・Workerを起動しません。`restart: "no"`のmigrationはCoolifyの状態集計から除外されます。

## 4. migrationと鍵の初期化

以下はデプロイ先の対象コミットのcheckoutで実行します。Coolifyが用意する生成済み設定とは別に、Composeへ同じ`CYCRIO_SECRETS_DIR`を渡してください。初回のmigration・鍵作成が終わってからCoolifyでDeployします。

```sh
export CYCRIO_SECRETS_DIR=/opt/cycrio/secrets
docker compose -f compose.coolify.yaml build
docker compose -f compose.coolify.yaml run --rm --no-deps migrate
docker compose -f compose.coolify.yaml run --rm --no-deps \
  -v "$CYCRIO_SECRETS_DIR:/keyring-output" \
  migrate manage init-keyring -keyring-file /keyring-output/keyring.json
```

migrationが失敗したら後続のDeployへ進みません。初期鍵作成は空の本番DBで一度だけ行います。既存の暗号化データがある場合は元の鍵を復元してください。生成したkeyringはDBとは別の保管先へバックアップします。

Coolifyで起動後、APIとWorkerのhealthを確認します。Hatchet Cloudの本番テナントにWorkerが登録されることも確認します。

## 5. Web・認証・管理者を準備する

Webは本番APIのURLを指定して、開発環境またはCIでビルドします。

```sh
docker compose run --rm --no-deps -v /app/.svelte-kit \
  -e PUBLIC_API_BASE_URL=https://api.cycrio.com web npm run build
```

`web/build/`をCloudflare Workers Static Assetsへ配信し、Custom Domainに`cycrio.com`を登録します。API用DNSはCoolifyの実際のデプロイ先へ向けます。静的配信では直接`/settings/ai`へアクセスした場合もSPAへ戻ることを確認します。

Clerk本番インスタンスのGoogleログイン・独自ドメイン・DNSを設定します。開発インスタンスのユーザーIDは引き継ぎません。本番で本人がログインして取得したユーザーIDへ、明示的に管理者権限を付与します。

```sh
docker compose -f compose.coolify.yaml run --rm --no-deps manage \
  manage grant-admin -user-id user_REPLACE_ME \
  -bootstrap-file /run/secrets/bootstrap -keyring-file /run/secrets/keyring
```

メールOAuthのコールバックは次のAPI URLに揃えます。

- Gmail: `https://api.cycrio.com/v1/mail/oauth/callback/gmail`
- Outlook: `https://api.cycrio.com/v1/mail/oauth/callback/outlook`
- Clerk Webhook: `https://api.cycrio.com/v1/webhooks/clerk`
- Resend Webhook: `https://api.cycrio.com/v1/webhooks/resend`

管理画面で本番の資格情報とClerkインスタンスID、送信元等を登録します。モデル同期・有料接続テスト・AI有効化・本文送信の同意は[設定ガイド](setup.md)に従います。料金調査の間隔は本番管理画面で`168`時間に設定してください。開発DBの運用設定は自動移行されません。

モデル一覧の同期でAPIキーが未登録の場合は、アプリの`credentials_required`として待機します。この状態をHatchetの実行エラーにはせず、有効なキーを登録するまで自動投入を止めます。キー登録後は、次のWorkerの確認で期限到来済みの自動同期が再開します。手動同期も利用できます。通信障害などの実行失敗は、引き続きHatchetとアプリの同期状態で確認します。

## 6. 更新・バックアップ・受入確認

mainへのpush・mergeで[CI](../.github/workflows/ci.yml)が起動します。ブラウザー・Go/Web・統合・PostgreSQL 16の全ジョブが成功した後、`production`ジョブが次の順に実行します。

1. 必須の認証情報・変数を確認し、開始時点でmainの最新コミットでない実行はスキップ。
2. CoolifyのコミットをCIと同じSHAに固定してデプロイ。起動コマンドも`docker compose up -d --wait --wait-timeout 300`に設定し、新しいイメージのmigration成功後にAPI・Workerを起動。両方のhealthcheck成功まで完了させません。
3. Coolifyの完了コミット・全体のhealthy・公開APIの`/readyz`を確認。
4. 同じCIでビルド・保存したWeb成果物を[本番Wrangler設定](../web/wrangler.production.jsonc)で公開。`https://cycrio.com/release.txt`のSHAを確認。

本番ジョブは同時実行せず、実行中のデプロイを新しいpushで中断しません。PRではデプロイせず、本番の秘密値も渡しません。CoolifyのAuto Deployは無効を維持し、CIを通さない二重デプロイを避けます。APIとWebの更新は別サービスなので原子的ではありません。失敗時はGitHub ActionsとCoolifyの状態を確認してから再実行してください。DBの自動巻き戻しは行いません。

GitHubリポジトリのActions設定に以下を登録します。`production` Environmentに承認待ちを設定すると自動実行が停止するため、無人更新を行う場合は必須承認を設定しません。

| 種別 | 名前 | 値・権限 |
| --- | --- | --- |
| Secret | `COOLIFY_API_TOKEN` | 対象チームのCI専用トークン。`read`・`write`・`deploy`。`read:sensitive`・`root`は不要 |
| Secret | `CLOUDFLARE_API_TOKEN` | 対象アカウントのWorkers Scripts Edit、対象ゾーンのWorkers Routes Edit・Zone Read |
| Variable | `COOLIFY_API_URL` | CoolifyのHTTPS API URL（末尾`/api/v1`） |
| Variable | `COOLIFY_APPLICATION_UUID` | API・Workerを管理するComposeアプリのUUID |
| Variable | `CLOUDFLARE_ACCOUNT_ID` | Webを公開するCloudflareアカウントID |

DB接続URL・keyring・Hatchetトークン・AI APIキーはGitHubへ登録しません。既存ホストのsecretファイルとDBを使い続けます。

DB変更が旧アプリと互換かをマージ前に確認し、非互換なら本番ジョブの停止・バックアップ確認・取り込みと定期処理の停止・実行中処理の完了待ちを含むメンテナンス手順を用意します。バックアップの監視と復元検証は自動デプロイとは別に継続します。

切り戻しはCoolifyで旧コミットを再デプロイします。DBの自動ダウングレードは行いません。旧アプリが新schemaで動かない場合は、互換性修正またはデータ復元を別途判断します。

- DBクラスタの物理バックアップとWAL保存の対象に新DBが含まれることを確認。
- Cycrio専用の論理dump・ロール定義の保管を追加し、単体復元を検証。
- クラスタ全体を巻き戻すと他のDBも影響を受けるため、Cycrioだけ戻す際は隔離先へ復元して取り出す。
- keyringはDBと別保管し、旧鍵も保持する。
- API・Worker health、Hatchetのジョブ失敗・待機、DB容量、バックアップ失敗を監視。

公開時はログイン、一般利用者の管理API拒否、メール接続・少量取り込み・原価記録、Webhook署名検証・メール配信状態、再デプロイ後の設定保持、バックアップからの復元を確認します。外部連携テストやメール解析の有料呼び出しは、費用と送信内容を確認して実行します。

リポジトリ内での互換性検証:

```sh
make test-postgres16
make test-deploy-gate
```

これは専用の一時Composeプロジェクトに合成DBを作り、初期化SQL・実行用権限・migration・DB/API統合テストを確認します。既存の開発DB・本番DBへ接続しません。実際のCoolifyによるCompose解釈、TLS接続、PgBouncer設定、Clerk・Hatchet・Webhookは、対象環境で別途確認する必要があります。

## 参考

- [Coolify: Docker Compose](https://coolify.io/docs/applications/builds/docker-compose)
- [Coolify: Persistent Storage](https://coolify.io/docs/applications/configuration/persistent-storage)
- [Clerk: Production deployment](https://clerk.com/docs/guides/development/deployment/production)
- [Cloudflare Workers Static Assets](https://developers.cloudflare.com/workers/static-assets/)
- [Cloudflare: GitHub Actions](https://developers.cloudflare.com/workers/ci-cd/external-cicd/github-actions/)
- [Docker Compose: 起動順序](https://docs.docker.com/compose/how-tos/startup-order/)
- [pgx: QueryExecMode](https://pkg.go.dev/github.com/jackc/pgx/v5#QueryExecMode)
- [PgBouncer: poolingとSQL機能の互換性](https://www.pgbouncer.org/features.html)
