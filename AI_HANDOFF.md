# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 66
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 039 — CSS Artifact Determinism and GitHub Actions Maintenance
Production-Authorization: NO_PRODUCTION_CHANGES
Expected-Main: 16e91f796c5e0c9b06884d55a3402e58e04365d2

## Independent assessment
ChatGPT fetched Sequence 65, CI YAML, package.json, Dockerfile, and .gitignore at main. CI currently uses checkout@v4 and setup-go@v5, Go 1.25.5, templ v0.3.977, sqlc v1.29.0. package.json build:css emits unminified CSS, while production Dockerfile generates CSS with --minify using npm ci. Tracked web/static/css/output.css is stale. This task must use a SINGLE canonical CSS generation command matching Docker's --minify, and verify it in CI; do not accidentally require unminified output while production uses minified output.

## Implementation-ready development task
1. Verify exact origin/main SHA and clean tree. Create branch chore/css-ci-maintenance. Scope: .github/workflows/ci.yml, package.json (and package-lock.json only if required), web/static/css/output.css (generated artifact), optionally small focused docs/test. No application logic, Dockerfile, migrations, SQL, auth, WLED, or production config changes.
2. In package.json retain existing build:css for local use if desired, but add canonical script:
   "build:css:prod": "tailwindcss -i ./web/static/css/input.css -o ./web/static/css/output.css --minify"
   Verify npm ci makes tailwindcss CLI available (package has @tailwindcss/cli); if CLI name differs use verified npx @tailwindcss/cli command from Dockerfile. Prefer EXACT Docker equivalent:
   "build:css:prod": "npx @tailwindcss/cli -i ./web/static/css/input.css -o ./web/static/css/output.css --minify"
   npm ci && npm run build:css:prod
   Ensure .gap-x-4/.gap-y-1 present in output. Re-run canonical command and assert git diff --exit-code -- web/static/css/output.css after staging expected artifact. Ensure no generated secrets.
3. Update CI to run npm ci and npm run build:css:prod, then git diff --exit-code -- web/static/css/output.css and untracked check. Keep Go/sqlc/templ checks and fts5 tests unchanged. Use actions/setup-node@v4 with node-version '22' and cache: npm; check Node version compatibility with Tailwind dependencies. Modernize actions/checkout@v5 and actions/setup-go@v6 (Node24 actions) if supported on GitHub-hosted runner; verify official release compatibility or via live PR run. Do not blindly upgrade if incompatible; record rationale.
4. Validate YAML on key 'on' (PyYAML YAML1.1 trap), npm ci reproducibility, two consecutive CSS builds deterministic, CSS utilities present, templ/sqlc deterministic, Go build/vet/full tests. Open PR for live CI; wait for result. If generated CSS unexpectedly huge or nondeterministic, diagnose rather than suppress drift.
5. Note Dockerfile still invokes equivalent npx command; ensure canonical CSS command is byte-identical to Docker CSS stage given same lockfile/dependencies. Avoid changing Dockerfile for this task. Explain whether new tracked CSS matches production minified form.
6. Publish Sequence 67 From DeepSeek To ChatGPT Status AWAITING_REVIEW Task 039 with exact commit, parent, changed paths, CSS bytes/diff, test results, PR URL, CI run URL and any warnings. DO NOT MERGE; STOP.

## Guardrails
No production deployment, restart, live DB change, Wall creation, physical LED/WLED calls, Home Assistant changes, production checkout edits, secrets, or unrelated service modifications. No automatic production deployment on merge.
