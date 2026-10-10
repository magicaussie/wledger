# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 14
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 015 — Make Drawer Locate Manual-Only
Production-Authorization: NONE
Baseline: 63016f4e75134ae0675934818fb77f53dd463800

## User approval
The user explicitly approved a development task to remove automatic LED activation on drawer page load, while keeping the Locate button. This is SOURCE DEVELOPMENT ONLY. No production deployment, restart, migration, DB write, hardware command, or LED coordinate conversion is authorized.

## Task
1. Read AGENTS.md and verify Git status, branches, baseline, existing changes, and applicable instructions before editing. Preserve all unrelated work.
2. Inspect the actual drawer page template and handler, especially `web/pages/drawer.templ`, generated templ output if applicable, and `POST /drawers/{id}/locate`. Identify the element using `hx-trigger="load"` or equivalent page-load trigger.
3. Remove only the automatic locate request on drawer page load. Preserve an explicit, clearly labeled Locate control that initiates the same locate endpoint only upon a deliberate user action. Avoid introducing an implicit HTMX trigger or accidental auto-execution. Keep existing authorization/CSRF semantics and other drawer behavior.
4. Add focused regression tests that prove rendering/loading a drawer page does not auto-trigger LED locate and the explicit Locate button remains wired correctly. Where feasible, cover the generated template output and ensure there are no other page-load locate triggers for the drawer view. Do not test by sending physical WLED commands.
5. Regenerate templ code if the project requires it; run relevant tests, formatting and build checks. Inspect resulting diff for unrelated changes. If a more substantial redesign is needed, stop and report rather than broadening scope.
6. Commit the coherent fix on an appropriate DEVELOPMENT branch (not the handoff branch), push it, and report commit SHA, parent, branch, files changed, tests/results and remaining risks. Do NOT merge to main or deploy to production; await independent ChatGPT review and further user authorization.

## Handoff protocol
After the development commit is pushed, fetch and verify the handoff branch tip for concurrency. Replace this entire file with Sequence 15, From DeepSeek, To ChatGPT, Status AWAITING_REVIEW, Task 015. Include the exact development branch and SHA, succinct evidence and test outcomes. Commit/push ONLY AI_HANDOFF.md on experiment/ai-handoff without force. No production operations.

## Boundaries
- No manual LED coordinate conversion, hardware commands, production changes, Home Assistant integration, or unrelated refactors.
- Keep source changes isolated and independently reviewable.
- Do not begin the next task automatically.
