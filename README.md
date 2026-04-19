# CvkeHarness

CvkeHarness is a provider-agnostic Go CLI for running a tool-using LLM agent against local DevOps-style workflows.

The runtime is model-aware: it knows which provider/model is active, learns from previous runs, retrieves scoped memory for the current task, and can route different phases of a run to different approved models.

## What It Does

- Runs a local agent loop with a compact system prompt and tool access
- Supports multiple providers behind a shared provider interface
- Tracks execution history, tool outcomes, and model performance in SQLite
- Maintains human-readable memory files under `~/.cvkeharness/`
- Routes `planning`, `execution`, and `memory curation` phases independently
- Uses only approved models for automatic routing
- Falls back gracefully to file-backed memory when the state DB is unavailable

## Current Runtime Model

The runtime is split into three routed phases:

1. `planning`
2. `execution`
3. `memory_curation`

For each run, the harness:

1. Classifies the task into a coarse task class such as `inspection`, `debugging`, `shell_heavy`, or `policy_sensitive`
2. Chooses a model for the active phase from the configured default or the learned approved shortlist
3. Loads:
   - built-in runtime rules
   - `soul.md`
   - the top relevant learned snippets from memory/findings
4. Executes the model/tool loop
5. Records structured outcomes to SQLite
6. Writes short-lived lessons to `findings.md`
7. Promotes repeated durable lessons into `memory.md`

## Human-Managed vs Machine-Managed State

CvkeHarness keeps both readable files and structured machine state.

### Human-facing files

These live under `~/.cvkeharness/`:

- `soul.md`
  The primary persona and long-lived behavioral guidance. User-managed.
- `memory.md`
  Durable learned heuristics. Agent-managed.
- `findings.md`
  Recent short-lived lessons. Agent-managed.

### Machine-managed state

This lives in `~/.cvkeharness/state.db` and stores:

- run history
- phase records
- tool outcomes
- per-model stats
- routing candidates
- model approvals
- memory entry metadata
- snapshots for rollback

Markdown stays readable; SQLite is the source of truth for scoring, routing, and memory metadata.

## Quick Start

### Requirements

- Go `1.26.2` or newer
- One configured provider:
  - OpenRouter
  - LM Studio

### Build

```bash
go build -o cvkeharness .
```

### Initial setup

```bash
./cvkeharness setup
```

The setup wizard configures:

- provider
- API key or local base URL
- default model
- command approval mode
- safety model when using LLM judge mode
- routing mode
- token limit
- iteration limit
- log level
- initial `soul.md` profile

### Run a task

```bash
./cvkeharness run "inspect the current process list"
```

Show why routing chose a model:

```bash
./cvkeharness run --explain-routing "debug why journalctl output is empty"
```

## CLI Commands

### Core commands

- `cvkeharness setup`
  Interactive configuration wizard
- `cvkeharness settings`
  Interactive settings editor for updating an existing configuration
- `cvkeharness run [task]`
  Execute a task through the routed agent runtime
- `cvkeharness redteam`
  Run a live red-team evaluation harness
- `cvkeharness scorecard`
  Generate a deterministic safety scorecard

### Memory commands

- `cvkeharness memory show`
  Show `soul.md`, `memory.md`, `findings.md`, and snapshot summary
- `cvkeharness memory rollback <snapshot>`
  Restore a managed memory file from a snapshot and reindex state
- `cvkeharness memory reindex`
  Rebuild SQLite memory metadata from the markdown files

### Model commands

- `cvkeharness models shortlist`
  Show approved models and learned routing candidates
- `cvkeharness models approve <provider/model>`
  Approve a model for future routing
- `cvkeharness models stats`
  Show normalized model performance data

## Configuration

Config is stored in `~/.cvkeharness/config.yaml`.

Important fields:

- `provider`
- `base_url`
- `default_model`
- `planning_model`
- `execution_model`
- `curation_model`
- `routing_enabled`
- `routing_mode`
- `approved_models`
- `memory_dir`
- `state_db_path`
- `memory_max_snippets`
- `routing_min_confidence`
- `safety_mode`
- `safety_model`
- `max_tokens`
- `max_iterations`
- `allowed_commands`

### Routing behavior

- If routing is disabled, the default model is used everywhere.
- If routing is enabled, the router scores approved candidates from local history.
- If confidence is too low, the runtime falls back to the default.
- If a strong unapproved candidate is found, the CLI asks for one-off approval.
- Prompt-approved models are added to the learned approval pool and can be reused later.

## Memory Retrieval Model

Memory retrieval is scope-aware.

Entries can be:

- global
- model-specific
- tool-specific
- task-class-specific
- combined, such as `model + tool`

Retrieval ranks by:

- current phase
- current task class
- active toolset
- current model
- actual served model
- recent tool trouble or policy denial

When repeated tool failure or a policy denial occurs, the runtime is allowed one refresh of learned context during the run.

## Routing Heuristics

Routing is intentionally deterministic and local-first.

The router currently optimizes for:

1. successful completion
2. low policy-denied behavior
3. latency as a tie-breaker

It does not silently browse provider catalogs or auto-expand into arbitrary models. The candidate pool is limited to:

- approved models from config
- models approved interactively and stored in approval state

## Tooling Model

The current default tool surface is intentionally small.

### `shell_execute`

The shell tool:

- validates shell syntax
- blocks obvious breakout constructs
- checks an allowlist for automatically approved commands
- routes non-allowlisted commands through either an LLM judge or direct user confirmation, depending on config
- records telemetry and tool outcomes

This keeps the runtime provider-agnostic while still allowing policy-sensitive shell access behind a narrow gate.

## Repository Layout

### Runtime and orchestration

- `main.go`
  CLI entrypoint
- `cmd/`
  Cobra commands and interactive setup wizard
- `agent/`
  Routed runtime loop and execution orchestration
- `core/`
  Normalized runtime concepts such as phases, task classes, and model refs
- `router/`
  Deterministic per-phase model routing
- `memory/`
  Readable memory files, retrieval, curation, reindex, rollback
- `state/`
  SQLite persistence for runs, stats, routing, approvals, and snapshots

### Providers and tools

- `provider/`
  Provider interface plus OpenRouter and LM Studio implementations
- `tools/`
  Tool registry and shell tool

### Safety and evaluation

- `safety/`
  Red-team harness and safety scorecard generation
- `docs/`
  Generated reports and safety planning docs

## Key Code Paths

If you are reading the code for the first time, these are the best entry points:

- `cmd/run.go`
  How a runtime instance is assembled from config
- `agent/agent.go`
  The planning/execution/curation flow
- `router/router.go`
  How candidate models are scored and selected
- `memory/manager.go`
  How file-backed memory and DB metadata stay in sync
- `state/store.go`
  The SQLite schema and persistence layer
- `config/config.go`
  Config shape and defaults

## Development

### Run tests

```bash
GOCACHE=/tmp/cvke-go-build go test ./...
```

Using a local `GOCACHE` is helpful in sandboxed environments where the default Go build cache path is not writable.

### Format code

```bash
gofmt -w .
```

### Notes for contributors

- Keep the runtime provider-agnostic at the core.
- Store normalized facts and outcomes rather than provider-specific lore.
- Prefer deterministic local heuristics over hidden autonomy.
- Treat `soul.md` as user-owned and never auto-edit it.
- When changing memory behavior, keep file readability and DB consistency aligned.
- When changing routing behavior, preserve the approval boundary.

## Generated Artifacts

The repository already includes generated safety artifacts under `docs/`:

- `docs/redteam-report.md`
- `docs/redteam-report.json`
- `docs/safety-scorecard.md`
- `docs/safety-scorecard.json`
- `docs/safety-hardening-plan.md`

## Project Status

The harness now supports model-aware memory and routed execution with local learning, but it is still intentionally compact:

- routing is heuristic, not fully autonomous
- the default tool surface is narrow
- memory is markdown-first and local
- provider support is focused on a shared abstraction rather than provider-specific features

That keeps the system easy to inspect, test, and extend.
