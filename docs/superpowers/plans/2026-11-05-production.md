# 本番デプロイ準備 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Coolifyと既存PostgreSQLへ設定を投入して起動できる構成を用意する。

**Architecture:** API・Workerを同じComposeアプリに置き、migrationは明示実行する。独立した一時PostgreSQL 16で既存のDB/API統合テストを実行する。

**Tech Stack:** Docker Compose、Go、PostgreSQL 16.15、Coolify、Cloudflare Workers Static Assets。

**Spec:** [本番デプロイ設計](../specs/2026-11-05-production-design.md)

## Global Constraints

- 開発用PostgreSQL 18は変更しない。
- APIだけを公開し、秘密値・別プロジェクトの情報をコミットしない。
- Clerk・Hatchet未設定の間は本番起動を成功扱いしない。

## Review Focus

- 再デプロイ後の秘密ファイルとkeyringの保持。
- migration失敗時に新バージョンを起動しない手順。
- PgBouncer transaction poolingとprepared statementの互換性。
- 実行用ロールのschema・テーブル権限。
- 他サービスを巻き戻さないDB復元手順。

## Task 1: Coolify構成と運用手順

**Files:** `compose.coolify.yaml`、`deploy/production/init-database.sql`、`docs/production.md`、`docs/operations.md`。

- [x] Coolify用Compose、専用DB初期化SQL、設定サンプルを追加する。
- [x] Compose構文、SQLを一時DBで検証する。
- [x] 初回起動・更新・復元・未確認事項を運用手順へ記載する。

## Task 2: PostgreSQL 16互換性

**Files:** `deploy/test/postgres16.compose.yaml`、`deploy/test/postgres16.sh`、`Makefile`、`.github/workflows/ci.yml`。

- [x] 一時DBだけを作成・削除する検証コマンドを追加する。
- [x] PostgreSQL 16.15でDB/API統合テストを実行する。
- [x] CIへ構文検証と互換性検証を追加する。
- [x] 本番Webビルドを実行し、変更全体をレビューする。

## 検証結果

`make test-postgres16`はDB統合テスト195.597秒、API統合テスト4.635秒で成功。専用DB初期化・migration・実行用の読み書き権限・DDL権限の拒否も確認した。`cache_describe`で型情報を保持する。Composeとシェル構文、本番API URLを指定したWebビルド、差分レビューも成功。本番起動・外部サービスの受入確認は別途実施する。
