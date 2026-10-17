# Documentation entry point

**Read this before changing WLEDger.** Last reconciled 2026-10-17.

| Source | Role | Authority |
| --- | --- | --- |
| [../AGENTS.md](../AGENTS.md) | Agent workflow, review, branch and hardware safety | Mandatory policy |
| [PRODUCT_SPEC.md](PRODUCT_SPEC.md) | Owner-approved future product requirements | **Authoritative target** |
| [OMO_HANDOVER.md](OMO_HANDOVER.md) | Current development status and verification evidence | Operational handover; verify commit |
| [../ROADMAP.md](../ROADMAP.md) | Older implementation history and evolving plans | Historical where conflicting |
| [roadmap.md](roadmap.md) | Older gap inventory and implementation notes | Historical where conflicting |
| [storage-and-scanning.md](storage-and-scanning.md) | Earlier hierarchy/scanner design and code map | Historical where conflicting |

## Interpretation rules

1. Never equate **agreed target** with **already implemented**. Validate code, migrations and tests on the exact branch/commit.
2. When a historical document contradicts `PRODUCT_SPEC.md`, follow the latter for future requirements; preserve old data and behavior until an explicitly approved migration.
3. No documents authorize modifying production, restoring databases, changing LED coordinate spaces, deploying, merging to `main` or sending physical WLED commands.
4. Retain historical documents while their unique recovery/migration details or links remain in use. Remove them only after content and references are safely consolidated.
5. Open decisions (not silently resolved): implementation phasing, initial supplier-ordering scope, AI optionality. Hardware topology must be verified against physical reality before enabling outputs.

## Documentation cleanup remaining

- Inventory all documentation, site pages, handovers and references; classify current, historical, duplicate or obsolete.
- Move unique migration and recovery information out of duplicate roadmaps before deletion.
- Check internal links and code/documentation references, then delete genuinely redundant files.
- Reconcile every implementation-status claim against the current development commit and tests.
- Refresh public README and website guides to distinguish current behavior from future plans.

This is an index and status guide, **not** a claim that the entire documentation tree has already been audited.
