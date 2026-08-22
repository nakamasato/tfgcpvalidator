# tfgcpvalidator 設計

## 1. 背景

Terraform の CI/CD は「PR で plan を出してレビュー → merge → apply」という流れを前提とし、
これは **plan が apply の正確な予告になっている**ことに依存する。

GCP の `deletion_protection` はこの前提を壊す。判定を行うのは GCP API 側 = apply 時であり、
`terraform plan` はリソースが削除可能かどうかを検証しない。結果として PR の plan は緑になり、
merge 後の apply が失敗する。レビューという安全網をすり抜けた後に問題が表面化する。

さらに悪いことに、失敗は部分適用を伴う。Terraform は依存順に削除するため、
`google_sql_database_instance` の削除では先に `google_sql_database` / `google_sql_user` が削除され、
親のインスタンスに到達したところで `deletion_protection` により停止する。
**データベースは消え、保護対象だったインスタンスの殻だけが残る。**
守りたかったデータが先に失われる。

この挙動は hashicorp/terraform#33732 および terraform-provider-google#7869 で報告されているが、
いずれも fix なしで close されている。upstream での解決は期待できず、外側でガードするしかない。

### Terraform 標準の防御機構が機能しない理由

| 機構 | 不足点 |
|---|---|
| `lifecycle.prevent_destroy` | リソース定義を削除すると `lifecycle` ごと消えるため、最も典型的な destroy 経路で作動しない |
| `check` ブロック | assert 失敗は警告止まりで plan/apply を止めない。リソース削除時に一緒に消える |
| `precondition` / `postcondition` | リソース削除時に一緒に消える |
| provider プラグイン | plan 全体を俯瞰する API が plugin モデルに存在せず、自分の resource type しか見えない |

HCL の内側に書いたガードは、リソースが HCL から消える瞬間にすべて無力化される。
ガードは plan という成果物を外から検査する形でしか成立しない。

### 既存 OSS

`deletion_protection` × destroy を正面から扱うメンテ済み OSS は存在しない。

- OPA/conftest, Checkov, Trivy, cfn-guard: 汎用ポリシーエンジン。rego 等を自分で書けば実現できるが既製の答えはない
- tflint: 思想は近いが HCL を読むため destroy 系は原理的に対象外
- terrasafe, terraguard: plan ベースの削除ガードだが allowlist 方式で `deletion_protection` を見ない
- `gcloud beta terraform vet`: CFT constraint 前提で重く、destroy 検知が主眼でない

## 2. スコープ

**GCP 専用。** 対象とする失敗はいずれもクラウド固有の API 挙動に根ざしており、
抽象化しても共有できる部分が少ない。汎用性より深さを取る。

### v1 に含むもの

- `destroy` チェック 2 ルール (詳細は 5 章)
- CLI (`tfgcpvalidator`)
- GitHub Action (composite)
- 出力形式: text / markdown / github / json

### v1 に含まないもの (非目標)

- SA 権限チェック — resource type から必要権限へのマッピング表の整備が重いため v2 送り。
  Check interface は先に切っておき、実際に使いながら設計する
- HCL パース — v1 の 2 ルールは plan JSON だけで完結する。
  唯一 HCL が要りそうな `lifecycle.prevent_destroy` は、設定されていれば
  `terraform plan` 自体がエラーで止まるため plan JSON が生成されず検査対象にならない。
  v1 では `--dir` フラグ自体を提供しない (何もしないフラグを公開 API に置かないため)
- Terraform provider 形式での提供 — destroy 検知は plugin モデルで実現不可能。
  SA 権限チェックのみ provider 化の余地があるが、
  必要 permission を人間が手で列挙する必要が残り CLI 版に劣る
- GCP API を参照するモード (`--mode api`) — v1 は plan JSON のみで完結する
- AWS / Azure 対応

## 3. CLI 設計

チェックごとにサブコマンドを切り、親コマンド `validate` が登録済みの全チェックを実行する。

```
tfgcpvalidator validate --plan tfplan.json          # 全チェックを実行
tfgcpvalidator validate destroy --plan tfplan.json  # destroy のみ
```

チェック固有のフラグはそのチェックのサブコマンドにのみ置く (v2 の `sa-permission` であれば
`--project` など)。親から全実行する場合は既定値を使い、将来的には設定ファイルで与える。

両経路とも共通の runner とレポータを通すため、出力は完全に同一になる。
これにより Action は `validate` を 1 回呼ぶだけで済み、
CI の step と PR コメントが分裂しない。

### 共通フラグ

親・サブコマンドの双方で受け付ける。

```
--plan tfplan.json                  # 必須。terraform show -json の出力
--format text|markdown|github|json  # 既定: text
--fail-on error|warn|never          # 既定: error
```

### 終了コード

| コード | 意味 |
|---|---|
| 0 | `--fail-on` の閾値を超える Finding なし |
| 1 | 閾値を超える Finding あり |
| 2 | ツール自体のエラー (plan が読めない等) |

CI が「チェック失敗」と「ツール故障」を区別できるようにする。

## 4. 構成

Go module: `github.com/nakamasato/tfgcpvalidator`

```
cmd/tfgcpvalidator/       cobra エントリポイント (validate 親 + 各チェックのサブコマンド)
internal/plan/            tfplan JSON から正規化した ResourceChange へ
internal/check/           Check interface + レジストリ + runner
  └ destroy/              destroy チェック実装
internal/report/          text / markdown / github / json 出力
action.yml                composite action
```

`internal/hcl/` と `internal/gcp/` は v1 では作らない。

### 中核インターフェース

```go
type Check interface {
    Name() string
    Run(ctx context.Context, in Input) ([]Finding, error)
}

type Input struct {
    Plan *plan.Plan
}

type Severity int // Error / Warn / Info

type Finding struct {
    Check       string
    Severity    Severity
    Address     string // module.db.google_sql_database_instance.main
    Type        string // google_sql_database_instance
    Message     string
    Remediation string
}
```

チェックは `Input` を受け取り `Finding` を返すだけで、出力形式・exit code・GCP 到達性を知らない。
これにより SA 権限チェックなど後続のチェックを同じ土俵に載せられる。

`Input` は将来 `Config *hcl.Config` と `GCP gcp.Client` を持つが、v1 では追加しない。

## 5. destroy チェック仕様

### 判定ロジック

対象リソース型の一覧は持たない。判定は次の一点に収束する。

> `actions` に `delete` を含むリソースの `before` に保護フラグが立っているか

resource type が何であるかは問わない。これによりリスト保守が不要になり、
新しいリソース型に自動で追随する。

代わりに保護フラグの**フィールド名**を持つ。

| フィールド | 型 | 検知条件 |
|---|---|---|
| `deletion_protection` | bool | `true` |
| `deletion_policy` | string | `"PREVENT"` |
| `settings.deletion_protection_enabled` | bool (ネスト) | `true` |

3 つ目は `google_sql_database_instance` の罠に対応する。このリソースには
Terraform 側の `deletion_protection` と GCP API 側の `settings.deletion_protection_enabled` という
別々の保護が 2 つあり、後者が有効だと前者を false にしても apply は失敗する。

### なぜ `before` を見るのか

削除時は `after` が null になり、Terraform は state 上の値 (= `before`) で API を叩く。
したがって同じ PR で `deletion_protection = false` に変更しつつリソースを削除しても apply は失敗し、
フラグ解除の apply と削除の apply の 2 回に分ける必要がある。
`before` を見る実装はこの挙動と一致する。

### 対象とする actions

| `actions` | 意味 | 判定 |
|---|---|---|
| `["delete"]` | 削除 | 保護フラグ有 → Error |
| `["delete", "create"]` | 強制再作成 | 同上 |
| `["create", "delete"]` | create_before_destroy による再作成 | 同上 |
| 上記以外 | — | 対象外 |

replace でも Terraform は `before` の状態で削除 API を叩くため、
`after` で `deletion_protection = false` にしていても救われない。
ルール 1 と同じロジックで処理でき、実装は「`actions` に `delete` を含むか」に収束する。

### フィルタ

- `mode` が `managed` のもののみ対象 (data source は除外)
- `deposed` 状態のリソースも対象に含める

### Finding の内容

```
ERROR  google_sql_database_instance.main
  deletion_protection = true のまま削除されようとしています。apply は必ず失敗します。
  対処: (1) deletion_protection = false に変更して apply
        (2) その後リソース定義を削除して apply
```

Remediation には 2 段階の手順を必ず含める。1 回の apply で済むと誤解させないため。

## 6. 出力形式

| format | 用途 |
|---|---|
| `text` | ローカル実行・CI ログ。既定 |
| `markdown` | PR コメント |
| `github` | GitHub Actions のワークフローコマンド (`::error::`) によるアノテーション |
| `json` | 他ツールとの連携 |

既定値は常に `text` とし、実行環境による自動切替は行わない。
GitHub Actions 上でアノテーションを出すには Action 側が `format: github` を明示的に渡す。
環境変数を見て暗黙に切り替えると、出力が変わる理由が追えずデバッグを難しくするため。

## 7. GitHub Action

リポジトリ直下の `action.yml` を composite action とし、
goreleaser が GitHub Release に置いた各 OS/arch のバイナリを取得し、
`check` が指定されていればそのサブコマンドを、省略されていれば親 `validate` を実行する。
Docker action にしない理由は起動が遅く、self-hosted runner で嫌われるため。

```yaml
- uses: nakamasato/tfgcpvalidator@v1
  with:
    plan: tfplan.json
    check: destroy       # 省略時は全チェック
    fail-on: error       # 既定: error
    format: github       # 既定: github
```

`outputs`: `findings` (JSON), `error-count`, `warn-count`。

## 8. テスト戦略

TDD で進める。各チェックは `testdata/` 配下の tfplan JSON フィクスチャに対する
table-driven test で検証する。フィクスチャは手書きの最小 JSON とし、
実際の `terraform show -json` 出力から必要部分のみを抽出したものを使う。

最低限カバーするケース:

- `deletion_protection = true` の削除 → Error
- `deletion_protection = false` の削除 → Finding なし
- `deletion_protection` フィールドを持たないリソースの削除 → Finding なし
- `deletion_policy = "PREVENT"` の削除 → Error
- `deletion_policy = "DELETE"` の削除 → Finding なし
- `settings.deletion_protection_enabled = true` の削除 → Error
- 3 種の replace パターン → Error
- `create` / `update` / `no-op` → Finding なし
- data source → 対象外
- module 配下のリソース → address が正しく出る

CLI レベルでは exit code (0 / 1 / 2) と各 format の出力を検証する。

## 9. リリース

goreleaser で linux/darwin × amd64/arm64 のバイナリをビルドし GitHub Release に配置する。
タグは semver。Action は `v1` の移動タグを維持する。

## 10. 今後

`deletion_protection` は「apply してから落ちる」という問題クラスの一例にすぎない。
同じ枠組みで扱える失敗:

- **apply 用 SA の権限不足** — plan は read 権限で通るが apply は write 権限を要求する。
  AWS には `aws_iam_principal_policy_simulation` data source があるが GCP には存在しない。
  resource type から必要ロール/権限へのマッピング表が必要で、Cloud Audit Logs の
  `authorizationInfo.permission` を用いて実測しながら育てる
- **API 未有効化**
