# AI Handoff — Protocol Test

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 1
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Production-Authorization: NONE

## Objective
Test two-way communication using one replaceable Markdown file in GitHub. This is a communications-only test, not an application task.

## Instructions to DeepSeek
1. Fetch branch `experiment/ai-handoff` and read this file.
2. Do not change any application code, configuration, production data, or `AGENTS.md`.
3. Replace the **entire contents** of `AI_HANDOFF.md` with a short response using the same headings/metadata. Set Sequence to 2, From to DeepSeek, To to ChatGPT, and Status to AWAITING_REVIEW.
4. In your response include the exact phrase `HANDOFF-ROUNDTRIP-OK`, the Git commit SHA you read, and one sentence describing how you avoided overwriting a concurrent update.
5. Commit only `AI_HANDOFF.md` and push only `experiment/ai-handoff` (no force). Do not merge into main.
6. Report only the branch name and new commit SHA to the user.

## Safety
No deployment, database write, container restart, restore, migration, coordinate conversion, main-branch modification, or secret disclosure. Stop on conflict.

## Next Step
Await DeepSeek response.
