# CvkeHarness

CvkeHarness is a provider-agnostic Go operations agent with two deliberate modes: a bounded `run` command for one task and an interactive `console` for ongoing operator work.

The runtime is phase-routed, approval-aware, and now uses a target-aware operational memory model. It distinguishes the machine running the harness from the system being operated on, remembers verified host facts and proven procedures for the active target, and injects only a compact retrieval brief into the prompt.

## What It Does

- Runs a local agent loop with a compact layered system prompt and tool access
- Separates one-shot execution from the stateful operations console
- Supports multiple providers behind a shared provider interface
- Tracks run history, tool outcomes, routing stats, approvals, and operational memory in SQLite
- Maintains human-readable managed memory files under `~/.cvkeharness/`
- Distinguishes the runtime host from remote SSH targets
- Retrieves target-specific playbooks, cautions, and findings with strict prompt budget caps
- Lets bounded runs fall back to file-backed memory when the state DB is unavailable

## Runtime Model

The main `run` flow is split into three routed phases:

1. `planning`
2. `execution`
3. `memory_curation`

The Chat workspace inside `console` uses the same runtime stack with a pinned `chat` phase selection and the same target-aware retrieval/call loop.

For each run, the harness:

1. Classifies the task into a coarse task class such as `inspection`, `debugging`, `shell_heavy`, or `policy_sensitive`
2. Chooses a model for the active phase from the configured default or the approved learned shortlist
3. Resolves the active target identity from the prompt and later from observed tool calls such as `ssh host ...`
4. Loads:
   - built-in runtime rules
   - compact compiled guidance from `guidance.md`
   - a tiny runtime-host summary from `targets.md`
   - at most one target summary
   - at most one primary playbook
   - at most one caution
   - at most one fallback finding when no strong playbook exists
5. Executes the model/tool loop
6. Records structured outcomes to SQLite
7. Curates durable target-aware memory from verified shell outcomes, failures, and host facts

## Target-Aware Memory

The current memory system is structured-first rather than semantic-first.

### Identity Model

CvkeHarness distinguishes:

- `runtime_host_id`
  The machine running CvkeHarness
- `target_id`
  The system the agent is acting on
- `target_kind`
  `runtime`, `ssh`, `local_container`, or `unknown`

If no remote context is present, the target defaults to the runtime host. When SSH-style context is detected, the harness resolves or creates a stable target record and keeps aliases, hostnames, IPs, and `user@host` forms attached to the same `target_id`.

### Managed Files

These live under `~/.cvkeharness/`:

- `guidance.md`
  User-authored operating guidance, collaboration style, and durable runtime boundaries.
- `targets.md`
  Target registry plus alias mapping and concise target facts, including the runtime host.
- `playbooks.md`
  Durable target-specific procedures with `Verify`, `Action`, and `Success Checks` sections.
- `findings.md`
  Manual or ad hoc observations only; assistant final-output summaries are never added automatically.
- `cautions.md`
  Target-specific negative memory for bad or unreliable approaches.


### Retrieval Policy

The runtime never injects whole memory files into the prompt.

Structured retrieval always loads:

1. built-in runtime rules
2. compiled `guidance.md`
3. one small runtime-host summary

Structured retrieval may additionally load:

1. one target summary
2. one primary playbook
3. one caution
4. one fallback finding when no strong playbook exists

Selection priority is target-first:

1. exact `target_id + intent + tool`
2. exact `target_id + intent`
3. exact `target_id + tool`
4. exact `target_id` caution
5. runtime-host guidance only when target-specific memory is absent

Freshness buckets:

- `fresh`: verified within 30 days
- `stale`: 31 to 90 days
- `cold`: older than 90 days

Direct-use behavior:

- a fresh, high-confidence playbook with at least one successful verified use can be rendered as direct-use eligible
- stale or cold playbooks are still retrievable, but they render as verify-first guidance
- repeated failures lower confidence and increase caution weight

### Persistence Model

The memory content decision is narrow and deterministic.

The runtime writes memory through a controlled pipeline:

- target resolution creates or updates target records
- successful shell outcomes can enrich verified host facts
- successful operational sequences can create or update playbooks
- concrete failures or policy denials can create or update cautions
- narrow reusable notes can be written into `findings.md`

`memory_record_finding` is intentionally narrow:

- it writes a concise verified ad hoc note into `findings.md`
- it is for reusable operator notes, preferences, or heuristics
- it does not directly create playbooks or cautions

For a deeper walkthrough, see [docs/memory-model.md](docs/memory-model.md).

## Human-Readable Files vs Structured State

Markdown remains the operator-facing source of truth. SQLite is the retrieval and indexing layer.

The state database in `~/.cvkeharness/state.db` stores:

- run history
- phase records
- tool outcomes
- per-model stats
- routing candidates
- model approvals
- scoped one-time action grants and quarantined legacy command approvals
- `targets`
- `target_aliases`
- `host_facts`
- `playbooks`
- `findings`
- `cautions`
- `snapshots`

The runtime reindexes from the managed markdown files back into SQLite with `cvkeharness memory reindex`.

## Quick Start

### Requirements

- Go `1.26.2` or newer
- One configured provider:
  - Codex via ChatGPT subscription
  - OpenRouter
  - OpenAI API
  - LM Studio

For ChatGPT subscription-backed Codex access, install the official Codex CLI and sign in first:

```bash
codex login
```

Choose `Sign in with ChatGPT`. CvkeHarness reuses the official `~/.codex/auth.json` login cache and sends Codex model requests to the ChatGPT Codex backend, so usage follows your ChatGPT/Codex plan rather than a manually pasted OpenAI API key. If you previously used API-key mode in Codex CLI, run `codex logout` and then `codex login` to switch to subscription-backed access.

The setup wizard also reads the official Codex `~/.codex/models_cache.json` model cache. When that cache was refreshed recently, the model picker and LLM judge picker show a `LIVE` status and list the same current Codex models exposed to your signed-in Codex account. If the cache is missing, stale, or empty, CvkeHarness does not guess at Codex model names; it offers only manual entry until Codex refreshes the account-scoped cache.

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
- Codex CLI ChatGPT login, API key, or local base URL
- default model
- security profile (`extra_strict`, `reasonable`, `less_strict`, `minimal`, or `yolo`)
- optional per-control overrides for filesystem, commands, system, network, remote actions, autonomy, approvals, and limits
- advisory model for controls explicitly set to `llm_review`
- routing mode
- token limit
- iteration limit
- log level
- initial `guidance.md` profile
- optional runtime-host machine notes for stable local quirks
- optional Tavily-backed public web search tools
- bootstrap of the structured memory files

`setup` creates the readable managed memory files up front in `~/.cvkeharness/`. If they are missing later, `run`, the console Chat workspace, and `memory reindex` bootstrap them again automatically before retrieval.

### Run a task

```bash
./cvkeharness run "inspect the current process list"
```

Show why routing chose a model:

```bash
./cvkeharness run --explain-routing "debug why journalctl output is empty"
```

### Open the operations console

```bash
./cvkeharness console
```

Open directly on the Chat workspace:

```bash
./cvkeharness console --view chat
```

## CLI Commands

### Core commands

- `cvkeharness setup`
  Interactive configuration wizard
- `cvkeharness settings`
  Interactive settings editor for updating an existing configuration
- `cvkeharness run [task]`
  Run one bounded task through the routed agent runtime, then exit
- `cvkeharness console`
  Open the interactive workspace for Chat, approvals, tool activity, verification, history, jobs, runs, and settings
  - `--view overview|jobs|runs|chat|settings` selects the initial workspace
  - `cvkeharness tui` remains a compatibility alias during the command transition
- `cvkeharness redteam`
  Run a live red-team evaluation harness
- `cvkeharness scorecard`
  Generate a deterministic safety scorecard

### Chat slash commands

Slash commands are handled locally and are never sent to the model. In the operations console, type `/` at the start of the Chat composer to open the filtered command palette; use the arrow keys to select and Enter to complete or run a command. Prefix a prompt with `//` to send a literal leading slash.

- `/new` (`/clear` remains an alias)
  Close the current logical session and start a fresh conversation
- `/memory`
  Show bounded previews of the memory sections used by the latest model call
- `/export`
  Write the persisted conversation to a private, redacted Markdown file under `~/.cvkeharness/exports/` by default
- `/tools`
  List registered capabilities and the current safety mode without implying authorization
- `/history`
  Browse saved conversations in the operations console
- `/help`
  Show commands available in the Chat workspace

Exports use private file permissions and mask obvious credential patterns. They may still contain private operational context, so review them before sharing.

### Memory commands

- `cvkeharness memory show`
  Show `guidance.md`, `targets.md`, `playbooks.md`, `findings.md`, `cautions.md`, and snapshot summary
- `cvkeharness memory rollback <snapshot>`
  Restore a managed memory file from a snapshot and reindex state
- `cvkeharness memory reindex`
  Rebuild structured target-aware memory metadata from the markdown files

### Model commands

- `cvkeharness models favorites`
  Show saved favorite models
- `cvkeharness models favorite <provider/model>`
  Save a favorite model without changing routing approvals
- `cvkeharness models unfavorite <provider/model>`
  Remove a model from favorites
- `cvkeharness models shortlist`
  Show favorite models, approved models, and learned routing candidates
- `cvkeharness models recent`
  Show recently used requested/actual model pairs across runs and chat
- `cvkeharness models aliases`
  Show requested models that resolved to different actual models
- `cvkeharness models approve <provider/model>`
  Approve a model for future routing
- `cvkeharness models stats`
  Show normalized model performance data

### Command commands

- `cvkeharness commands list`
  Show the static shell allowlist, scoped grants, and quarantined legacy approvals
- `cvkeharness commands approve "<command>"`
  Approve one exact shell action once for 15 minutes under the current policy/host/principal/directory
- `cvkeharness commands approve-work <blocked-work-id>`
  Approve the exact shell or non-shell action captured by blocked work once; the original executor scope remains binding

## Configuration

Config is stored in `~/.cvkeharness/config.yaml`.

Important fields:

- `provider`
  Use `codex` for ChatGPT subscription-backed Codex access, `openai` for usage-based OpenAI API access, `openrouter` for OpenRouter, or `lmstudio` for a local server.
- `api_keys`
  Stores API keys for usage-based providers; subscription-backed `codex` reads the official Codex CLI auth cache instead.
- `base_url`
- `default_model`
- `planning_model`
- `execution_model`
- `curation_model`
- `routing_enabled`
- `routing_mode`
- `approved_models`
- `favorite_models`
- `memory_dir`
- `state_db_path`
- `debug_prompt_dumps`
  When true, every model call writes a full prompt dump as Markdown and HTML for debugging.
- `prompt_dump_dir`
  Directory for prompt dump artifacts, grouped by date and run. Each run folder includes an `index.html` master page linking the individual Markdown and HTML dumps. The index starts with estimated prompt tokens and is updated with actual prompt, completion, total, and cached token counts when providers return usage. Defaults to `~/.cvkeharness/prompt_dumps`.
- `prompt_dump_retention_days`
  Retention window for debug prompt dumps. Dumps are pruned automatically and secret-looking values are redacted before persistence.
- `routing_min_confidence`
- `security`
  Canonical security profile and per-setting overrides. `reasonable` is the default. See the [detailed HTML security guide](docs/security-controls.html) or the [concise Markdown reference](docs/security-controls.md).
- `safety_mode`
  Deprecated compatibility input. It is migrated into `security` when the new section is absent.
- `safety_model`
- `max_tokens`
- `max_iterations`
- `allowed_commands`
- `web_search`
  Optional public web research tools. Disabled by default. Set `web_search.enabled: true`, keep `provider: tavily`, and provide `api_keys.tavily` or `TAVILY_API_KEY`. Defaults are `max_results: 5`, `search_depth: basic`, and `max_fetched_chars: 12000`; request/config caps are 10 results and 30000 fetched characters. `allowed_domains` and `blocked_domains` constrain public search/fetch targets.

### Routing behavior

- If routing is disabled, the default model is used everywhere.
- If routing is enabled, the router scores approved candidates from local history.
- If confidence is too low, the runtime falls back to the default.
- If a strong unapproved candidate is found, the CLI asks for one-off approval.
- Prompt-approved models are added to the learned approval pool and can be reused later.

## Prompt Stack

For execution runs, the system prompt is layered in this order:

1. compiled guidance prefix from built-in rules plus `guidance.md`
2. stable per-turn tool policy and schemas
3. compact host-target-memory brief from `targets.md`, `playbooks.md`, `cautions.md`, and `findings.md`
4. volatile turn context, conversation history, and optional planning notes

Each model call records a stable-prefix hash, full prompt hash, cached-token count, and cache-hit ratio so provider-side cache behavior is measurable without opening raw prompt dumps.

## Tooling Model

The default tool surface is intentionally small.

### `shell_execute`

The shell tool:

- validates shell syntax
- parses supported chaining operators like `&&`, `||`, `;`, and `|`
- blocks unsupported shell constructs such as redirection, substitution, and backgrounding
- checks both the static allowlist and the learned approved-command list
- routes unknown commands through either an LLM judge or direct user confirmation, depending on config
- persists approved command segments for reuse in future runs
- records telemetry and tool outcomes
- provides the main target discovery signal for remote SSH work

### `memory_record_finding`

The memory note tool:

- writes a concise verified note into `findings.md`
- is meant for reusable operator notes, environment facts, stable preferences, or tool heuristics discovered mid-run
- should not be used for raw logs, speculative thoughts, or verbose summaries
- keeps ad hoc notes provisional rather than executable

### `web_search` and `web_fetch`

The optional Tavily-backed web tools:

- are registered only when `web_search.enabled` is true and a Tavily key is available
- use direct Go HTTP calls to Tavily, not shell commands or external binaries
- return bounded structured JSON with URLs, snippets/content, request IDs, usage credits, and truncation flags
- reject likely secrets before making requests
- block `web_fetch` for localhost, private/link-local/metadata, bare internal, and configured blocked domains
- are intended for public documentation, release notes, issue trackers, and error-message research, not target discovery or internal network probing

## Repository Layout

### Runtime and orchestration

- `main.go`
  CLI entrypoint
- `cmd/`
  Cobra commands and interactive setup wizard
- `agent/`
  Routed runtime loop and execution orchestration
- `core/`
  Normalized runtime concepts such as phases, task classes, model refs, and retrieval context
- `router/`
  Deterministic per-phase model routing
- `memory/`
  Structured target-aware memory, retrieval, curation, reindex, and rollback
- `state/`
  SQLite persistence for runs, stats, routing, approvals, structured operational memory, and snapshots

### Providers and tools

- `provider/`
  Provider interface plus Codex ChatGPT subscription, OpenRouter, OpenAI Responses API, and LM Studio implementations
- `tools/`
  Tool registry, shell tool, and memory note tool

### Safety and evaluation

- `safety/`
  Red-team harness and safety scorecard generation
- `docs/`
  Architecture docs, visual guides, and generated reports

## Key Code Paths

If you are reading the code for the first time, these are the best entry points:

- `cmd/run.go`
  How a runtime instance is assembled from config
- `agent/agent.go`
  The planning/execution/curation flow
- `agent/chat_session_runtime.go`
  The interactive chat flow with the same runtime model
- `memory/manager_retrieval.go`
  Target resolution and compact retrieval brief rendering
- `memory/manager_persist.go`
  Deterministic curation of playbooks, findings, cautions, and host facts
- `memory/manager_files.go`
  Managed file parsing, rendering, snapshots, and rollback
- `state/store.go`
  The SQLite schema and persistence layer
- `state/store_operational_memory.go`
  Structured target-aware memory persistence
- `router/router.go`
  How candidate models are scored and selected
- `config/config.go`
  Config shape and defaults

For a deeper walkthrough of the current runtime, see:

- [docs/memory-model.md](docs/memory-model.md)
- [docs/project-visual-guide.md](docs/project-visual-guide.md)
- [docs/architecture.md](docs/architecture.md)

## Development

### Run tests

```bash
GOCACHE=/tmp/cvke-go-build go test ./...
```

Using a local `GOCACHE` is helpful in sandboxed environments where the default Go build cache path is not writable.

### Run end-to-end user journeys

```bash
./scripts/test-e2e.sh
```

The tagged end-to-end suite builds the real executable, drives setup through a
pseudo-terminal at representative widths, exercises local chat commands and an
allowlisted tool-backed conversation against a local mock model, and verifies
SQLite/export artifacts in an isolated temporary home. See
[e2e/README.md](e2e/README.md) for the journey matrix and safety boundaries.

### Format code

```bash
gofmt -w .
```

### Notes for contributors

- Keep the runtime provider-agnostic at the core.
- Store normalized operational facts rather than provider-specific lore.
- Prefer deterministic local heuristics over hidden autonomy.
- Treat `guidance.md` as user-owned and never auto-edit it.
- When changing memory behavior, keep file readability and DB consistency aligned.
- When changing routing behavior, preserve the approval boundary.
- When retrieval is uncertain, prefer retrieving less, not more.

## Generated Artifacts

The repository already includes generated safety artifacts under `docs/`:

- `docs/redteam-report.md`
- `docs/redteam-report.json`
- `docs/safety-scorecard.md`
- `docs/safety-scorecard.json`
- `docs/safety-hardening-plan.md`

## Project Status

The harness now supports routed execution plus target-aware operational memory, but it is still intentionally compact:

- routing is heuristic, not fully autonomous
- the default tool surface is narrow
- memory is markdown-first and local
- retrieval is structured-first and bounded, not semantic-first
- SQLite carries the machine-structured indexing and operational history
- provider support is focused on a shared abstraction rather than provider-specific features

That keeps the system inspectable, testable, and easy to extend.
