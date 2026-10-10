# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 53
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 032 — Add Pull Request and Main Push CI
Production-Authorization: NO_PRODUCTION_CHANGES
Base-Main: e14c66622d41f28019268081715414c284ffcb2d
Implementation-Branch: ci/pr-push-validation
Implementation-Commit: a12d824e48da7c19b8ad508027898492b9d84c81
Pull-Request: https://github.com/magicaussie/wledger/pull/2
Result: CI ADDED ON BRANCH — LIVE ACTIONS RUN GREEN — NOT MERGED, NOT DEPLOYED

## Summary
Added `.github/workflows/ci.yml` (the only changed file) on a branch from exact
`origin/main` `e14c666`. Opened PR #2; the workflow ran live on GitHub Actions and
passed. Not merged, not deployed.

## Changed files (e14c666..a12d824)
- `.github/workflows/ci.yml` (new)

No app source, generated files, Docker, `release.yml`, schema/SQL/auth/router or
production changes.

## Workflow (as committed)
```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  validate:
    runs-on: ubuntu-latest
    timeout-minutes: 25
    env:
      CGO_ENABLED: "1"
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: "1.25.5"
          cache: true

      # `go install` writes to $(go env GOPATH)/bin, which is not guaranteed to
      # be on PATH on hosted runners; make it explicit for later steps.
      - name: Add Go bin to PATH
        run: echo "$(go env GOPATH)/bin" >> "$GITHUB_PATH"

      - name: Install generators
        run: |
          go install github.com/a-h/templ/cmd/templ@v0.3.977
          go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0

      - name: Verify committed generated Go
        run: |
          sqlc generate
          templ generate
          git diff --exit-code -- internal/db web
          test -z "$(git status --porcelain --untracked-files=normal -- internal/db web)"

      - name: Build
        run: go build -tags fts5 ./...

      - name: Vet
        run: go vet -tags fts5 ./...

      - name: Test
        run: go test -tags fts5 -count=1 ./...
```

## Deviation from the supplied YAML (and why)
- Added the `Add Go bin to PATH` step (`echo "$(go env GOPATH)/bin" >> "$GITHUB_PATH"`)
  before installing generators, because `go install` writes to `GOPATH/bin`, which
  is not guaranteed to be on PATH on hosted runners. The live run confirms the
  generators resolved.
- Otherwise the YAML matches the supplied proposal (triggers, permissions, timeout,
  CGO, pinned versions, generator check, build/vet/test with `-tags fts5`).

## Local validation
- **YAML parse:** PyYAML 6.0.1 with a raw-node key check (YAML 1.1 coerces `on` to
  boolean; verified the raw top-level keys are `name`/`on`/`permissions`/`jobs`) plus
  a structural check of triggers, permissions, job env/timeout and every step's
  command. PASS.
- **Generator determinism:** `sqlc generate` + `templ generate` → `updates=0`;
  `git diff --exit-code -- internal/db web` clean; no untracked files under
  `internal/db` or `web`. PASS.
- `go build -tags fts5 ./...` → OK.
- `go vet -tags fts5 ./...` → clean.
- `go test -tags fts5 -count=1 ./...` → all packages ok, 0 FAIL.

## Live GitHub Actions result — GREEN
- PR: https://github.com/magicaussie/wledger/pull/2 (OPEN, MERGEABLE).
- Run: https://github.com/magicaussie/wledger/actions/runs/38044812260 (job 114192089989).
- Trigger: `pull_request`. Result: **success** in 2m30s. All steps passed:
  Set up job, Checkout, Set up Go, Add Go bin to PATH, Install generators,
  Verify committed generated Go, Build, Vet, Test, Post steps, Complete job.
- `gh pr checks 2` → `validate  pass  2m30s`.

## Annotations (non-blocking)
- Node.js 20 deprecation: `actions/checkout@v4` and `actions/setup-go@v5` target
  Node.js 20 and are being forced onto Node.js 24. Informational; consider bumping
  to newer action majors in a follow-up.
- `ubuntu-latest` will migrate to Ubuntu 26 from 2026-10-19. Informational.

## Limitations / notes
- The workflow triggers on `pull_request` and pushes to `main`; the `push` branch
  path was not exercised by this PR (only `pull_request` ran). The same job runs on
  main pushes after merge.
- `go install .../sqlc@v1.29.0` compiles sqlc from source; the live run completed
  well within the 25-minute timeout, so no fallback to a prebuilt binary was needed.
- No secrets, read-only permissions, no Docker publishing, no production access.

## Recommended follow-up
- Independent review of the workflow and PR #2; then fast-forward merge to main
  (separate, explicitly authorised step).
- Optional: bump `actions/checkout`/`actions/setup-go` majors to clear the Node 20
  deprecation warning.

## Evidence / SHAs
- Base / main: `e14c66622d41f28019268081715414c284ffcb2d`
- Branch: `ci/pr-push-validation`
- Commit: `a12d824e48da7c19b8ad508027898492b9d84c81`
- Parent: `e14c66622d41f28019268081715414c284ffcb2d`
- PR: https://github.com/magicaussie/wledger/pull/2
- Actions run: https://github.com/magicaussie/wledger/actions/runs/38044812260
