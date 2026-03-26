---
title: "Tetora v2.1.0 — 大規模コード統合 + Workflow Engine"
lang: ja
date: "2026-03-18"
tag: release
readTime: "約 5 分"
excerpt: "256 ファイルをスリムなコアに統合。DAG サポートの新 Workflow Engine と Template Marketplace。"
---

Tetora v2.1.0 は大型アップデートです。このバージョンの核心テーマは 2 つ：**コードアーキテクチャの大整理**と**新機能の実装**です。

ユーザー視点では、より安定した実行環境、高速な反復サイクル、そして待望の Workflow Engine と Template Marketplace を提供します。開発者視点では、Tetora が急速なプロトタイプから長期的に保守可能なプロダクトへの重要な一歩です。

> **一言で言えば：** root ソースファイルを 28 個から 9 個に統合し、テストファイルを 111 個から 22 個に統合。リポジトリ全体を 256+ ファイルからスリムな構造へ圧縮——同時に Workflow Engine、Template Marketplace、各種 Dashboard 改善を追加しました。

## コード統合：256 ファイル → スリムなコア

今回の統合は複数ラウンドのリファクタリングを経て、次の成果を達成しました：

| 指標 | 統合前 | 統合後 |
|---|---|---|
| リポジトリ総ファイル数 | 256+ | ~73（進行中） |
| Root ソースファイル数 | 28 | 9 |
| テストファイル数 | 111 | 22 |

9 つの root ファイルはドメインごとに分割されています：

- `main.go` — エントリーポイント、コマンドルーティング、起動処理
- `http.go` — HTTP サーバー、API ルーティング、Dashboard ハンドラー
- `discord.go` — Discord ゲートウェイ、メッセージ処理、ターミナルブリッジ
- `dispatch.go` — タスクディスパッチ、TaskBoard、並列制御
- `workflow.go` — Workflow Engine、DAG 実行、ステップ管理
- `wire.go` — クロスモジュール配線、初期化、依存性注入
- `tool.go` — ツールシステム、MCP 統合、ケイパビリティ管理
- `signal_unix.go` / `signal_windows.go` — プラットフォーム固有のシグナル処理

大量のビジネスロジックが `internal/` サブパッケージ（`internal/cron`、`internal/dispatch`、`internal/workflow`、`internal/taskboard`、`internal/reflection` など）に移行し、root 層は薄い調整ロジックのみを保持します。

### なぜこれが重要か？

以前は root 層に 100 以上のファイルがあり、機能が散在していたため、新規コントリビューターが正しいファイルを見つけるだけで多くの時間を要していました。統合後は：

- **保守が容易に**——機能の変更先が明確
- **新規コントリビューターの立ち上げが速く**——28 ではなく 9 つのエントリーポイント
- **IDE ナビゲーションが明確に**——go to definition でファイルを迷わない
- **ビルドが高速化**——不要なパッケージ境界と import チェーンを削減

## Workflow Engine

Workflow Engine は v2.1.0 の中核となる新機能です。YAML でマルチステップの AI ワークフローを記述し、Tetora が実行・エラー処理・状態追跡を担います。

### DAG ベースのパイプライン

ワークフローは有向非巡回グラフ（DAG）構造で定義され、以下をサポート：

- **条件分岐**——前のステップの出力に基づいてルートを決定
- **並列ステップ**——依存関係のないステップを同時実行して総時間を短縮
- **リトライ機構**——ステップ失敗時の自動リトライ、回数とバックオフ戦略を設定可能

```yaml
name: content-pipeline
steps:
  - id: research
    agent: hisui
    prompt: "テーマを調査：{{input.topic}}"
  - id: draft
    agent: kokuyou
    depends_on: [research]
    prompt: "調査結果をもとに初稿を作成"
  - id: review
    agent: ruri
    depends_on: [draft]
    condition: "{{draft.word_count}} > 500"
```

### ダイナミックモデルルーティング

Workflow Engine はタスクの複雑さに応じてモデルを自動選択：

- 簡単なフォーマット・要約 → **Haiku**（高速・低コスト）
- 一般的な推論・文章作成 → **Sonnet**（デフォルト）
- 複雑な分析・マルチステップ計画 → **Opus**（最高性能）

YAML で明示的に指定するか、プロンプトの長さとキーワードに基づいてルーターが自動判断します。

### Dashboard DAG 可視化

実行中のワークフローは Dashboard にノードグラフとして表示：完了ステップは緑、実行中は紫のアニメーション、待機中はグレー、失敗は赤。ログを確認しなくてもパイプライン全体の進捗をリアルタイムで把握できます。

## Template Marketplace

Template Marketplace でワークフローテンプレートを共有・閲覧・ワンクリックインポートできるようになりました。Tetora が個人ツールからエコシステムへ向かう第一歩です。

### Store タブ

Dashboard に Store タブを新設し、以下を提供：

- **カテゴリブラウズ**——ドメインでフィルタリング（マーケティング、エンジニアリング、財務、リサーチ等）
- **全文検索**——テンプレート名と説明を検索
- **おすすめ表示**——公式推奨の高品質テンプレート
- **ワンクリックインポート**——クリックでローカル workspace にインポート

### Capabilities タブ

新たな Capabilities タブで、Tetora インスタンスが持つすべてのケイパビリティを一覧表示：

- **Tools**——利用可能な MCP ツール一覧
- **Skills**——定義済みの Skill コマンド
- **Workflows**——ローカルの Workflow テンプレート
- **Templates**——Agent プロンプトテンプレート

### CLI インポート / エクスポート

UI だけでなく、CLI でも完全にサポート：

```bash
tetora workflow export my-pipeline   # 共有可能な YAML としてエクスポート
tetora workflow create from-store    # Store からテンプレートをインポート
tetora workflow list                  # ローカルの Workflow を一覧表示
```

エクスポートした YAML は GitHub Gist や Tetora Store に貼り付けてコミュニティで共有できます。

## TaskBoard & Dispatch の改善

TaskBoard と Dispatch 層に複数の重要な改善を加え、マルチエージェント並列作業の安定性と可観測性を向上させました。

### 設定可能な並列スロット + スロットプレッシャー

設定ファイルで最大並列スロット数とスロットプレッシャー閾値を指定できるようになりました。システム負荷が閾値を超えると、新しいタスクは強制挿入ではなく自動でキューに入り、エージェント間のリソース競合を防ぎます：

```json
{
  "dispatch": {
    "maxSlots": 4,
    "slotPressureThreshold": 0.8
  }
}
```

### Partial-Done ステータス

長時間タスクで `partial-done` の中間ステータスをサポート。エージェントが一部の作業を完了した後に進捗を報告でき、TaskBoard に完了率が表示されます。タスクが停止しているのか進行中なのかが一目でわかります。

### Worktree データ保護

複数のエージェントが Git worktree を使って並列開発する際、明確なデータ分離保護が加わりました。各エージェントの作業ディレクトリは独立しており、意図しない上書きや他エージェントの状態を汚染するマージコンフリクトが発生しません。

### GitLab MR サポート

GitHub PR に加えて、GitLab Merge Request ワークフローをサポート。`tetora pr create` コマンドがリモートの種類を自動検出し、GitHub CLI または GitLab CLI を適切に呼び出して MR を作成します。

## インストール / アップグレード

### 新規インストール

```bash
curl -fsSL https://tetora.dev/install.sh | bash
```

シングルバイナリ、外部依存ゼロ。macOS、Linux、Windows に対応。

### 旧バージョンからのアップグレード

```bash
tetora upgrade
```

最新バージョンを自動ダウンロードし、バイナリを置き換え、デーモンを再起動します。アップグレード中も実行中のタスクは中断されません。

> **注意：** アップグレード前に長時間実行中のワークフローがないことを確認することをお勧めします。`tetora status` でアクティブなタスクを確認してください。

## 次のステップ：v2.2 計画

v2.1.0 のリリース後、開発の重点は v2.2 の 2 つのテーマモジュールに移ります：

### Financial Module

個人および中小企業向けの財務自動化：収支追跡、レポート生成、予算モニタリング。一般的な会計 API（freee、Money Forward 等）との統合を予定。

### Nutrition Module

健康・食事追跡：食事記録、栄養分析、目標設定。Claude が栄養アドバイザーとして、あなたの食習慣に基づいたアドバイスを提供します。

両モジュールとも Workflow テンプレートとして Store に公開予定で、ゼロから設定することなく直接インポートして使用できます。

## v2.1.0 へアップグレード

シングルバイナリ、依存関係ゼロ。macOS / Linux / Windows 対応。

```bash
tetora upgrade
```

[リリースノートを見る](https://github.com/TakumaLee/Tetora/releases)
