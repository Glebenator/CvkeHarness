# CvkeHarness Memory Model

CvkeHarness keeps operational memory readable, conservative, and target-aware. Markdown remains the authoritative surface; SQLite is a derived index and inspection layer, not a second source of truth.

## Design goals

1. Keep user-authored guidance separate from machine-curated operational facts.
2. Resolve targets conservatively so filler prose never becomes infrastructure inventory.
3. Retrieve less when uncertain: one target summary, one primary playbook, one caution, and at most one fallback finding.
4. Curate only concrete verified facts, successful playbooks, and cautions from failures or policy denials.
5. Keep `findings.md` manual or ad hoc only.

## Managed files

The memory directory contains:

- `guidance.md` — user-authored operating guidance and collaboration style.
- `targets.md` — the target registry, aliases, verified target facts, and the runtime host as a normal `kind=runtime` target.
- `playbooks.md` — durable target-specific procedures with verify/action/success-check structure.
- `cautions.md` — concrete negative memory from failures or policy denials.
- `findings.md` — manual or ad hoc observations only.

There is no separate runtime-host file. The machine running CvkeHarness is represented in `targets.md` like every other target.

## Target identity

CvkeHarness distinguishes:

- `runtime_host_id` — the local machine running the harness.
- `target_id` — the system the agent is acting on.
- `target_kind` — one of `runtime`, `ssh`, `local_container`, or `unknown`.

Command parsing is allowed to resolve ordinary command targets. Natural-language prose is intentionally conservative: only strong signals such as `user@host`, IP addresses, or explicit dotted hostnames create or resolve a non-runtime target. Phrases such as “ssh into the container” never mint targets from filler words like `into` or `from`.

## Retrieval

Each prompt receives:

1. built-in runtime rules,
2. compiled guidance from `guidance.md`,
3. one compact host-target-memory brief.

The compact brief may contain:

- one runtime-host summary,
- one active-target summary,
- one primary playbook,
- one caution,
- one fallback finding only when no strong playbook is available.

Retrieval is target-first and intentionally bounded. The runtime never injects whole managed files.

## Curation

Automatic curation may write only:

- verified host or target facts,
- successful playbooks,
- concrete cautions from failures or policy denials.

Automatic curation never turns assistant final output into generic findings. If a user or operator wants to preserve a one-off observation, they do it explicitly through `findings.md` or the ad hoc memory tool.

## Prompt interaction

`guidance.md` is compiled into a compact runtime form before prompt assembly. The prompt planner then lays out each request as:

1. compiled guidance prefix,
2. stable tool policy and schemas,
3. compact host-target-memory brief,
4. volatile turn context and conversation history.

This keeps the cacheable prefix stable while preserving a small retrieval surface.

## Persistence contract

Markdown is authoritative. SQLite stores parsed facts, aliases, playbooks, cautions, telemetry projections, and query-ready summaries rebuilt from the durable surfaces and event stream. If a representation disagrees, the readable markdown plus canonical telemetry stream are the durable record to rebuild from.
