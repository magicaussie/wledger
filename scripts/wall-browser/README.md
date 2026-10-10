# Wall browser regression (optional)

An optional, manual browser regression for the dashboard **Wall** UI. It exists
because the Go render/integration tests can only assert static HTML; the
behaviours that matter here (modal open/close, keyboard activation, vertical
scrolling, mobile bottom-sheet layout) are driven by Alpine.js and DaisyUI in a
real browser.

It is **not** part of the Go CI suite: it needs Node, Playwright and a running
harness, none of which are project dependencies.

## What it covers

- Wall renders with two containers (one populated 8x8 grid, one empty).
- Container card opens exactly one scoped modal (`dialog[open]`).
- Close button, backdrop click and `Escape` all close the modal.
- The card is reachable by `Tab` and opens with `Enter`.
- The card carries a localized `Open container: <name>` `aria-label` and
  `aria-haspopup="dialog"`.
- No duplicate element ids; each container has its own dialog.
- The tall grid overflows the modal-box and scrolls vertically.
- The empty container shows the localized "No bins mapped to this container."
  state and renders no bin links.
- Mobile viewport (390x844) uses the bottom-sheet modal layout.

## Safety

The harness is disposable and non-production:

- Binds to `127.0.0.1` only (never a LAN interface).
- Uses a temp SQLite database, temp uploads and temp logs; never `./data` or
  `./app`.
- Seeds a controller on `192.0.2.10` (RFC5737 TEST-NET-1); no real hardware
  address is used and no WLED/LED call is made.
- The temp staging directory is removed on exit.

## Running

From the repository root:

```sh
# Terminal 1 — start the disposable harness (loopback only).
go run -tags fts5 ./scripts/wall-browser -port 18080

# Terminal 2 — drive it with Playwright.
DEMO_URL=http://127.0.0.1:18080 \
  node scripts/wall-browser/wall_browser.test.js
```

If Playwright is not resolvable from the current directory, point `NODE_PATH` at
an install, for example:

```sh
NODE_PATH="$(npm root -g)" node scripts/wall-browser/wall_browser.test.js
```

Screenshots and `results.json` are written to `./screenshots` (override with
`SHOT_DIR`). The script exits non-zero if any check fails.
