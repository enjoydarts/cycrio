# Healthchecks.ioへのheartbeat送信

CycrioのWorkerからHealthchecks.ioへ、正常な稼働を示す空のHTTP POSTを定期送信します。監視側からCycrioの専用エンドポイントを呼び出す方式ではありません。

## 設定

1. Healthchecks.ioで、この環境専用のチェックを作成します。送信周期は1分のため、Periodは1分、Grace Timeは2分を目安にします。
2. 管理者でCycrioへログインし、「API キー・連携アプリ」の「Healthchecks.io」を開きます。
3. チェックのPing URL（`https://hc-ping.com/<UUID>`）を入力し、「保存して監視を開始」を選びます。
4. Healthchecks.io側で受信と通知先の設定を確認します。初回は次のWorker巡回以降、正常確認ができたときに送信します。

Ping URLにはチェックを操作するための識別子が含まれるため、秘密値としてDBへ暗号化保存します。保存済みのURLは再表示せず、通常のAPIは登録状態・versionなどのメタデータだけを返します。更新・無効化には既存の管理者認可・version確認・監査記録を使用します。

UUID形式の公式HTTPS送信先だけを受け付けます。独自ドメイン・セルフホスト版・slug形式・追加パス・クエリー・フラグメント・認証情報・ポート指定には対応していません。リダイレクトを追跡せず、秘密URLを他の送信先へ渡しません。

開発・検証・本番ではチェックを分けてください。同じPing URLを複数環境で共有すると、開発環境からの通知によって本番停止を見逃す可能性があります。未設定・無効の場合は外部送信しません。

## 正常確認と送信

Workerの巡回で、次を確認します。

- 監視設定が有効で、次の送信時刻を迎えている。
- Worker自身がDBへ接続でき、Hatchetの自分のWorkerグループが正常である。
- Composeネットワーク内のAPI（`http://api:8080/readyz`）が200と`ok`を返す。

正常な場合だけ、DBで共有する1分ごとの送信枠を確保し、有効な資格情報を解決して空のPOSTを送ります。複数Workerが動作しても、同じ周期の送信枠を重複取得しません。正常確認に失敗したWorkerは枠を消費せず、別の正常Workerによる送信を妨げません。

API確認は1秒、Healthchecks.ioへの送信は3秒、全体は5秒でタイムアウトします。巡回は10秒ごとなので、1分の間隔に巡回待ちの時間が加わる場合があります。送信枠の取得後に停止・通信失敗が起きた場合も即時再送せず、次の周期に再開します。

Healthchecks.ioの200応答でも、本文が`OK (not found)`や`OK (rate limited)`なら成功として扱いません。200かつ本文が`OK`の場合だけ送信成功と判定します。プロトコルと応答の仕様は[公式Pinging API](https://healthchecks.io/docs/http_api/)を参照してください。

送信内容にはメール・ユーザー識別子・ログ・APIキーを含めません。Ping URLをアプリのログやジョブpayloadへ出さず、外部応答の本文も保存しません。

## 異常・停止時

API・DB・Hatchet・Workerに異常がある場合は、成功POSTを送りません。アプリ全体が停止した場合も通知が途絶えるため、Healthchecks.ioがPeriodとGrace Timeに従って検知し、設定済みの通知先へ知らせます。Cycrioから`/fail`への通知は行いません。

Cycrio側でHealthchecks.ioの資格情報を無効化すると、新しい送信を停止します。送信中の処理が完了する場合はあります。意図した停止の際は、Healthchecks.io側のチェックも一時停止してください。

この確認はAPI・DB・Workerの稼働を対象にします。公開Webの配信、公開APIのDNS・TLS経路、個別のメール解析・通知の成功、処理待ちの滞留まで保証するものではありません。公開経路は既存の`https://api.cycrio.com/readyz`等を使った外形監視、処理結果は取り込み履歴・ジョブ履歴と合わせて確認してください。

## 実装と既存ヘルスチェック

`internal/healthchecks`で送信と稼働確認、`internal/database/healthchecks.go`で送信枠を管理します。migration `00046`で既存schemaへ送信時刻用のテーブルと資格情報の種類を追加し、新しいDB・schemaや環境変数は追加しません。

既存のAPI `/healthz`（プロセス生存）・`/readyz`（DB接続）とWorker内部の`/readyz`（DB・Hatchet確認）は維持します。WorkerのHTTPポートを外部へ公開する必要はありません。
