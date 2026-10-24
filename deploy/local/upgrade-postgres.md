# 開発DBのPostgreSQL 16 → 18移行

この手順は以前のCycrio開発DBを引き継ぐ場合だけに使います。新規環境や移行済みの18環境では実行しません。他プロジェクトと本番DBは対象外です。

更新済みのComposeで最初の`make up`を行う前に、旧コンテナが稼働している状態で実行します。旧版を停止・削除済みの場合は、旧版のComposeと既存volumeで起動してから退避してください。空の18用DBで旧データを置き換えないよう、まずバージョンを確認します。

```sh
docker compose exec -T postgres psql -U postgres -d cycrio -c 'SHOW server_version'
docker compose exec -T hatchet-postgres psql -U hatchet -d hatchet -c 'SHOW server_version'
```

両方が16であることを確認し、書込みを止めて非公開バックアップを作成します。保存先が既にある場合は、新しい保存先を用意し、既存バックアップへ上書きしないでください。

```sh
docker compose stop api worker hatchet
umask 077
mkdir .local/pg16-backup
docker compose exec -T postgres pg_dump -U postgres -d cycrio -Fc > .local/pg16-backup/cycrio.dump
docker compose exec -T hatchet-postgres pg_dump -U hatchet -d hatchet -Fc > .local/pg16-backup/hatchet.dump
docker compose run --rm --no-deps --entrypoint tar hatchet-init -C /config -cf - . > .local/pg16-backup/hatchet-config.tar
```

各コマンドの正常終了とバックアップファイルを確認します。Hatchet設定のvolumeも別途退避し、署名・暗号鍵を失わないようにします。`hatchet_config`・`hatchet_token`は継続利用するため削除しません。

新しいComposeは`postgres_data_pg18`と`hatchet_postgres_data_pg18`を使います。18用volumeに既存の利用データがないことを確認してから、新規DBへ復元します。`--clean`は復元先を置き換えるため、移行済みDBでは実行しないでください。

```sh
docker compose up -d --wait postgres hatchet-postgres
docker compose exec -T postgres pg_restore -U postgres -d cycrio --clean --if-exists --exit-on-error < .local/pg16-backup/cycrio.dump
docker compose exec -T hatchet-postgres pg_restore -U hatchet -d hatchet --clean --if-exists --exit-on-error < .local/pg16-backup/hatchet.dump
make up
make test-integration
make smoke
```

復元成功後にDB版・テーブル・権限・Hatchet接続を確認します。旧16用volumeとバックアップは、復元確認まで削除しません。原文・認証情報を含む可能性のあるバックアップをGitへ追加しないでください。
