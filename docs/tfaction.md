# tfaction

[tfaction](https://github.com/suzuki-shunsuke/tfaction) already runs `terraform
plan` and `terraform show -json` for you, and exposes the path of the resulting
JSON as a step output. Point `tfgcpvalidator` at it — nothing in tfaction itself
has to change.

## tfaction v2

```yaml
- name: Plan
  id: plan
  uses: suzuki-shunsuke/tfaction@v2
  with:
    action: plan
    github_token: ${{ steps.token.outputs.token }}

- uses: nakamasato/tfgcpvalidator@v0
  with:
    plan: ${{ steps.plan.outputs.plan_json }}
    target: ${{ matrix.target.target }}
```

`plan_json` is set at run time and is not listed under `outputs` in tfaction's
`action.yaml`, which does not stop the expression from resolving.

## tfaction v1

Same shape, different action and output name:

```yaml
- name: Plan
  id: plan
  uses: suzuki-shunsuke/tfaction/terraform-plan@v1
  with:
    github_token: ${{ steps.token.outputs.token }}

- uses: nakamasato/tfgcpvalidator@v0
  with:
    plan: ${{ steps.plan.outputs.plan_json_path }}
    target: ${{ matrix.target.target }}
```

## Notes

- `target` separates this call's pull request comment from the other targets'
  running on the same pull request. tfaction plans one target per matrix job, so
  without it every job overwrites the same comment. `${{ env.TFACTION_TARGET }}`
  works too, and is the value the matrix sets.
- The path is absolute and lives under the runner's temporary directory, so the
  step runs from the repository root regardless of which root module was
  planned.
- Do not add `if: always()`. A failed plan produces no JSON, and the step would
  fail on a missing file on top of the failure that matters.
- The comment needs `pull-requests: write` on the job, the same permission
  tfaction needs for tfcmt.
