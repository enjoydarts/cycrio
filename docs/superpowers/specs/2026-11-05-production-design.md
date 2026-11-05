# 本番デプロイ設計

Webを`https://cycrio.com`、APIを`https://api.cycrio.com`で公開する。WebはCloudflare Workers Static Assets、API・Workerは既存Coolify、DBは既存の別ホストのPostgreSQLを使う。空の専用DBから開始し、ジョブ基盤はHatchet Cloudを使う。

## 採用方式

新規VPSとマネージドDBを用意する方式も検討したが、既存のCoolify・PostgreSQLを利用する方式を採用する。ホストとDBの保守は既存基盤で行い、アプリ専用DB・ロール・暗号化鍵を分離する。

## 制約

- PostgreSQL 16.15でmigrationとDB/API統合テストを検証する。開発用PostgreSQL 18は変更しない。
- アプリはTLS必須のPgBouncer transaction poolingへ接続する。migrationは直接接続または専用session poolingを使う。
- APIだけをCoolifyのプロキシから公開する。WorkerとDBのホストポートは公開しない。
- 基盤設定は秘密ファイル、外部サービスの資格情報は管理画面から暗号化DB保存とする。
- keyringは再デプロイで消えない場所に置き、DBと別にバックアップする。
- Clerk本番インスタンスとHatchet Cloudの準備後に実際の公開確認を行う。
- 公開資料に接続先IP、実資格情報、別プロジェクトの運用情報を記載しない。

## 受入条件

Coolify用Composeの検証、PostgreSQL 16.15の統合テスト、本番Webビルドが成功すること。初回migration・鍵作成・管理者付与・更新・復元の手順を明記すること。実環境で未確認の項目を区別すること。
