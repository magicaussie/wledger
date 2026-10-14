# Inventory-only WLED output guard

WLED state output is controlled by the server capability `WLEDGER_PHYSICAL_OUTPUT_ENABLED`.

- Unset, empty, or any value other than `1`, `true`, `yes`, or `on` (case-insensitive) disables WLED state writes. This is the default for fresh installations and installations upgraded without configuration changes.
- An operator may explicitly set one of those values to opt in. Test opt-ins must point only at disposable databases and loopback mock controllers. This setting is not enabled automatically by an upgrade or migration.
- Read-only controller inspection/discovery and controller, wiring, allocation, and physical layout editing remain available while disabled.

While disabled, the server rejects modern physical drawer/bin Locate, legacy part/bin/drawer Locate, flash operations, and GlobalOff. The restriction is enforced by the shared WLED service/client write gate, by the physical Locate activation service, and by the authenticated JSON API used by MCP; hiding browser controls is supplementary only. These refusals do not create a new physical Locate session or change existing durable ownership.

**Recovery exception:** Startup recovery and an explicit Stop may still attempt cleanup for a previously persisted session whose controller target has `may_have_received` set. This path is not GlobalOff and cannot create an activation. Before a write, recovery reloads the saved session and target under the database operation claim, verifies the recorded endpoint, controller mapping fingerprint, ranges and cleanup commands, and permits only the saved targeted pixel-off command while the session is in `stopping` or `partial`. It clears a target's ownership marker only after the targeted write is acknowledged and that resolution is persisted. Missing, changed, unverified or failed targets remain unresolved and are retained for retry; disabling output alone never releases ownership. UI Stop is shown only for an existing session owned by the current user.

If there is no matching durable session/target, a target no longer matches its verified snapshot, or the target's saved cleanup commands are absent, the recovery write is refused. Inventory browsing, edits and QR operations do not require WLED output.
