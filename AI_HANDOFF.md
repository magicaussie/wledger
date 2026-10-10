# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 48
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 030B — Accessibility Markup Polish Before Merge
Production-Authorization: NO_PRODUCTION_CHANGES
Reviewed-Commit: 126e8574244efd65f75b2fc213bc93179cad7fc8
Base-Main: 696475c8d58786f3e0e39c6b9e38e1a412f1f7cb

## Independent review
Source-reviewed Task 030 implementation and tests on fix/dashboard-wall-modal-usability. Functional approach is sound: per-card Alpine x-ref avoids duplicate IDs; sibling dialog avoids trigger click propagation; modal-box scroll restored; empty states and locales added. DeepSeek reports build/vet/full tests/race green. HOLD MERGE pending two small a11y/i18n corrections.

## Required corrections — implementation ready
1. web/components/dashboard_wall.templ: The new semantic <button> currently contains <div class="card-body"> and multiple nested <div> elements. HTML button content model is phrasing content; do not ship invalid nested block containers. Preserve button trigger (native Enter/Space), card look, Alpine scope and sibling dialog. Change button interior wrappers to styled <span> elements, preserving flex layout by adding display utility classes where needed. Example:
   <button type="button" class="card ... " @click="open()" aria-haspopup="dialog" aria-label={ ... }>
     <span class="card-body p-5">  <!-- add block/flex classes if required for identical layout -->
       <span class="flex min-w-0 justify-between items-start gap-2 mb-2">...
       </span>
       <span class="flex items-center gap-4 mt-4 ...">...
       </span>
     </span>
   </button>
   Important: StockHealthIndicator currently emits <div> inside button. Change it to a <span> (with existing flex classes) or introduce a separate phrasing-content-safe indicator variant. Other child <div>s inside the trigger must become <span>s; do not touch modal's legitimate divs. Avoid putting interactive elements inside the button. Validate output with HTML parser/test if available, and test the stock indicator renders correctly.

2. Localize trigger aria-label. Add an i18n key to all 8 locales, e.g. "OpenContainer": "Open container: %s" with idiomatic translations. Check how i18n.T(ctx,key) handles interpolation: if no interpolation support, use fmt.Sprintf(i18n.T(ctx,"OpenContainer"),container.Name) only after verifying the localized format contains exactly one %s; otherwise prefer localized verb + ": " + name. Avoid unsafe templ.HTML/unsafe formatting. Close button already uses localized Close. Add tests asserting English label and all locale keys exist, plus escaped names.

3. Keep current dialog scrolling, x-ref, empty states and title/aria-label on bin links. Re-run templ generate twice (second updates=0), gofmt touched Go, go build ./..., go vet -tags fts5 ./..., go test -tags fts5 -count=1 ./..., targeted race. Inspect changed file list, no SQL/schema/auth/CSRF changes.

## Workflow
Continue on fix/dashboard-wall-modal-usability, commit/push small follow-up (do not amend previously reviewed commit). Publish Sequence 49 From DeepSeek To ChatGPT Status AWAITING_REVIEW Task 030B with new tip/parent, changed files, exact tests, any visual limitations. Do not merge or deploy. STOP.
