# CvkeHarness Memory Model

This document explains the current target-aware operational memory system used by CvkeHarness.

It is intentionally implementation-oriented and complements:

- [README.md](../README.md)
- [architecture.md](architecture.md)
- [project-visual-guide.md](project-visual-guide.md)

## Design Goals

The current memory layer is optimized for reliable operational recall rather than open-ended conversational memory.

Its priorities are:

1. remember the right facts for the active target system
2. keep prompt usage bounded and predictable
3. store readable markdown as the operator-facing source of truth
4. use SQLite for indexing and retrieval, not for opaque hidden memory
5. prefer deterministic extraction and persistence over freeform prompt dumping

The practical consequence is simple: when retrieval is uncertain, the runtime retrieves less, not more.

## Identity Model

CvkeHarness explicitly distinguishes the machine running the harness from the system being operated on.

### Core ids

- `runtime_host_id`
  The host running CvkeHarness locally.
- `target_id`
  The host or container the agent is currently acting on.
- `target_kind`
  One of `runtime`, `ssh`, `local_container`, or `unknown`.

### Resolution behavior

- If no remote context is present, `target_id` defaults to the runtime host.
- If SSH-like context is detected in the prompt or in a `shell_execute` command, the harness resolves or creates a target record.
- Unknown remote targets become provisional records immediately.
- Later verified facts such as `hostname` output can merge aliases like `prod-app` and `web-01.internal` into the same stable target record.

## Managed Files

The memory directory is still human-readable first.

### `operator.md`

Purpose:

- stable runtime contract
- prompt boundaries
- managed file roles
- dependency/install behavior

Ownership:

- user-editable
- not machine-rewritten except for initial bootstrap

### `soul.md`

Purpose:

- persona and tone only

Ownership:

- fully user-owned
- never auto-edited after bootstrap

### `targets.md`

Purpose:

- target registry
- alias mapping
- concise verified target facts

Typical metadata:

- `target_id`
- `kind`
- `primary_name`
- `aliases`
- `hostnames`
- `ips`
- `transport`
- `first_seen_at`
- `last_seen_at`
- `confidence`
- `status`

### `host.md`

Purpose:

- concise verified runtime-host profile plus operator-authored machine notes

Typical facts:

- hostname
- OS / distro
- package manager
- service manager
- container runtime

Optional notes:

- stable local quirks or caveats
- nonstandard package or tool locations
- VPN, DNS, proxy, or service-manager behavior the harness should remember

### `playbooks.md`

Purpose:

- durable target-specific procedures

Typical metadata:

- `playbook_id`
- `target_id`
- `intent`
- `tool_name`
- `confidence`
- `success_count`
- `failure_count`
- `last_verified_at`
- `last_used_at`
- `match_terms`
- `preconditions`
- `status`

Body structure:

- `Verify`
- `Action`
- `Success Checks`
- optional `Notes`

### `findings.md`

Purpose:

- provisional observations awaiting promotion or future reuse

Typical metadata:

- `finding_id`
- `target_id`
- `intent`
- `tool_name`
- `confidence`
- `seen_count`
- `origin`
- `created_at`
- `updated_at`
- `status`

### `cautions.md`

Purpose:

- target-specific negative memory for unreliable or denied approaches

Typical metadata:

- `caution_id`
- `target_id`
- `intent`
- `tool_name`
- `confidence`
- `failure_count`
- `last_seen_at`
- `status`

## Legacy Import

The old `memory.md` file is treated as a legacy source.

Bootstrap behavior:

- if `memory.md` exists and the structured files do not, the harness imports legacy entries into `findings.md`
- imported records are tagged with:
  - `origin=legacy_memory`
  - `target_id=unknown`
  - `status=needs_curation`

Legacy entries are intentionally not auto-promoted into executable playbooks.

## Structured State In SQLite

The structured retrieval/indexing layer currently uses dedicated tables:

- `targets`
- `target_aliases`
- `host_facts`
- `playbooks`
- `findings`
- `cautions`
- `snapshots`

The broader runtime state database also retains:

- `runs`
- `phase_records`
- `tool_outcomes`
- `model_stats`
- `routing_candidates`
- `model_approvals`
- `command_approvals`

Reindexing always rebuilds the operational memory tables from the markdown files.

## Retrieval Model

Retrieval is compact by design.

### Always-loaded layers

Every run gets:

1. built-in runtime rules
2. `operator.md`
3. `soul.md`
4. a tiny runtime-host summary from `host.md`

### Retrieved brief

The operational memory brief may add:

1. one target summary
2. one primary playbook
3. one caution
4. one fallback finding when no strong playbook exists

The runtime never injects whole memory files into prompt context.

## Ranking Policy

Playbooks are ranked target-first:

1. exact `target_id + intent + tool`
2. exact `target_id + intent`
3. exact `target_id + tool`

Cautions and fallback findings are then considered for the same target.

### Freshness buckets

- `fresh`
  verified within 30 days
- `stale`
  31 to 90 days old
- `cold`
  older than 90 days

### Direct-use behavior

The runtime currently renders a playbook as direct-use eligible only when it is:

- fresh
- confidence `>= 0.82`
- verified successfully at least once
- not outweighed by repeated failure

Otherwise the playbook is still retrievable, but it renders as verify-first guidance.

## Deterministic Extraction

The current implementation avoids open-ended model-generated persistence wherever possible.

### Derived without an LLM

The runtime derives these fields directly:

- target ids
- alias mapping
- timestamps
- default confidence values
- freshness labels
- success and failure counters
- runtime-host bootstrap
- cheap verified facts from shell output

### Current fact extraction signals

Successful `shell_execute` calls can currently infer facts such as:

- `hostname`
- OS info from `os-release`
- `package_manager`
- `service_manager`
- `container_runtime`

### Why this matters

This keeps the memory layer inspectable and makes retrieval behavior reproducible under test.

## Write Pipeline

The runtime writes memory through a controlled persistence pipeline.

### Target discovery

- prompt hints can create or resolve a target
- observed `ssh`, `scp`, or `rsync` style shell commands can refine that target during execution

### Verified fact updates

- successful shell commands can enrich host facts
- verified facts are merged onto the resolved target profile

### Playbook creation and update

- successful operational sequences can create or update a target-specific playbook
- repeated successful reuse increases confidence
- repeated failure lowers confidence and can suppress direct-use behavior

### Caution creation and update

- concrete command failures or policy denials can create or update a caution
- repeated failures increase caution weight

### Findings

- the agent-facing `memory_record_finding` tool writes concise verified ad hoc findings
- findings are intentionally narrower and less executable than playbooks

## File Fallback

If SQLite is unavailable:

- managed markdown files still exist
- retrieval still works by parsing those files directly
- curation still rewrites markdown files
- snapshot/rollback still works for managed files

That keeps the memory system usable even when the database cannot be opened.

## Operational Mental Model

The shortest accurate summary of the current design is:

CvkeHarness remembers the runtime host, resolves the active target, retrieves one compact target-aware brief, and turns verified successes and failures into readable operational memory instead of loose generic snippets.
