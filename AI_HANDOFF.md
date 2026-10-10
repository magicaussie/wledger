# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 60
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 036 — Isolated Dashboard Wall Demo and Browser Regression Test
Authorization: NONPRODUCTION_STAGING_ONLY
Target-Commit: a12d824e48da7c19b8ad508027898492b9d84c81
Production-Commit: a12d824e48da7c19b8ad508027898492b9d84c81

## User request
User explicitly said "go ahead with the demo test you mentioned then" after discussion of WLEDger Walls. Build and exercise a synthetic Wall demo in an isolated nonproduction environment, preserving live production and physical LED mappings. This does NOT authorize creating a Wall in production or triggering hardware.

## Required implementation
1. Inspect actual source for dashboard wall model, schema, routes, fixture expectations, UI templates, auth and test harness. Identify minimal valid synthetic dataset: one or more demo controllers, two demo containers and representative bins arranged on a Wall, with safe offline/fake controller IPs (RFC5737 TEST-NET e.g. 192.0.2.10), no actual hardware addresses. Do not copy production DB, secrets, cookies or tokens. Never use 192.168.1.40.
2. Prefer a disposable temporary database and local test server isolated on loopback, unique ports, dedicated temp uploads/logs and dummy secrets. If a running server is not necessary, render the dashboard via existing Go/templ test harness and inspect HTML/DOM, supplemented by a browser test against a disposable instance where feasible. Review migration/fixture schemas before writing data; synthetic fixtures ONLY in staging. Do not point staging at production SQLite or production volume paths.
3. Provide implementation-ready automated regression coverage in repo tests where appropriate: wall layout with two containers, one empty container, long names, localized OpenContainer label, unique modal refs/IDs, accessible phrasing-only button, modal open/close via card/close/backdrop, keyboard Tab+Enter, tall-grid vertical scroll, mobile viewport. Do not claim browser tests if only static render tests were run. If browser automation tools are unavailable, clearly identify remaining manual checks and provide screenshots only if actually captured.
4. Verify app build/vet/tests and existing CI if code changed. Any source/test changes go on separate branch and PR for review; do NOT merge into main automatically. If no code changes necessary, report test evidence and demo instructions. Prefer read-only repository work and disposable temp staging files. Avoid creating permanent demo data on the mainserver.
5. Safety: no changes to production DB, live Wall configuration, 68 mapped bins, Docker services, existing production release, Home Assistant, or physical WLED LEDs. No WLED/Locate/Global Off calls. No port binding to LAN, no remote hardware connections, no shared production credentials. Staging teardown after tests; retain only safe logs/screenshots or test artifacts.
6. Publish Sequence 61 From DeepSeek To ChatGPT Status AWAITING_REVIEW Task 036 with exact test fixture strategy, steps performed, assertions/results, browser automation coverage vs pending, any commit/PR, confirmation production unchanged and staging cleaned up. STOP.

## Important
User wants a demo test, not another production deployment. The Wall concept is a virtual dashboard arrangement; do not modify physical mapping or create real walls.
