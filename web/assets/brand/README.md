# Cycrio ブランドアセット

ユーザーが採用したブランドアイデンティティボードに基づくロゴです。C の曲線は継続する支払い周期、波は支出のリズム、右上の球体は発見されたサブスクリプションを表します。

## 配置と用途

| ファイル                                               | 用途                                                               |
| ------------------------------------------------------ | ------------------------------------------------------------------ |
| `cycrio-mark-master.png`                               | 透過原稿。1254 × 1254px。配信ページから直接読み込まない            |
| `../../static/brand/cycrio-mark-128.png`               | ヘッダー・サービス説明のロゴ                                       |
| `../../static/brand/cycrio-mark-512.png`               | 大きな表示用の透過ロゴ                                             |
| `../../static/favicon-16x16.png` / `favicon-32x32.png` | ブラウザータブ用。白い角丸背景で明暗両方のタブに対応               |
| `../../static/favicon.ico`                             | 16px / 32px の PNG フレームを収録                                  |
| `../../static/apple-touch-icon.png`                    | iOS ホーム画面用。180px、白背景                                    |
| `../../static/icon-192.png` / `icon-512.png`           | Web manifest 用。白背景。OS によるマスクを前提にしない通常アイコン |

ワードマークは `Cycrio` 表記です。画面ではテキストで表示し、ホームへのリンクにアクセシブルな名前を付けます。原稿は PNG のため、ベクター形式のロゴではありません。

## カラー

共通設定は `src/lib/brand.css` にあります。Primary `#22C55E`、Secondary `#0F766E`、Accent `#A7F3D0`、Background `#ECFDF5`。白文字を載せるボタンは Secondary、本文は濃いティール、Primary は装飾に使用します。Clerk も同じ CSS 変数を参照します。

## 生成記録

2026-10-25、組み込み imagegen の画像編集モードでユーザー提供ボードから透過シンボルを生成しました。元ボードは上書きしていません。サイズ展開は Docker 内の Chromium Canvas で同じ原稿を縮小し、PNG と ICO に書き出しています。アスペクト比・リボン形状・グラデーションを保持してください。

生成時のプロンプト:

> Use case: background-extraction / identity-preserve.
> Edit target: the supplied Cycrio brand identity board. Extract ONLY the large standalone logo symbol in the upper left (above the large Cycrio wordmark), reproducing that exact selected design as a clean high resolution transparent PNG.
> Preserve the logo identity faithfully: the open circular C ribbon, internal flowing wave ribbons with clear negative space between them, teal lower crescent, mint/green upper arc, and detached shaded green sphere in the upper-right opening. Preserve original shape, proportions, layer overlaps and smooth subtle green-mint-teal gradients. Palette from the board: #22C55E, #0F766E, #A7F3D0. Do not simplify into another C design and do not invent an arrow.
> Output ONE isolated symbol centered on a square transparent canvas with a small even transparent margin of about 6% on all sides. Real alpha transparency in the exterior and all internal negative spaces. No white fill, white halo, cast shadow, panel, text, wordmark, caption, border, background or other board elements. The intended usage is website header logo and favicon source.
