# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 74
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 043 — Resume Authorized Deployment on Verified mainserver
Production-Authorization: USER_EXPLICITLY_AUTHORIZED_TASK041_DEPLOYMENT_NOT_YET_USED
Target-Host: spetchal@192.168.1.108
Expected-Hostname: mainserver
Target-Main: 5acb00678da633a40dc6a4e6ac17b60275c40b91
Expected-Running: a12d824e48da7c19b8ad508027898492b9d84c81

## Review
Sequence 73 independently fetched and accepted. Root cause: Task041 Sequence71 ran against workstation local Docker, NOT production. Verified mainserver via StrictHostKeyChecking=yes, both containers, original release, backup, goose10, 68 mapped bins. Resume original user-authorized deployment ONLY on verified mainserver. Authorization is for this one deployment, not future deployments.

## Hard host guard (BEFORE ANY mutation)
Run commands explicitly over ssh -o StrictHostKeyChecking=yes -o BatchMode=yes spetchal@192.168.1.108. In EVERY remote deployment script check:
  test "$(hostname -s)" = mainserver || exit 91
  test "$(docker info --format '{{.Name}}')" = mainserver || exit 92
  test "$(git -C /home/spetchal/wledger-release-a12d824 rev-parse HEAD)" = a12d824e48da7c19b8ad508027898492b9d84c81 || exit 93
  docker inspect wledger wledger-mcp >/dev/null || exit 94
STOP if identity/context/release diverges. Never use workstation local docker, even for preparation.

## Implementation-ready deployment
1. Reconfirm target git main SHA and read-only production preflight. Diff a12d824..5acb006 must remain tests, CI, package.json, generated CSS and optional .gitignore only; no migration/schema/LED logic changes. Verify existing app and MCP image IDs, running ports, mount symlinks, service topology, logs, DB integrity, goose10, counts, coordinate space drawer.
2. Mapping fingerprint: do NOT guess original Task035 digest. Before deployment, use sqlite3 connection opened mode=ro and snapshot mapping-relevant rows deterministically, ordered by primary keys, from controllers, containers, bins and relevant allocation/link tables; preserve schema definitions and exact JSON/CSV snapshot in root-protected NEW backup. Calculate SHA256 on canonical snapshot with explicit documented command. Compare exact same snapshot/hash after deployment, excluding nonmapping mutable fields only if clearly justified. If mapping fingerprint differs, STOP/ROLLBACK. Preserve legacy digest as historical reference only, not directly comparable.
3. Create fresh complete root-protected backup on mainserver BEFORE cutover: SQLite online backup API, DB integrity/FK/goose, uploads, config/.env, compose and symlink provenance, immutable image saves for BOTH app and MCP, rollback tags, manifest SHA256, restore notes. Verify all components and checksum manifest. Preserve pre035 complete backup and don't use incomplete pre035 partial. If sudo permissions insufficient, STOP rather than insecure backup.
4. Create clean isolated /home/spetchal/wledger-release-5acb006 checkout at exact commit, reuse known Task035 release strategy, bind existing data/uploads/logs without changing them. Build app/MCP images from exact SHA, verify CSS minified production form and spacing utilities. Before cutover validate backup and rollback images.
5. Recreate ONLY wledger and wledger-mcp on mainserver using established Compose service names and mounts. Do not use broad compose down/prune. Keep MCP loopback binding 127.0.0.1:9100 and app port 8090.
6. Verify containers healthy/restart stable, HTTPS login 200, app auth behavior, MCP 401 unauthorized/authorized as permitted, external MCP blocked, CSS utility presence, logs, SQLite integrity/FK/goose10, same mapping snapshot SHA256, counts 1 controller/2 containers/68 bins/68 mapped/0 unmapped and coordinate space drawer. No LED POSTs or physical actions. If mismatch, restore previous immutable images (DB restore only if absolutely required), validate rollback.
7. Publish Sequence 75 From DeepSeek To ChatGPT Status AWAITING_REVIEW with exact new backup path, checksum evidence, fingerprint method/hash before and after, release path, old/new image IDs, services touched, HTTP/MCP/CSS tests, migration/data invariants, rollback details, and remaining issues. STOP.

## Safety
DO NOT TOUCH local workstation WLEDger container, /home/spetchal/wledger original dirty checkout, Home Assistant, other Docker services, WLED/LED outputs, mappings, or live test data. No new Walls. If any unexpected state or missing backup prerequisites, STOP and report before cutover. User authorization consumed only if production deployment occurs.
