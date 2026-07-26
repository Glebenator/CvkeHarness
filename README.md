# CvkeHarness

CvkeHarness is a provider-agnostic Go CLI for running a tool-using LLM agent against local DevOps-style workflows.

The runtime is phase-routed, approval-aware, and uses a safety-first target-scoped memory model. It distinguishes the machine running the harness from the system being operated on, stages learned procedures for operator review, and injects only compact, active, unexpired hints into the prompt.

## What It Does

- Runs a local agent loop with a compact layered system prompt and tool access
- Supports multiple providers behind a shared provider interface
- Tracks run history, tool outcomes, routing stats, approvals, and operational memory in SQLite
- Generates human-readable memory export views under `~/.cvkeharness/`
- Distinguishes runtime host, stable target ID, environment, transport, and remote identity
- Retrieves target-specific playbooks, cautions, and findings with strict prompt budget caps
- Fails closed for operational memory when the canonical state DB is unavailable

## Runtime Model

The main `run` flow is split into three routed phases:

1. `planning`
2. `execution`
3. `memory_curation`

Interactive chat uses the same runtime stack with a pinned `chat` phase selection and the same target-aware retrieval/call loop.

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
7. Stages target-aware memory candidates, including bounded typed facts from explicit probes

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
- `environment`
  An operator-bound scope such as `production` or `staging`
- `remote_identity`
  A transport-specific identity such as `ops@api-01`

If no remote context is present, the target defaults to the runtime host. SSH-style context creates a provisional target with `environment=unknown`. Operational memory and reusable approvals remain withheld until the operator binds its environment and remote identity. If one host identity matches more than one environment, resolution fails closed as ambiguous.

### Managed Files

These views live under `~/.cvkeharness/`:

- `guidance.md`
  User-authored operating guidance. It is prompt context, not managed policy.
- `targets.md`
  Generated target registry, scope, identity, and fact view.
- `playbooks.md`
  Generated candidate and active procedures with `Verify`, `Action`, and `Success Checks` sections.
- `findings.md`
  Generated candidate and active finding view.
- `cautions.md`
  Generated candidate and active caution view.


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

Selection is target-first and gated at read time:

1. exact live target and environment
2. active status and operator or verified trust
3. unexpired target and memory item
4. valid evidence integrity hash
5. exact intent and tool match
6. explicit success check for playbooks

Candidates, rejected items, revoked items, expired items, wrong-environment items, and tampered items are withheld.

Every playbook is rendered as a historical, verify-first hint. Memory never authorizes a command.

### Candidate lifecycle

Model-authored findings, successful operational sequences, and failed-output cautions enter a review inbox:

```text
candidate -> operator review -> active -> expired or revoked
                    \-> rejected
```

Use `cvkeharness memory inbox` and the `promote`, `reject`, `revoke`, or `delete` subcommands to manage the lifecycle.

### Persistence Model

SQLite is canonical for live fleet inventory and operational knowledge. Markdown is a generated export and explicit validated import surface. Normal runtime loading never trusts edited Markdown over SQLite.

`memory_record_finding` is intentionally narrow:

- it submits an untrusted candidate for operator review
- it cannot create active memory, policy, permissions, credentials, host mappings, or command approvals
- its candidate is not retrieved before promotion

For a deeper walkthrough, see [docs/memory-model.md](docs/memory-model.md) and the standalone [memory guide](docs/memory-guide.html).

## Human-Readable Files vs Structured State

SQLite is the source of truth. Markdown files are inspectable generated views and an explicit validated import format.

The state database in `~/.cvkeharness/state.db` stores:

- run history
- phase records
- tool outcomes
- per-model stats
- routing candidates
- model approvals
- target-scoped, expiring command approvals
- `targets`
- `target_aliases`
- `host_facts`
- `playbooks`
- `findings`
- `cautions`
- `snapshots`

Use `cvkeharness memory export [directory]` to generate views and `cvkeharness memory import [directory]` to validate and apply deliberate edits.

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
- command approval mode
- safety model when using LLM judge mode
- routing mode
- token limit
- iteration limit
- log level
- initial `guidance.md` profile
- optional runtime-host machine notes for stable local quirks
- optional Tavily-backed public web search tools
- bootstrap of generated, readable memory views backed by SQLite

`setup` creates SQLite-backed operational memory plus readable generated views in `~/.cvkeharness/`. Later `run` and `chat` commands regenerate missing views from SQLite before retrieval. Manual Markdown edits have no effect until an operator runs the explicit, validated `memory import` workflow.

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
- `cvkeharness chat`
  Start an interactive chat session with the same target-aware runtime
- `cvkeharness redteam`
  Run a live red-team evaluation harness
- `cvkeharness scorecard`
  Generate a deterministic safety scorecard

### Memory commands

- `cvkeharness memory show`
  Show `guidance.md`, `targets.md`, `playbooks.md`, `findings.md`, `cautions.md`, and snapshot summary
- `cvkeharness memory inbox`
  List untrusted candidates waiting for operator review
- `cvkeharness memory promote|reject|revoke|delete <kind> <id>`
  Manage the reviewed lifecycle of a fact, playbook, finding, or caution
- `cvkeharness memory target set-environment <target-id> <environment> <remote-identity>`
  Bind a provisional target to its operator-confirmed scope
- `cvkeharness memory export [directory]`
  Generate Markdown views from canonical SQLite state
- `cvkeharness memory import [directory]`
  Validate Markdown views and atomically replace canonical operational state

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
  Show the static shell allowlist plus learned approved commands
- `cvkeharness commands approve "<command>" --target <id> --environment <env> --ttl 1h`
  Create a short-lived exact command approval for one target and environment

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
- `memory_max_snippets`
  Retained for compatibility; structured retrieval now uses fixed brief caps in code
- `routing_min_confidence`
- `safety_mode`
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
  Structured target-aware memory, candidate review, validated import/export, retrieval, and curation
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
  Canonical SQLite persistence plus generated Markdown views and validated import/export
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
- [docs/memory-guide.html](docs/memory-guide.html), a standalone operator field guide
- [docs/project-visual-guide.md](docs/project-visual-guide.md)
- [docs/architecture.md](docs/architecture.md)

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
