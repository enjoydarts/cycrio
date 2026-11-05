# 運用・復元ガイド

[READMEへ戻る](../README.md)

## 保存領域

ローカル開発では、次の領域を分けて保持します。

| 対象                        | 保存先                                                 |
| --------------------------- | ------------------------------------------------------ |
| アプリDB                    | `postgres_data_pg18` named volume                      |
| Hatchet DB                  | `hatchet_postgres_data_pg18` named volume              |
| Hatchet設定・Workerトークン | `hatchet_config` / `hatchet_token` named volume        |
| アプリの暗号化鍵            | `.local/secrets/keyring.json`                          |
| Clerkの公開設定             | `.local/bootstrap.json`                                |
| Goキャッシュ・Web依存       | `go_modules` / `go_build` / `web_modules` named volume |

実際のvolume名にはComposeのプロジェクト名が付きます。`make down`はnamed volumeを保持します。`docker compose down --volumes`はデータを削除する操作なので、通常の再起動やトラブルシュートには使いません。

`.local/`を消してもDBのvolumeは残ります。DBだけが残り、暗号化鍵を失う状態を避けてください。

## 暗号化鍵の保管と復元

退会時のClerk削除待ちも暗号化された削除先を保持するため、処理中は元のkeyringが必要です。Clerk Backend APIキーとインスタンスIDは管理画面で設定します。キーの更新・無効化後は次の試行から最新の有効版だけを使います。Clerk削除の待機件数や障害の内訳は、DBの`clerk_erasure_outbox`を`error_code`ごとに集計して確認できます。暗号化された削除先や認証情報をログへ出力しないでください。設定不足や障害時も削除待ちを破棄せず、設定復旧後に再試行します。[退会の仕様](user-erasure.md)も参照してください。

APIキーとメール接続資格情報はAES-256-GCMで暗号化してDBへ保存します。復号鍵はDBの外にあるkeyringで管理します。

**DBバックアップと、暗号化鍵のバックアップを別々に保管してください。** DBを復元しても元の鍵がなければ、保存済み資格情報は復号できません。

- 初回起動は鍵を生成します。通常の再起動は既存の鍵を使います。
- 暗号化済みデータがあるのに鍵がない場合、自動生成せず停止します。
- 鍵が失われた場合は、元のkeyringを元のパスへ復元し、ファイル権限を`0600`または`0400`にして再起動します。
- APIキーの更新と、暗号化鍵の更新は別の操作です。

退会済み主体の再作成防止にもkeyringの用途別指紋を使います。暗号文を削除した後でも、`erased_identities`が参照する旧鍵を保持してください。削除履歴があるDBで鍵を失った場合も、新しい鍵の自動生成では復旧できません。[退会とデータ削除](user-erasure.md)を参照してください。

keyringの形式は次のとおりです。以下は構造の例で、実際の鍵として使う値ではありません。

```json
{
  "active_key_id": "key-id",
  "keys": {
    "key-id": "base64-encoded-32-byte-key"
  }
}
```

鍵を更新するときは、旧鍵を`keys`に保持し、新しいランダム32バイト鍵を一意のIDで追加して`active_key_id`を切り替えます。API・Workerを再起動してください。旧暗号文の再暗号化や旧鍵の削除は自動では行いません。旧鍵を使うデータが残っている間は、その鍵も保持します。

## 復元訓練とDB更新

```sh
make test-recovery
```

起動済みの開発環境で実行すると、専用の一時PostgreSQLと合成データ・合成鍵でdump / restoreを検証します。既存のアプリDB・keyring・通常のvolumeは変更しません。

Linuxのbind mountでコンテナが作成したroot所有・`0700` / `0600`の成果物を扱う場合は、CIと同様に`sudo make test-recovery`で同じ所有者として実行します。鍵や成果物の権限を全ユーザーへ広げず、詳細は[開発・検証ガイド](development.md#合成データによる評価)を参照してください。

訓練ではデータの件数・内容、migrationの再適用、資格情報の復号、誤鍵・鍵なしの拒否、所有者分離、無効化済みキーの扱いなどを確認します。結果は`.local/recovery/`へ保存します。

これは実データのバックアップではありません。本番環境ではDBと鍵の別保管、外部保管先、復旧手順と所要時間、Hatchetなど周辺基盤の復旧も確認してください。

PostgreSQL 16の既存開発環境から移す場合は、[開発DBの移行手順](../deploy/local/upgrade-postgres.md)を使います。18用volumeへの自動データ移行は行いません。

## 本番向け構成

既存Coolifyと別ホストのPostgreSQLへ公開する場合は、[Coolifyへの本番デプロイ](production.md)を参照してください。専用構成は`compose.coolify.yaml`です。以下は汎用の本番Composeの説明です。

[compose.production.yaml](../compose.production.yaml)はAPI・Workerとmigration用サービスを定義します。Web、PostgreSQL、Hatchetは含みません。

- GoプロセスはUID/GID `10001`、読み取り専用ファイルシステムで実行します。
- 専用PostgreSQLと最小権限の実行ロール、Hatchet Cloudへの接続を準備します。
- APIはlocalhostへbindするため、HTTPS終端・公開経路・ドメインは別途準備します。
- Webは`web/build/`をCloudflare Workers Static Assetsへ配信する構成です。

### Webの公開先APIを指定してビルドする

WebのAPI接続先はビルド時に埋め込まれます。本番では`PUBLIC_API_BASE_URL`に公開APIのHTTPS originを指定します。次の`https://api.example.com`は実際の公開先へ置き換えてください。

```sh
docker compose run --rm --no-deps -v /app/.svelte-kit \
  -e PUBLIC_API_BASE_URL=https://api.example.com \
  web npm run build
```

生成された`web/build/`を公開対象にします。`make build-production`が行う標準のWebビルドはローカルAPIを参照するため、公開用の成果物には上記のAPI指定が必要です。API側のbootstrap設定の`allowed_origins`にも、本番WebのHTTPS originを登録します。

### 基盤設定

本番用の基盤設定はsecretファイルで渡します。

| ファイル                                   | 内容・用途            |
| ------------------------------------------ | --------------------- |
| `.local/production/database_url`           | API・Worker用のDB接続 |
| `.local/production/migration_database_url` | migration用のDB接続   |
| `.local/production/hatchet_token`          | Hatchetの接続トークン |
| `.local/production/bootstrap.json`         | 本番Clerkの公開設定   |
| `.local/production/keyring.json`           | 本番DBの暗号化鍵      |

配置パスの変更に使う変数はCompose定義を参照してください。AIキーやモデル選択は、環境変数へ追加せず管理画面で設定します。開発用の鍵を本番へ流用しません。

### 初回の鍵とmigration

更新時は、API・Workerより先にmigrationを適用します。

```sh
docker compose -f compose.production.yaml run --rm migrate
```

初めて使う空DBでは、migration後にUID/GID `10001`が書き込める専用の`.local/production/`を用意し、次のように初期鍵を作成できます。既存DBの場合は、元の鍵を復元してください。

```sh
docker compose -f compose.production.yaml run --rm --no-deps \
  -v "$PWD/.local/production:/keyring-output" \
  migrate manage init-keyring -keyring-file /keyring-output/keyring.json
```

鍵を別保管先へバックアップし、コンテナのUID/GID `10001`が読み取れる所有者・権限にしてから起動します。通常のAPI用DBロールにはDDL権限を与えません。

この構成ファイルだけで本番公開が完了するわけではありません。認証、OAuthアプリ、監視、バックアップ、外部サービスの契約・利用条件は対象環境で確認します。

## AI費用の上限管理

AI費用の制御は「AI利用原価」で対象ユーザーを選び、月額上限を設定します。上限未設定は予算による制限なし、0 USDは新規呼び出し停止です。判定は当月の使用ユーザー別原価で行い、過去月の表示やメール接続の絞り込みに左右されません。原価不明やUSD以外の費用がある場合は、上限判定を続行せず停止します。原価の再照合・上限変更・月替わり後に再判定します。実行中の呼び出しの費用が確定して上限を超えることがあるため、プロバイダー側の請求上限とは区別してください。管理者の接続テストやシステム処理は対象外です。詳細は[AI費用の月額上限](ai-budgets.md)を参照してください。

## 任意設定：メール送信と外部イベント

管理者の「メール送信・外部イベント」で、ResendとClerk / ResendのWebhookを設定します。アプリのログイン検証にClerkのSecret API Keyは使いません。Webhookの署名鍵は別の資格情報として暗号化保存します。

| Webhook | APIの受信パス         |
| ------- | --------------------- |
| Clerk   | `/v1/webhooks/clerk`  |
| Resend  | `/v1/webhooks/resend` |

外部サービスから到達できるAPIのHTTPS URLを用意し、署名鍵・ClerkインスタンスID・Resendの送信元を管理画面へ登録します。localhostへ外部サービスから直接配信することはできません。

本人が通知設定で同意したメール通知は、Workerが通知生成後にHatchetへ投入します。ジョブには配信IDとlease IDだけを渡し、送信直前に本人の有効状態・最新の通知設定・契約や元情報・検証済み宛先・有効なResend資格情報を確認します。WorkerにもAPIと同じ公開認証bootstrapを配置し、認証設定変更後は両方を再起動してください。本番Composeは同じbootstrap secretを両方へ渡します。

未送信の準備が中断された場合は、1分間のlease期限後に再取得できます。外部通信の前に配信台帳を「結果不明」として確定するため、送信途中の停止や受付応答の消失では自動再送しません。失敗・抑止済みの同じ通知も再送しません。通知の「送信受付済み」は到達を意味せず、配信・バウンス・迷惑メール報告などは署名付きResend Webhookの事実を本人の配信詳細へ表示します。Webhookが受付応答より先に届いても照合できます。

Clerkの署名検証済みイベントから、確認済みprimary emailを暗号化して保持し、管理者本人への固定テストメールに使います。管理者本人は「本人の確認済みメールを同期」でも、登録済みClerk Backend APIキーで同じ情報を取得できます。インスタンスと認証ドメインを照合し、本人の確認済みメインメールだけを保存します。未確認・停止・削除を確認した場合は、古い宛先を送信に使わせません。同期はメールを送信せず、Webhook用トンネルなしのローカル環境でも利用できます。

管理APIは同期結果と時刻だけを返し、アドレスや秘密値を平文再表示しません。取得中の利用停止・退会・設定やキーの変更はDBロックで直列化し、古い同期結果によって新しいWebhook情報や停止状態を復活させません。同期と監査は同一トランザクションです。未知の利用者をWebhookや同期だけで許可したり、管理者へ昇格したりしません。

送信要求の受付、受信サーバーへの配信、配信拒否などは別々に確認します。結果不明のメールは自動再送せず、画面から同じ要求の状態を確認します。別のテスト送信を作る場合は、新しい要求として再度確認してください。

Webhookには遅延があるため、画面は受信済みの状態を表示します。外部Webhook・DNS・実メール送信の受入確認は、合成テストとは別に行います。
