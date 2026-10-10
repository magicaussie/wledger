# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 6
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 011
Production-Authorization: NONE
Baseline: 63016f4e75134ae0675934818fb77f53dd463800

## Objective
Complete a read-only deployment readiness check for MCP connectivity and prepare a precise Stage A backup runbook. Do not perform production writes.

## Independent GitHub source review
ChatGPT directly inspected `docker-compose.yaml`, `cmd/mcp-server/main.go`, and `cmd/mcp-server/auth.go` at baseline. The baseline compose sets `MCP_HTTP_ADDR=:9100` **inside** the container, publishes `127.0.0.1:9100:9100` **on the host**, and requires bearer authentication for inbound MCP. These are distinct settings. Changing the host port publication can break remote clients even if the service starts normally.

## Instructions
1. Read AGENTS.md and latest origin/main. Reconfirm Task 010 conclusions as needed.
2. Identify actual MCP consumers using read-only checks of available configuration, reverse proxy routes, service definitions and access logs. Do not attempt password guessing or credential disclosure. If Home Assistant cannot be inspected, mark its dependency UNKNOWN.
3. Propose a secure, minimally disruptive MCP transition: confirm whether any remote clients exist, how their URLs and bearer credentials would change, and whether HTTPS proxying or LAN-only access is feasible. Do not recommend publishing an unauthenticated service.
4. Examine the reported hard-coded Algolia key in the untracked production diagnostic **without printing or copying its value**. Determine whether it is a public/search-only key or privileged/admin key only if possible from safe metadata or non-secret code context; otherwise mark UNKNOWN. Recommend rotation if exposure is plausible. Do not change or delete files or credentials.
5. Draft an executable Stage A backup runbook that preserves the currently running Docker images, a WAL-consistent SQLite snapshot, uploads, configuration, and production-only source/diagnostics securely. Include permissions, hashes, verification, failure handling, and exact rollback asset inventory. Do not execute the backup or alter production.
6. Provide a concise decision table: what is VERIFIED, what is UNKNOWN, and what explicit approval is needed for Stage A.

## Restrictions
Read-only investigation only. No production writes, Docker image tags/saves, backups, restarts, deployment, DB migrations, credential rotation, LED operations, code changes or main-branch commits. Do not disclose tokens or secrets.

On completion, replace the entire AI_HANDOFF.md with Sequence 7, From DeepSeek, To ChatGPT, Status AWAITING_REVIEW and the evidence-based findings. Fetch and verify branch tip before writing; commit only this file and push only experiment/ai-handoff without force. Stop on conflict.
