<img src="web/static/brand/cycrio-mark-128.png" alt="Cycrio" width="72" height="72">

# Cycrio

**メールから見つけて、自分で確認するサブスク管理。**

Cycrio（サイクリオ）は、請求・更新メールから契約情報を抽出するサブスクリプション管理サービスです。見つかった情報を確認してから登録し、複数のメールアカウントに分散した契約をまとめて管理できます。

```text
メールを接続 → 過去メール・新着を解析 → 候補を確認 → サブスクへ登録
```

現在は個人利用を中心に開発しています。利用許可制で、メール・契約データをユーザーごとに分離し、AI設定や原価管理は管理者に限定しています。

## できること

| 機能           | 内容                                                                                          |
| -------------- | --------------------------------------------------------------------------------------------- |
| メール取り込み | 対象フォルダーと期間を指定し、過去メールと新着を自動解析                                      |
| 確認・承認     | 抽出候補を確認して登録・既存契約へ紐付け。不要な候補は一括除外                                |
| サブスク管理   | 検索・ソート・メール別の絞り込み、一覧／カード表示、手動追加・編集・削除                      |
| 契約の詳細     | 料金・周期・次回請求日、契約ごとの請求履歴・取得元を確認                                      |
| 取り込み状況   | メール接続ごとの履歴・進捗・件数、停止／再開、失敗分の再試行・過去メールの再解析              |
| サービスの識別 | 海外・国内サービス、キャリア・MVNO、レジストラなどのロゴを自動表示                            |
| AI管理         | モデル一覧のAPI同期、価格調査・変更履歴、接続テスト、月別・ユーザー別・メール接続別の原価集計 |

解析結果はまず「確認が必要」に入ります。本人が承認した情報をサブスクへ登録し、同じ条件の関連メールはまとめて確認できます。メール解析の技術的な失敗は、各メール接続の取り込み履歴から扱います。

金額・通貨・周期が不明な場合は、0円や推定値で埋めません。料金と支払額、カタログの参考価格と実際のAI利用原価を区別します。Cycrioから契約を削除しても、利用先のサービスは解約されません。

## 対応メール

| 接続                     | 方式・対象                                                    |
| ------------------------ | ------------------------------------------------------------- |
| Gmail / Google Workspace | Gmail API・OAuthによる読み取り                                |
| Outlook.com / Hotmail    | Microsoft Graph・OAuth。個人Microsoftアカウント向け           |
| Fastmail                 | IMAP・専用アプリパスワード                                    |
| 汎用IMAP                 | TLS対応の公開メールサーバー。主要サービスの接続プリセットあり |

Yahoo!メール（日本／海外）、iCloud Mail、OCN、BIGLOBE、So-net、@niftyなどはIMAPのプリセットから接続できます。利用プラン・IMAP有効化・アプリパスワードなどの条件は、接続画面の案内で確認してください。

Microsoft 365の職場・学校アカウントは、現在のOutlook OAuth接続の対象外です。アプリへのGoogleログインと、Gmailの読み取り許可は別々に設定します。

## データの扱い

- メール本文・件名は永続保存せず、抽出した契約情報と受信日時・メールID・処理状態などを保存します。
- 解析時には、許可したAIプロバイダーへメール本文を送信します。送信先と保持・学習利用条件を確認し、対象の範囲に同意してから開始します。
- APIキーとメール接続資格情報はDBへ暗号化保存し、保存後の秘密値は画面へ再表示しません。
- 暗号化鍵はDBとは別に保管します。復元にはDBと鍵の両方が必要です。
- ロゴはローカル配信し、サービス名や個人のメールアドレスを外部ロゴ検索へ送りません。

## ローカルで起動する

Docker Engine / Docker Desktop、Docker Compose v2、Makeが必要です。ホスト側のGo・Node・npmは不要です。

```sh
git clone https://github.com/enjoydarts/cycrio.git
cd cycrio
make up
```

DBの初期化・migration、ローカルHatchet、Worker用トークン、初回の暗号化鍵を準備します。初回はイメージと依存関係の取得・ビルドに時間がかかります。

| 接続先          | URL                          |
| --------------- | ---------------------------- |
| Web             | http://localhost:3100        |
| API readiness   | http://localhost:8090/readyz |
| Hatchet管理画面 | http://localhost:8888        |

通常は`.env`の作成不要です。ポートを変更するときは[.env.example](.env.example)を参照してください。公開ポートはlocalhostに限定しています。

起動後は次の順に設定します。

1. ClerkでGoogleログインを準備し、`make configure-auth`を実行する。
2. Webでログインし、`make grant-admin USER_ID=user_example`で最初の管理者を許可する。`user_example`は本人のClerkユーザーIDへ置き換える。
3. 管理画面でAPIキー・モデル・価格・送信条件を設定し、接続テスト後に有効化する。
4. 自分のメールを接続し、フォルダーと期間を選んで取り込みを始める。

詳細は[初期設定ガイド](docs/setup.md)へ。OAuthアプリの登録場所とRedirect URIも記載しています。

## 構成

| 領域             | 技術・役割                                                                        |
| ---------------- | --------------------------------------------------------------------------------- |
| フロントエンド   | Svelte 5 / SvelteKit / TypeScript。Cloudflare Workers Static Assets向け静的ビルド |
| API / Worker     | Go。HTTP APIとバックグラウンド処理を分離                                          |
| DB               | PostgreSQL。所有者分離、暗号化資格情報、処理・原価台帳                            |
| ジョブ           | ローカルはHatchet Lite、本番向け構成はHatchet Cloudへ接続                         |
| アプリ認証       | Clerk。Googleログイン限定・利用許可制                                             |
| AI               | 一次判定はLiquid AI、二次抽出はOpenRouter / OpenAI / Anthropic                    |
| アプリからの送信 | Resend。管理者本人への固定テストメールと配信結果の確認                            |

```text
cmd/                         API・Worker・管理／検証CLI
internal/                    共通処理・Goテスト
internal/database/migrations/ SQL migration
web/                         画面・Webテスト・静的配信設定
deploy/                      コンテナ・初期化・復元手順
compose.yaml                 ローカル開発構成
compose.production.yaml      本番向けAPI・Worker構成
```

依存バージョンは[go.mod](go.mod)、[web/package.json](web/package.json)、[lockfile](web/package-lock.json)、Dockerfile・Composeに固定しています。

## 開発・運用ガイド

| ガイド                                                    | 内容                                                   |
| --------------------------------------------------------- | ------------------------------------------------------ |
| [初期設定](docs/setup.md)                                 | Clerk・利用許可、メールOAuth、AI設定、取り込みの開始   |
| [開発・検証](docs/development.md)                         | Makeコマンド、テスト、合成評価、ログとトラブルシュート |
| [運用・復元](docs/operations.md)                          | 永続データ、暗号化鍵、復元訓練、本番向け構成、Webhook  |
| [サービスロゴの出典](web/static/brand/services/README.md) | 公式画像・Simple Icons・ローカル配信の方針             |
| [作業規約](AGENTS.md)                                     | コーディング・テスト・変更時のルール                   |

よく使うコマンドは`make up`、`make ps`、`make logs`、`make check`、`make test-integration`、`make smoke`です。Goの変更は`make restart`、Webの変更は開発サーバーのHMRで反映します。

本番サービスとしての公開は開発環境の起動と別の工程です。実アカウントでのメール接続・OAuthの継続利用、外部サービスの設定、バックアップ・復旧・監視を対象環境で確認してから運用します。
