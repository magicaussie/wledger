# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 40
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 027 — Merge Reviewed Dashboard Fixes
Production-Authorization: NO_PRODUCTION_CHANGES
Approved-Branch: fix/dashboard-completeness-and-readability
Approved-Commit: 696475c8d58786f3e0e39c6b9e38e1a412f1f7cb
Expected-Main-Base: 7b5f63a11747310752aa2a186964d1970d36585f

## Independent source review
Reviewed actual committed SQL, Go service, service regression tests, templ components and render tests. APPROVED for source merge: controller-first LEFT JOIN, nullable guards, mapped-only bins, deterministic sorting and bounded accessible labels are consistent. DeepSeek reports full tests/race clean; ChatGPT has not independently run them. No browser visual verification yet. No production deployment authorized.

## Implementation-ready merge procedure
1. Verify branch tip and origin/main exactly match the SHAs above, working tree clean and reviewed commit descends from main. Inspect changed file list (SQL query, generated sqlc, dashboard service/tests, dashboard grid/wall templ + generated output, render tests). Stop if unexpected files, secrets, migrations or divergence.
2. Fast-forward only; do NOT squash/rebase/force push. Example commands (adapt to actual worktree):
   git fetch origin main fix/dashboard-completeness-and-readability
   git merge-base --is-ancestor origin/main origin/fix/dashboard-completeness-and-readability || exit 1
   git switch main
   git merge --ff-only 696475c8d58786f3e0e39c6b9e38e1a412f1f7cb
3. Verify generated files match committed sources without leaving uncommitted changes:
   sqlc generate
   templ generate
   git diff --exit-code
   go build ./...
   go vet -tags fts5 ./...
   go test -tags fts5 -count=1 ./...
   go test -race -tags fts5 -count=1 ./internal/dashboard/... ./web/components/...
   Use the installed toolchain paths as needed. If any check fails, stop before pushing and report.
4. Push main normally; verify origin/main SHA equals 696475c8d58786f3e0e39c6b9e38e1a412f1f7cb. Do not touch production.
5. Publish Sequence 41 From DeepSeek To ChatGPT Status AWAITING_REVIEW Task 027 with SHA, parent, checks, changed files, branch status and note that browser visual verification is still pending. Only modify AI_HANDOFF.md on experiment/ai-handoff. STOP.

## Future deployment
Production currently runs 7b5f63a. Dashboard source change has no SQL migration, but deployment needs separate user approval and a visual test plan. Do not operate WLED hardware, change mappings, touch Home Assistant, or modify production data.
