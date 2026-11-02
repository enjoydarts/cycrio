# 開発・検証ガイド

[READMEへ戻る](../README.md)

コマンドはリポジトリ直下から実行します。Go・Node・npmはDocker内の固定バージョンを使います。ホストへの言語ツールのインストールは不要です。

## 日常の開発

| コマンド             | 内容                                                   |
| -------------------- | ------------------------------------------------------ |
| `make up`            | ビルド・初期化して全サービスの起動を待つ               |
| `make ps`            | コンテナの状態を確認                                   |
| `make logs`          | 直近のログを追跡                                       |
| `make down`          | コンテナを停止・削除。named volumeは保持               |
| `make restart`       | API・Workerを再起動。起動処理でGoを再ビルド            |
| `make fmt`           | Go / TypeScript / Svelteを整形                         |
| `make db-shell`      | migration用ロールでアプリDBへ接続                      |
| `make hatchet-token` | ローカルHatchetのトークンを更新し、API・Workerを再起動 |

Webはソース変更をHMRで反映します。Goの変更は`make restart`で反映します。初回起動時はWebの`npm ci`が終わってから検証コマンドを実行してください。

設定の初期化や権限管理は[初期設定ガイド](setup.md)、保存領域と鍵は[運用ガイド](operations.md)を参照してください。

## 構成

| サービス                       | 役割                                                |
| ------------------------------ | --------------------------------------------------- |
| `web`                          | SvelteKit / Viteの開発サーバー                      |
| `api`                          | HTTP API。`/healthz`は生存、`/readyz`はDB接続を確認 |
| `worker`                       | Hatchetジョブの処理とバックグラウンド実行           |
| `postgres`                     | アプリDB。管理・migration・通常実行用のロールを分離 |
| `hatchet` / `hatchet-postgres` | ローカルのジョブ制御と専用DB                        |
| `hatchet-init`                 | 初回のWorker接続トークン発行                        |
| `keyring-init`                 | migration後の初期鍵生成、既存鍵の確認               |
| `devcheck`                     | `make smoke`から実行する疎通確認CLI                 |

`hatchet-init`・`keyring-init`・`migrate`は一度実行して正常終了するコンテナです。終了後に停止していても異常ではありません。DBとWorkerはホストへポート公開しません。HatchetのgRPCは標準で`localhost:7077`、アプリ内部では`hatchet:7077`へ接続します。

## 基本の検証

```sh
make check
make test-integration
make smoke
```

- `make check`：Goテスト・vet、Webテスト・型検査・整形検査・静的ビルド。
- `make test-integration`：一時schemaを使ったDB/API統合テスト。migration、所有者分離、資格情報、メール処理、契約、原価などを確認。
- `make smoke`：起動中のAPI・Web、Hatchet経由の実ジョブ、WorkerのDB接続を確認。

機能追加・不具合修正は、期待する振る舞いを先にテストで再現してから実装します。文書だけの変更は、リンク・構文・記載コマンドとの整合性を確認します。詳細は[AGENTS.md](../AGENTS.md)を参照してください。

対象を絞って実行する例：

```sh
docker compose run --rm --no-deps api go test ./internal/mailoauth
docker compose run --rm --no-deps web node --test src/lib/service-logos.test.ts
```

Web開発サーバーを起動したまま検証する場合は、生成ファイルを分離して実行できます。

```sh
docker compose run --rm --no-deps -v /app/.svelte-kit web \
  sh -c 'npm test && npm run check && npm run lint && npm run build'
```

DBを使う統合テストや復元訓練は、対象を把握したうえで順番に実行してください。別プロジェクトのコンテナ・volumeは操作しません。

## 合成データによる評価

| コマンド                       | 対象                                             |
| ------------------------------ | ------------------------------------------------ |
| `make test-evaluation-smoke`   | 合成メールによる処理・停止・機密値残存の検査     |
| `make test-evaluation`         | より大きな合成データでの評価                     |
| `make test-evaluation-hatchet` | 一時Workerを使うHatchet配送・履歴・ログの検査    |
| `make test-recovery`           | 専用の一時DB・合成鍵によるバックアップ／復元訓練 |
| `make prompt-evaluation`       | 合成メールのプロンプト比較。既定はdry-run        |

合成評価は実メールや実AIの精度・実利用原価の受入確認とは区別します。通常の合成評価は外部AIを使いません。`prompt-evaluation`を有料実行する場合は、対象管理者・有効なモデル設定・費用を確認し、明示的な実行オプションを指定します。

評価結果はGit対象外の`.local/evaluation/`・`.local/recovery/`・`.local/prompt-quality-*.json`などへ保存します。合成鍵やログ、接続用設定を含む成果物もあるため、そのまま公開・コミットしないでください。Hatchetを使う評価は合成metadataを履歴に残します。

Linuxのbind mountでは、コンテナが作った`.local`や評価成果物がroot所有の`0700` / `0600`になります。ホスト側でこれらを操作する復元訓練とHatchet評価は、必要に応じてCIと同じ所有者で実行します。Docker Desktopで通常ユーザーから読み書きできる場合は、通常のMakeコマンドを使います。

```sh
sudo make test-recovery
sudo make test-evaluation-hatchet
```

実行のために鍵や成果物の読み取り権限を全ユーザーへ広げないでください。

## 起動や取り込みに問題があるとき

```sh
make ps
docker compose logs --tail=100 api worker web
```

| 症状                      | 確認すること                                                            |
| ------------------------- | ----------------------------------------------------------------------- |
| Webへアクセスできない     | `web`の起動と`npm ci`の完了、ポート設定                                 |
| 利用許可待ちになる        | ClerkユーザーIDと`allow-user` / `grant-admin`の実行                     |
| Gmail / Outlookの準備待ち | 管理者のOAuthアプリ登録、Client Secret、API側Redirect URI               |
| IMAPの接続に失敗する      | 利用プラン・IMAP有効化・専用パスワード、TLS条件                         |
| 解析が進まない            | メール接続ごとの取り込み履歴、AI有効版、送信許可、WorkerとHatchetの状態 |
| 鍵が見つからず起動しない  | 元のkeyringを復元。新しい鍵で置き換えない                               |

ログを共有する際は認証情報・メールアドレス・個人用の設定が含まれていないか確認してください。メール本文やプロトコルのdebug出力を有効にして切り分ける運用は行いません。

## ビルドとCI

```sh
make build-production
make preview-static
```

`build-production`は本番用GoランタイムとWeb静的成果物をビルドします。Webの公開用成果物は、[運用ガイド](operations.md#webの公開先apiを指定してビルドする)に従って本番APIのURLを指定してビルドしてください。`preview-static`はCloudflareのローカルランタイムで`http://localhost:3199`へ配信します。実サービスへの公開操作は行いません。

[GitHub Actions](../.github/workflows/ci.yml)では、Go/Webのチェック、本番用ビルド、起動・統合テスト、合成評価、復元訓練を実行します。依存バージョンとlockfileの更新方針は[作業規約](../AGENTS.md)を参照してください。
