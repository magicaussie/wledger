# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 46
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 030 — Dashboard Wall Modal Usability and Accessibility
Production-Authorization: NO_PRODUCTION_CHANGES
Base-Commit: 696475c8d58786f3e0e39c6b9e38e1a412f1f7cb

## Goal
Fix source-verified wall modal overflow, keyboard activation, click propagation, duplicate IDs, and empty-bin display without changing dashboard data, LED behaviour, SQL, or production. User prefers autonomous scoped development and implementation-ready handoffs. Create a dedicated branch from main; commit/push for independent review; do NOT merge or deploy.

## Source-verified defects
- web/components/dashboard_wall.templ: DashboardContainerCard wraps a dialog inside a clickable non-focusable <div> with @click using document.getElementById('container_modal_<id>').showModal(). Same container can appear on different walls; repeated id makes global lookup ambiguous. Dialog event bubbles to card.
- Same file modal-box has overflow-hidden, defeating DaisyUI vertical scroll; existing grid wrapper overflow-x-auto is useful.
- web/components/dashboard_grid.templ: empty container shows a blank bordered grid.
- web/components/dashboard_render_test.go: current tests cover long-name bounding and empty controller, not the modal cases.
- web/pages/dashboard.templ calls DashboardContainerCard(container) for each wall. No wall id parameter needed if Alpine per-card x-ref is used.

## Recommended patch shape (illustrative, adapt to actual templ/Alpine syntax)
Prefer semantic <button type="button"> as card trigger rather than role=button on outer div. Keep the <dialog> OUTSIDE the <button>, but inside per-card Alpine x-data scope. Example structure:

<div x-data="{ open() { $refs.modal.showModal() } }" class="...">
  <button type="button" class="card w-full text-left ... group" @click="open()" aria-haspopup="dialog" aria-label={ fmt.Sprintf("Open %s", container.Name) }>
    <div class="card-body p-5"> ... existing header and stock indicator ... </div>
  </button>
  <dialog x-ref="modal" class="modal modal-bottom sm:modal-middle" @click.stop>
    <div class="modal-box max-w-4xl p-0 border border-base-300 shadow-2xl bg-base-100">
      ... modal title, close form, grid ...
    </div>
    <form method="dialog" class="modal-backdrop"><button type="submit">close</button></form>
  </dialog>
</div>

Native button supplies keyboard Enter/Space automatically. The x-ref is scoped to each card, so repeated container IDs across walls are safe and no global DOM IDs needed. Ensure Alpine component scope and templ-generated output work. If changing existing outer card CSS affects appearance, retain equivalent classes on the button; no nested interactive elements inside button. Modal should be sibling of button and must not be inside a clipping/overflow-hidden ancestor if this affects top-layer rendering. Test with Alpine installed in Base.

For accessibility, use dialog aria-labelledby with unique IDs ONLY if uniqueness guaranteed (e.g. wall+position) or use aria-label={ fmt.Sprintf("%s bins",container.Name) } instead. Close button aria-label="Close"; type="submit" in dialog form. Avoid hardcoded English if existing i18n keys apply. Ensure dialog scrolls vertically (remove overflow-hidden on modal-box, keep scrollable DaisyUI defaults), and grid scrolls horizontally on narrow viewports. If CSS utility rounded-t-box is unavailable, omit rather than introduce an unbuilt class.

Empty bin state: inside BOTH dashboard wall modal and legacy DashboardGrid per-container grid, use
if len(container.Bins) == 0 {
  <p class="py-6 text-center text-sm opacity-70">No bins mapped to this container.</p>
} else {
  ... existing grid ...
}
Prefer existing i18n key if available; if adding a key, update all relevant locale dictionaries and tests. Preserve grid layout/positions and links when bins exist.

## Regression tests
Extend web/components/dashboard_render_test.go:
1. Modal HTML lacks duplicate container_modal_ IDs even when rendering two DashboardContainerCard instances with the same container ID (and each has its own Alpine x-ref).
2. Trigger is semantic button with type="button", aria-haspopup="dialog"; no dialog nested inside button; no @click handler on parent that receives modal click events.
3. Modal-box not overflow-hidden; long grid retains overflow-x-auto and scrolling CSS; dialog close and backdrop form remain.
4. Empty wall modal and empty legacy container show explicit text; long-name title/aria-label regression tests remain green.
5. Optionally add browser-based manual smoke instructions; do not claim visual validation without browser.

Run templ generate, verify deterministic generated Go, go build ./..., go vet -tags fts5 ./..., go test -tags fts5 -count=1 ./..., targeted race tests; gofmt only touched Go. Check no SQL/schema changes, no changes to authorization/CSRF. Commit and push fix/dashboard-wall-modal-usability, publish Sequence 47 From DeepSeek To ChatGPT Status AWAITING_REVIEW with SHA, parent, tests, risk/visual status. STOP.

## Guardrails
No production changes, migrations, DB writes, LED/WLED commands, Home Assistant changes, unrelated formatting, or merge to main. Main remains 696475c until independent review.
