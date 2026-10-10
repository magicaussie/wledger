# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 4
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 010
Production-Authorization: NONE
Baseline: 63016f4e75134ae0675934818fb77f53dd463800

## Objective
Resume WLEDger work after Task 009 production preflight. Investigate and prepare a decision-ready, non-destructive resolution plan for deployment blockers B1–B5. **Do not perform production changes.** This task is investigative only.

## Verified background from Task 009 report (recheck before relying on it)
- Production on mainserver at /home/spetchal/wledger is at d7b5690, 14 commits behind approved main, with uncommitted API/MCP changes and untracked files.
- Production SQLite database is WAL-mode, goose v9; 1 controller, 2 drawers, 68 bins, 2 parts. No verified pre-upgrade database backup.
- Current production MCP publishes port 9100 on all interfaces, while repository compose binds localhost.
- Application startup at current main applies migration 010 and backfills drawer allocations; coordinate conversion must remain separate and explicitly authorized.
- Main branch and production working tree must not be modified during this investigation.

## Work requested
1. Read AGENTS.md and inspect current origin/main, without changing it.
2. **B1 dirty checkout:** Obtain a precise, sanitized inventory of production tracked diffs and untracked paths. Identify what functionality the changes implement, whether they exist in newer GitHub source, and what would be lost in a clean deployment. Do not display secrets, sensitive config values, or contents of user data. Do not stash, clean, commit, reset, or alter production files.
3. **B3 MCP:** Trace actual MCP service configuration and identify whether any known clients rely on external port 9100. Read-only evidence only; mark client dependence UNKNOWN if not verifiable.
4. **B4/B5 rollback:** Determine how the old v9 application handles a database at goose v10, using source inspection and optionally a disposable local test with synthetic data. Never use production DB as a test target or make a production copy without separate authorization.
5. **B2 backup:** Draft exact safe, WAL-consistent backup and verification procedure, including how to preserve the current running image and uncommitted production source for rollback. **Do not execute backup or write to production.**
6. Recommend a deployment approach that preserves any needed production-only changes and explicitly maintains current MCP connectivity.
7. State which production actions require explicit user authorization and propose the smallest next approval gate.

## Constraints
- Read-only production inspection permitted; no writes, restarts, migrations, backups, restores, LED commands or coordinate conversions on production.
- No commit or push to main. No application source edits.
- Do not change AI_HANDOFF.md until final response. Then replace the entire file with Sequence 5, From DeepSeek, To ChatGPT, Status AWAITING_REVIEW, and a concise report containing evidence, risks, and decision requests.
- Before writing, fetch the branch and check for concurrency; commit only AI_HANDOFF.md and push only experiment/ai-handoff, without force.
- Stop if remote changed or evidence is insufficient; report unknowns instead of guessing.

## Acceptance criteria
- B1–B5 addressed with VERIFIED/INFERRED/UNKNOWN distinctions.
- Production-only source differences understood and no data/production mutations.
- Concrete rollback and backup strategy, no premature deployment.
- GitHub handoff contains a concise report and exact commit SHA for any relevant code inspected.
