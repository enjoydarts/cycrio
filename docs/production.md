# Coolifyへの本番デプロイ

[運用・復元ガイド](operations.md) / [初期設定ガイド](setup.md)

Webは`https://cycrio.com`、APIは`https://api.cycrio.com`で公開します。API・WorkerはCoolifyのGit連携Docker Composeアプリ、DBは既存の別ホストのPostgreSQL、ジョブ基盤はHatchet Cloudを使います。本番DBは新規作成し、開発データ・開発用keyringをコピーしません。

## 1. DBを準備する

既存DBの管理者が[初期化SQL](../deploy/production/init-database.sql)をpsqlで一度だけ実行します。専用DB `cycrio`、所有・migration用ロール `cycrio_migrator`、実行用ロール `cycrio`を作成します。既存名があれば停止するので、そのまま再実行せず状態を確認してください。新規DBの作成権限が必要です。

作成後、psqlの`\password cycrio`と`\password cycrio_migrator`で別々のランダムパスワードを設定します。SQL・シェル履歴に平文を記載しません。パスワードを含む接続URLを作る際は、ユーザー名とパスワードをURLエンコードしてください。

PgBouncerに専用DBと実行用ユーザーを登録します。既存のDBマッピング・認証方式・HBA・TLS設定を確認し、既存ユーザーの接続条件を変更せず追加します。reload後、既存アプリとCycrioの両方で接続を確認します。通常処理のURL例:

```text
postgresql://cycrio:URL_ENCODED_PASSWORD@db.example.com:6432/cycrio?sslmode=require&default_query_exec_mode=cache_describe
```

`default_query_exec_mode=cache_describe`は型情報をキャッシュし、名前付きprepared statementの保持に依存しません。JSON等のパラメーターはサーバーが返した型に従ってエンコードします。型の事前取得を省く`exec`は、このアプリのJSON保存と互換性がないため使いません。接続先DBのロール設定で`search_path=cycrio,public`が適用されることも確認します。TLSの最低条件は`require`です。CAと証明書名を検証できる環境では`verify-full`を使い、必要なCAをコンテナへ読み取り専用で配置します。

migration用URLは、同じ専用DBのPostgreSQLへ直接接続する`cycrio_migrator`のURLにします。PgBouncerを通しません。接続経路はプライベート経路を優先し、公開経路を使う場合も必要な送信元だけ許可します。

## 2. 永続する基盤設定を配置する

デプロイ先で、GitのcheckoutやCoolifyの一時buildディレクトリとは別に秘密ファイル用ディレクトリを用意します。例は`/opt/cycrio/secrets`です。コンテナはUID/GID `10001`で動作します。ディレクトリは同UID/GID・`0700`、ファイルは`0400`または`0600`とし、管理者以外へ読み取り権限を広げません。

| ファイル | 内容 |
| --- | --- |
| `database_url` | PgBouncer経由の実行用URL |
| `migration_database_url` | PostgreSQL直接接続のmigration用URL |
| `bootstrap.json` | 本番ClerkのPublishable Keyと許可origin |
| `hatchet_token` | Hatchet Cloud本番テナントの接続トークン |
| `keyring.json` | 初回migration後に生成する本番専用鍵 |

[bootstrapサンプル](../deploy/production/bootstrap.example.json)を置き換え、`allowed_origins`は`https://cycrio.com`にします。APIとWorkerは同じbootstrapとkeyringを使います。AI・Resend・メールOAuth・Clerk Backend API・Webhookの秘密値はここへ追加せず、起動後に管理画面へ登録します。

## 3. Coolifyを登録する

- Gitリポジトリを接続し、Docker Compose build packを選択。
- Composeファイルは`compose.coolify.yaml`、build contextはリポジトリ直下。
- [変数サンプル](../deploy/production/coolify.env.example)に従い、`CYCRIO_SECRETS_DIR`へ上記の絶対パスを設定。
- APIのドメインを`https://api.cycrio.com`、転送先の内部ポートを`8080`に設定。Workerにはドメインを設定しない。
- 初回はAuto Deployを無効にし、CI成功済みのコミットを選択。

DB、HatchetはこのComposeに含みません。ホストへのAPIポート公開もありません。CoolifyのプロキシからAPIへ接続します。healthcheckはCompose定義を使用します。`manage`・`migrate`は`tools` profileなので通常起動しません。

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

## 6. 更新・バックアップ・受入確認

更新はCI成功済みコミットを対象に、バックアップ確認 → 必要なら取り込み・定期処理の停止と実行中処理の完了確認 → migration → Coolify Deploy → API・Worker・主要画面の確認、の順です。DB変更が旧アプリと互換かを事前確認し、非互換ならAPI・Workerを停止するメンテナンス手順を用意します。

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
```

これは専用の一時Composeプロジェクトに合成DBを作り、初期化SQL・実行用権限・migration・DB/API統合テストを確認します。既存の開発DB・本番DBへ接続しません。実際のCoolifyによるCompose解釈、TLS接続、PgBouncer設定、Clerk・Hatchet・Webhookは、対象環境で別途確認する必要があります。

## 参考

- [Coolify: Docker Compose](https://coolify.io/docs/applications/builds/docker-compose)
- [Coolify: Persistent Storage](https://coolify.io/docs/applications/configuration/persistent-storage)
- [Clerk: Production deployment](https://clerk.com/docs/guides/development/deployment/production)
- [Cloudflare Workers Static Assets](https://developers.cloudflare.com/workers/static-assets/)
- [pgx: QueryExecMode](https://pkg.go.dev/github.com/jackc/pgx/v5#QueryExecMode)
- [PgBouncer: poolingとSQL機能の互換性](https://www.pgbouncer.org/features.html)
