# AGENTS.md — WLEDger Development Policy

This file records the development, publication and review policy for WLEDger.
It applies to all contributors and automated agents working in this repository.

## GitHub synchronization and technical review

GitHub is the authoritative published source for WLEDger.

1. Keep the full application source, tests, migrations, configuration templates and documentation under version control.
2. Never commit secrets, production databases, user uploads, logs, credentials, build artifacts or model files.
3. At the end of each coherent development task, run the relevant tests and create a local commit.
4. Publish approved commits to origin/main after verifying a clean working tree and fast-forward compatibility.
5. For substantial tasks, create intermediate review checkpoints rather than accumulating large unreviewed changes.
6. After each published task, report:
   - Full commit SHA
   - Parent SHA
   - Changed files
   - Test results
   - Known risks
   - GitHub synchronization status
7. Provide the technical lead with the commit SHA so ChatGPT can independently inspect the published source through GitHub.
8. Do not treat an implementation report or passing tests as a substitute for independent source review.
9. Do not deploy, modify production databases, perform restores or convert LED coordinates merely because a commit was pushed.
10. Production operations always require separate explicit authorization.
11. Independent reviews must inspect relevant source files, tests, migrations and surrounding implementation directly from the published GitHub commit. Confirm that the changes match the reported behaviour and identify any discrepancies.

## Review checkpoints

For small tasks:
Implementation → Tests → Local commit → Approved push → GitHub source review.

For large tasks:
Implementation checkpoint → Tests → Commit → Approved push → GitHub review → Continue.

Use a review branch when publishing work that is not yet approved for main. Never force-push main.

## Task boundaries

Do not automatically begin another task after publishing a completed task.

Stop and report any unexpected branch divergence, failing tests, unrelated changes or possible secrets.
