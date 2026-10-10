# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 45
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 029 — Read-Only Post-Deployment Audit and Next-Priority Plan
Production-Authorization: READ_ONLY_ONLY (respected)
Production-Commit: 696475c8d58786f3e0e39c6b9e38e1a412f1f7cb
Result: AUDIT PASS — no production changes; UX findings and CI proposal below

## 1. Read-only production audit — PASS (confirms Sequence 43)
- Release HEAD `/home/spetchal/wledger-release-696475c` = `696475c8d58786f3e0e39c6b9e38e1a412f1f7cb`.
- Containers `wledger` / `wledger-mcp`: `Up`, RestartCount **0**, state running; images `sha256:5b9e68f2…` / `sha256:21407226…`; compose `working_dir` = `/home/spetchal/wledger-release-696475c`.
- Logs: `goose: no migrations to run. current version: 10`; **0 ERROR** lines in both.
- Web: `https://storage.localdomain/` → 303 → `/login` (200, `<title>WLEDger - Login</title>`).
- API `/api/v1/health`: 401 without token, 200 with token. MCP `POST /mcp`: 401 without token, 200 with token; listener `127.0.0.1:9100` only (external blocked).
- DB: `integrity_check` ok, `foreign_key_check` CLEAN, goose 10, `led_coordinate_space=drawer`; counts 1/2/68/2/2/15/1/23 (controllers/containers/bins/parts/assignments/audit/users/sessions); bins mapped 68/0; digest `1b0f9bd7b09223548df0f7097bec0ffa8b57d0c85f31979ad2c5db821ece8f09` (unchanged).
- No credentials/tokens/cookies printed; no authenticated session created; no WLED calls.

## 2. Dashboard UX findings (source on `main` @ 696475c)

### P1 — Wall modal cannot scroll a tall grid (overflow clipping)
`web/components/dashboard_wall.templ` line 33 sets `class="modal-box max-w-4xl p-0 overflow-hidden …"`.
DaisyUI's `.modal-box` provides `overflow-y: auto` and `max-height: calc(100vh - 5em)` (see
`web/static/css/output.css` lines 2408/2421/3041/4683). Tailwind's `overflow-hidden` utility wins over the
component rule, so a container whose grid is taller than the viewport is **clipped with no scrollbar**.
This is the most likely real-world defect (e.g. a 68-bin container with many rows on a short/mobile viewport).
Suggested fix (restore DaisyUI scroll, keep rounded top corners):
```diff
- <div class="modal-box max-w-4xl p-0 overflow-hidden border border-base-300 shadow-2xl bg-base-100">
+ <div class="modal-box max-w-4xl p-0 border border-base-300 shadow-2xl bg-base-100">
-   <div class="bg-base-200 p-6 border-b border-base-300 flex flex-wrap gap-2 justify-between items-start">
+   <div class="bg-base-200 p-6 border-b border-base-300 flex flex-wrap gap-2 justify-between items-start rounded-t-box">
```
(Verify `rounded-t-box` resolves in Tailwind v4 + DaisyUI 5; fallback: keep `overflow-hidden` on the header only.)

### P1 — Card is not keyboard-activatable
`web/components/dashboard_wall.templ` lines 10–13: the card is a `<div>` with `@click` and no `role`/`tabindex`,
so keyboard-only users cannot open the modal (no Enter/Space activation, not focusable).
Suggested fix (also removes the global-ID lookup, see P2):
```diff
  <div
    class="card … cursor-pointer group overflow-hidden"
-   @click={ fmt.Sprintf("document.getElementById('container_modal_%d').showModal()", container.ID) }
+   role="button" tabindex="0" aria-haspopup="dialog"
+   x-data="{ open() { $refs.modal.showModal() } }"
+   @click="open()" @keydown.enter.prevent="open()" @keydown.space.prevent="open()"
  >
```
and on the dialog: `<dialog x-ref="modal" class="modal …" @click.stop>` so clicks inside the dialog do not
re-trigger the card handler.

### P2 — Modal nested inside the clickable card (click propagation)
The `<dialog>` (lines 32–79) is a DOM child of the card `<div>` that owns `@click`. Because event bubbling
follows the DOM tree (not the top layer), clicks inside the dialog — header, legend, backdrop, ✕ — bubble to
the card and call `showModal()` again. Today this is masked because `showModal()` on an already-open modal
dialog is a spec no-op, and the ✕/backdrop default action still closes it. It is fragile: any future switch to
non-modal `show()` would throw `InvalidStateError`, and it makes close behaviour depend on event ordering.
Fix: `@click.stop` on the dialog (above) or move the dialog out of the card.

### P2 — Duplicate modal IDs when a container is on multiple walls
Modal id is `container_modal_<container.ID>` (line 32). `wall_cards` has no uniqueness on `container_id`
(`sql/schema/004_multi_container_hierarchy.sql`), `AddContainerToWall` (`sql/queries/walls.sql` line 20) does a
plain insert, and `HandleWallEdit` passes **all** containers (`internal/handler/walls.go`, `GetAllContainers`).
So the same container can be linked to two walls → two dialogs with the same `id` → invalid HTML and
`getElementById` returns the first (wrong card opens the wrong modal). Fix: use Alpine `x-ref` (per-card scope)
instead of a global id, or suffix the id with the wall id.

### P3 — Empty-container rendering has no empty state
- Legacy grid (`web/components/dashboard_grid.templ` lines 143–159): a container with zero mapped bins renders
  an empty bordered box with no message.
- Wall modal (`web/components/dashboard_wall.templ` lines 54–66): same — an empty grid, no message.
Suggested fix: render a small empty-state (`i18n.T(ctx, "NoBinsMapped")`) when `len(container.Bins) == 0`.

### P3 — Minor
- `cursor-pointer` on the card is inherited into the modal content (cosmetic pointer over non-interactive areas).
- The ✕ button (line 50) has no `aria-label`; the dialog has no `aria-labelledby` pointing at the title.
- `overflow-hidden` on the card does **not** clip the modal (top layer escapes it) — noted so it is not
  "fixed" unnecessarily.

## 3. CI proposal — `.github/workflows/ci.yml` (new)
Current: `.github/workflows/release.yml` builds/pushes Docker images on tags only; `deploy-docs.yml` deploys docs.
No PR/push validation exists. Pinned versions confirmed from source: Go `1.25.5` (`go.mod`), templ `v0.3.977`
(generated header + `Dockerfile`), sqlc `v1.29.0` (generated header + `Dockerfile`), CGO required for
`mattn/go-sqlite3`, tests use `-tags fts5`.
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
    env:
      CGO_ENABLED: "1"
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.25.5"
          cache: true
      - name: Install pinned generators
        run: |
          go install github.com/a-h/templ/cmd/templ@v0.3.977
          go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0
      - name: Verify generated code is committed
        run: |
          templ generate
          sqlc generate
          if ! git diff --exit-code; then
            echo "::error::Generated code is stale. Run 'templ generate' and 'sqlc generate' and commit."
            exit 1
          fi
      - name: Build
        run: go build ./...
      - name: Vet
        run: go vet -tags fts5 ./...
      - name: Test
        run: go test -tags fts5 -count=1 ./...
      # Optional (cheap): JS unit test
      # - run: node web/tests/grid_painter.test.js
```
Notes: `ubuntu-latest` ships gcc so CGO builds work; pinning templ/sqlc to the committed versions is required
or the generated-file diff check will false-positive on the version header. CSS determinism is intentionally
out of scope (the `Dockerfile` regenerates `output.css` at build time), but could be added later with
`npx @tailwindcss/cli` + `git diff --exit-code web/static/css/output.css`.

## 4. User-safe browser checklist (still pending; no credentials used)
Task 025 residual:
1. Log in at `https://storage.localdomain/` in a browser; open DevTools → Application → Cookies; confirm the
   session cookie has the **Secure** attribute (and HttpOnly/SameSite as configured).
2. From the UI, run each Locate action (hardware/bin, part, drawer) and Global Off; confirm each succeeds with
   the CSRF token present (no 403) — **do not** trigger physical LEDs unless separately authorised.
3. Log in as a `viewer` account; confirm the three Locate actions and Global Off return 403 / are hidden.
Dashboard visual test (Task 026/028):
4. Legacy view (0 walls): confirm long controller/container/bin names truncate with full text on hover
   (`title`), and an empty controller shows "No containers configured.".
5. Wall view: open a container card modal; confirm the ✕ and backdrop close it; confirm a tall grid scrolls
   (P1); confirm keyboard Tab+Enter opens a card (P1); check desktop and mobile widths.
6. Confirm no console errors and no duplicate `id="container_modal_*"` in the DOM (P2).

## 5. Recommended next scoped task
**Task 030 — Dashboard wall modal usability & accessibility** (development only, no deploy):
- Fix P1 overflow scrolling, P1 keyboard activation, P2 click propagation + duplicate IDs, P3 empty-container
  state, and the minor aria-label items in `web/components/dashboard_wall.templ` / `dashboard_grid.templ`.
- Add regression tests in `web/components/dashboard_render_test.go`:
  - modal-box does not carry `overflow-hidden` (or carries `overflow-y-auto`);
  - card exposes `role="button"`, `tabindex="0"`, `aria-haspopup="dialog"`;
  - two cards for the same container id do not emit duplicate `id="container_modal_N"`;
  - an empty container renders the empty-state message.
- Then land the CI workflow (section 3) as a separate small task (Task 031) so it can gate the above.

## Boundaries respected
Read-only production inspection only. No DB writes, backups/restores, deployment, restarts, merges, pushes to
main, hardware LED commands, Home Assistant changes or unrelated service modifications. Only `AI_HANDOFF.md`
was committed to `experiment/ai-handoff`.
