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
| `deletion_protection` | string | `"PROTECTED"` (Bigtable は enum で綴る) |
| `deletion_protection_enabled` | bool | `true` |
| `settings.deletion_protection_enabled` | bool (ネスト) | `true` |
| `deletion_policy` | string | `"PREVENT"` |
| `delete_protection_state` | string | `"DELETE_PROTECTION_ENABLED"` |
| `delete_protection` | bool | `true` |
| `enable_deletion_protection` | bool | `true` |

この表は provider スキーマ (`terraform providers schema -json`) を全走査して導出した。
google 6.44.0 から 7.41.0 の間に保護フィールドを持つ箇所は 48 から 868 に増えており、
リソース型ではなくフィールド名で照合することが、まだ存在しないリソース型に追随する唯一の方法である。

ネストしたパスは明示的に宣言し、探索はしない。
`google_backup_dr_restore_workload` の
`compute_instance_restore_properties.deletion_protection` を除外するためで、
これは復元先インスタンスの属性であって、そのリソース自身の削除ガードではない。

同じフィールド名は他クラウドの provider にも存在するため、
`google_` で始まらない resource type は対象外とする。

### ルール表の網羅性の維持

`tools/schemaaudit` がこの表を provider スキーマに対して検査する。保護らしき
フィールド名を全リソース型から拾い、ルール表がカバーするもの、意図的に除外した
もの、どちらでもないものに分類し、3 つ目が 1 件でもあれば exit 1 とする。

除外は `destroy.ExcludedPaths()` に理由付きで記録する。記録しない限り未カバー
として失敗するため、判断の省略が黙って通ることはない。

同じツールがテストフィクスチャも生成する。どちらもスキーマを読んでフィールド
パスを解決し型と入れ子を検証する処理を共有しており、出口が違うだけである。

`.github/workflows/schema-audit.yml` が週次と手動で走らせる。利用者のバイナリ
には含めない保守用ツールであり、`validate` の挙動は provider バージョンに依存
しない。

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

ユーザー向けの文字列は README に合わせて英語で統一する。

```
ERROR  google_sql_database_instance.main
  deletion_protection is set on this resource and it is being destroyed.
  The apply will fail.
  fix: Apply deletion_protection = false first, then apply the removal.
       Terraform deletes with the value already in state, so both changes
       cannot land in a single apply.
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

TDD で進める。各チェックは Go のインラインの文字列定数と組み立てた値による
table-driven test で検証する。この規模では `testdata/` 配下に tfplan JSON
フィクスチャを置くよりテストコードの近くに入力が見える方が読みやすく、
実装でもそちらを採用した。

裏返しとして、実際の `terraform show -json` の出力を読ませるテストは
1 本もない。Terraform の実出力の形について誤った前提を置いていても、
このテストスイートは検知できない。

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

### 2 つ目のチェックを足すとき何が変わるか

データの継ぎ目は保たれる。`check.Input` はフィールド名付きの struct literal で
組み立てているのでフィールド追加はソース互換であり、`Finding` は出力形式に
依存しない値であり、4 種の reporter と `ShouldFail`/`Counts` はすべて
`Severity` だけを見て動く。つまり新しいチェックが返す `Finding` は
コード変更なしにどの出力形式にもそのまま流れる。

一方で、「現在の構造に手を入れずに 2 つ目のチェックが差し込める」というのは
半分しか正しくない。実際には次の 2 点で構造の変更が要る:

- **CLI 層**: `cmd/tfgcpvalidator/validate.go` の `newCheckCmd` は汎用だが、
  束縛しているのは `validateOpts` だけである。`Check` 自身が独自フラグを
  登録するフックが無いため、`--project` のような固有フラグを要求する最初の
  チェックが来た時点で `newCheckCmd` の作り直しが要る。変更自体は小さく
  閉じているが、ゼロではない。
- **エラー処理**: `check.Run` は最初にエラーを返したチェックでバッチ全体を
  中断する。チェックが destroy の 1 つだけの今はこれで問題ないが、2 つ目
  として GCP に到達する SA 権限チェックを足すと、GCP に到達できないだけで
  `sa-permission` がエラーを返し、`destroy` が既に見つけていた Finding も
  丸ごと握りつぶされ、exit code 2 で終わる。チェックを追加したことで
  検証全体がむしろ安全でなくなる。2 つ目のチェックを出す前に、チェック単位で
  エラーを分離する仕組みと、チェックが「自分はスキップした」と申告できる
  何らかの手段が必要になる。
