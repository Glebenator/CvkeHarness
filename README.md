# CvkeHarness

CvkeHarness is a provider-agnostic Go operations agent with two deliberate modes: a bounded `run` command for one task and an interactive `console` for ongoing operator work.

The runtime is phase-routed, approval-aware, and uses a target-aware operational memory model. It distinguishes the machine running the harness from the system being operated on, keeps operational knowledge behind an explicit review lifecycle, and injects only a compact retrieval brief for the exact active target.

> [!WARNING]
> CvkeHarness lets a language model propose and run shell commands on your machine and on SSH targets you point it at. Start with the default `reasonable` security profile (or `extra_strict`), review every approval prompt, and try it on disposable hosts first. The permissive profiles (`less_strict`, `minimal`, `yolo`) remove safeguards on purpose. See [security controls](docs/security-controls.md).

## What It Does

- Runs a local agent loop with a compact layered system prompt and tool access
- Separates one-shot execution from the stateful operations console
- Supports multiple providers behind a shared provider interface
- Tracks run history, tool outcomes, routing stats, approvals, and operational memory in SQLite
- Maintains human-readable managed memory files under `~/.cvkeharness/`
- Distinguishes the runtime host from remote SSH targets
- Retrieves target-specific playbooks, cautions, and findings with strict prompt budget caps
- Fails closed for operational-memory retrieval when the state DB is unavailable
- Offers typed file recovery, standalone NGINX transactions, guarded SSH port changes, native Btrfs checkpoints, and checked resource arithmetic; see the [recovery operating guide](docs/recovery.md) for supported scope

## Runtime Model

The `run` flow supports these phases:

1. `planning`: an optional model call when routing is enabled
2. `execution`: the model/tool loop using the configured execution role or a routed selection
3. `verification`: checks completion evidence for actionable tasks and can trigger a bounded repair
4. Memory curation: the normal runtime deterministically records structured candidates. The `memory_curation` model phase is used only by an LLM-based curator, not by the default structured memory manager.

The Chat workspace inside `console` uses the same runtime stack with a pinned `chat` phase selection and the same target-aware retrieval/call loop.

For each run, the harness:

1. Classifies the task into a coarse task class such as `inspection`, `debugging`, `shell_heavy`, or `policy_sensitive`
2. Chooses a model for the active phase from the configured default or the approved learned shortlist
3. Resolves the active target identity from the prompt and later from observed tool calls such as `ssh host ...`
4. Loads:
   - built-in runtime rules
   - compact compiled guidance from `guidance.md`
   - a tiny runtime-host summary from canonical state
   - at most one target summary
   - at most one primary playbook
   - at most one caution
   - at most one fallback finding when no strong playbook exists
5. Executes the model/tool loop
6. Records structured outcomes to SQLite
7. Curates target-aware candidates from verifier-backed outcomes, failures, and typed host probes

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

If no remote context is present, the target defaults to the runtime host. When SSH-style context is detected, the harness resolves or creates a provisional target record. Remote targets begin with `environment=unknown`; an operator must bind the environment and remote identity label before target-scoped memory can become active.

### Managed Files

These live under `~/.cvkeharness/`:

- `guidance.md`
  User-authored operating guidance, collaboration style, and durable runtime boundaries.
- `targets.md`
  Generated target inventory and fact view, including the runtime host.
- `playbooks.md`
  Generated target-specific procedure view with `Verify`, `Action`, and `Success Checks` sections.
- `findings.md`
  Generated findings and candidate view.
- `cautions.md`
  Generated target-specific caution and candidate view.


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

Every retrieval gate requires a live target binding, exact target and environment match, active status, operator or verified trust, unexpired evidence, and a valid integrity hash. Playbooks additionally require a meaningful success check. Candidate, rejected, revoked, expired, untrusted, wrong-scope, and tampered records are excluded from every injection path, including target summaries.

Every retrieved procedure is historical, verify-first context. Operational memory can shape a proposal, but it never creates policy, permission, or approval.

### Persistence Model

The memory content decision is narrow and deterministic.

The runtime writes memory through a controlled pipeline:

- target resolution creates a live runtime record or a provisional remote target
- typed low-risk probes can create host-fact candidates
- completion-verifier-backed operational sequences can create playbook candidates
- concrete failures or policy denials can create short-lived caution candidates
- narrow reusable notes can create finding candidates

`memory_record_finding` is intentionally narrow:

- it submits a concise, untrusted finding candidate
- the candidate remains outside prompt retrieval until explicit promotion
- it cannot create policy, permission, approval, playbooks, or cautions

For a deeper walkthrough, see the [memory model](docs/memory-model.md).

## Human-Readable Files vs Structured State

SQLite at the configured `state_db_path` is canonical for target inventory and operational knowledge. Operational memory fails closed when SQLite is unavailable. `guidance.md` remains user-authored prompt context; the other managed Markdown files are generated views and explicit validated-import material.

The state database in `~/.cvkeharness/state.db` stores:

- run history
- phase records
- tool outcomes
- per-model stats
- routing candidates
- model approvals
- scoped one-time action grants and quarantined legacy command approvals
- chat sessions, turns, and tool activity
- user-declared endpoint labels
- scheduled jobs, job runs, scheduler claims, and system crontab audit records
- blocked work and redacted telemetry
- recovery operations, batches, and supervision evidence
- `targets`
- `target_aliases`
- `host_facts`
- `playbooks`
- `findings`
- `cautions`
- `snapshots`

The runtime never silently reindexes generated Markdown into SQLite. Use `cvkeharness memory export` to regenerate views and `cvkeharness memory import` for an explicit validated replacement. The deprecated `memory reindex` command is only an alias for that explicit import boundary. On first upgrade, legacy Markdown records are quarantined as untrusted candidates rather than activated.

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

Choose `Sign in with ChatGPT`. CvkeHarness reuses the official `~/.codex/auth.json` login cache (or `auth.json` under `CODEX_HOME`, or a connection's explicit `auth_file`) and sends Codex model requests to the ChatGPT Codex backend, so usage follows your ChatGPT/Codex plan rather than a manually pasted OpenAI API key. If you previously used API-key mode in Codex CLI, run `codex logout` and then `codex login` to switch to subscription-backed access.

The setup wizard also reads the Codex `~/.codex/models_cache.json` model cache (or the cache under `CODEX_HOME`). The primary and judge model pickers use that account-scoped catalog and distinguish recent from older cached choices. These are cached models, not a live availability check. If the cache is missing or empty, enter an exact model ID or run Codex to refresh the cache. Existing configured models remain selectable even when absent from the catalog.

### Install

```bash
go install github.com/glebenator/cvkeharness@latest
```

This places `cvkeharness` in `$(go env GOPATH)/bin`. The examples below use `./cvkeharness` for a binary built from a clone; drop the `./` when using an installed binary.

### Build

```bash
git clone https://github.com/Glebenator/CvkeHarness.git
cd CvkeHarness
go build -o cvkeharness .
```

On Windows, use Go 1.26.2 or newer and build from PowerShell:

```powershell
go build -o cvkeharness.exe .
.\cvkeharness.exe --help
.\cvkeharness.exe setup
```

The Windows build includes the CLI/Console and checked arithmetic. The native
recovery executor currently requires Linux or macOS; Windows omits the
`recovery_manage` and `recovery_fleet` agent tools and returns an explicit
unsupported-platform error for executor commands. `recovery calculate` works
without an executor. Use a Linux build inside WSL2 to test recovery, subject to
each adapter's filesystem/service requirements. The shell tool currently uses
POSIX `sh`, so native Windows shell execution also requires `sh` on `PATH`; it
does not automatically translate commands into PowerShell.

The [platform build workflow](.github/workflows/platform-build.yml) builds and
runs focused platform checks on Windows, Linux and macOS, and cross-compiles
Windows ARM64. These checks do not replace the recovery Docker/VM fault labs.

### Initial setup

```bash
./cvkeharness setup
```

Fresh installations must complete setup before opening the console (including Chat and the `tui` alias), running tasks, or starting scheduled work. Missing, empty, incomplete, or unreadable configuration produces a setup-required error instead of creating runtime state. Existing usable configurations continue to work without a new completion flag. Help, Settings for configuration repair, model-independent recovery, and daemon stop/status/uninstall remain available. Codex login runs through the separate official Codex CLI.

The setup wizard configures:

- provider
- Codex CLI ChatGPT login, API key, or local base URL
- default model
- security profile (`extra_strict`, `reasonable`, `llm_advisor`, `llm_judge`, `less_strict`, `minimal`, or `yolo`)
- optional per-control overrides for filesystem, commands, system, network, remote actions, autonomy, credential access, approvals, and limits
- safety judge or advisor model, defaulting to the primary model with an independent connection/model available
- optional host scan and dependency planning (daemon installation is Linux-only)
- initial `guidance.md` profile
- optional runtime-host machine notes for stable local quirks
- optional Tavily-backed public web search tools
- bootstrap of the structured memory files

The basic path skips host probes, model-generated suggestions, and optional integrations. The review shows both models and offers external actions only when you selected them. Setup preserves existing recovery, routing, and security overrides; routing, token/iteration limits, and logging can be edited in Settings. Older non-OpenRouter configurations carrying the obsolete Grok judge default automatically use their primary model instead.

`setup` creates the managed memory surfaces up front in `~/.cvkeharness/`. If generated views are missing or drift from canonical state, normal initialization repairs them from SQLite. A populated legacy Markdown-only installation is migrated once into quarantined candidates.

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
  Open the same Settings workspace used by the console, including configuration repair
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
- `cvkeharness fuzztool`
  Run Go fuzz smoke checks for the shell parser and policy and write a report
- `cvkeharness recovery`
  Prepare, inspect, apply, and restore typed operations without a model; see the [recovery guide](docs/recovery.md)
- `cvkeharness jobs`, `cvkeharness daemon`, and `cvkeharness cron`
  Manage internal scheduled work, its daemon, and the current user's crontab; see the [scheduling guide](docs/scheduled-jobs-and-cron.md)

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
- `cvkeharness memory inbox`
  List candidate facts, playbooks, findings, and cautions with provenance and review metadata
- `cvkeharness memory endpoints`
  Show endpoint labels saved from direct user declarations in Chat
- `cvkeharness memory forget-endpoint "<name>"`
  Forget one saved endpoint label
- `cvkeharness memory promote|reject|revoke|delete <kind> <id>`
  Apply the one-way review lifecycle to one exact record
- `cvkeharness memory export [directory]`
  Generate Markdown views from canonical SQLite state
- `cvkeharness memory import [directory]`
  Validate Markdown and atomically replace canonical operational state
- `cvkeharness memory rollback <snapshot>`
  Restore a generated-view snapshot through the validated import boundary
- `cvkeharness memory target set-environment <target-id> <environment> <remote-identity>`
  Bind one provisional remote target to operator-confirmed labels
- `cvkeharness memory reindex`
  Deprecated alias for explicit `memory import`

### Model commands

- `cvkeharness models favorites`
  Show saved favorite models
- `cvkeharness models favorite <model-ref>`
  Save a favorite model without changing routing approvals
- `cvkeharness models unfavorite <model-ref>`
  Remove a model from favorites
- `cvkeharness models shortlist`
  Show favorite models, approved models, and learned routing candidates
- `cvkeharness models recent`
  Show recently used requested/actual model pairs across runs and chat
- `cvkeharness models aliases`
  Show requested models that resolved to different actual models
- `cvkeharness models approve <model-ref>`
  Approve a model for future routing
- `cvkeharness models stats`
  Show normalized model performance data

For a named connection, use `connection-id::provider/model-id`, such as
`review-api::openrouter/anthropic/claude-sonnet-4.6`. A bare provider-native model
ID uses the current Primary connection. The `provider/model-id` form also works
for legacy configurations or a saved connection whose ID equals the provider.

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

- `connections`
  Named provider access: provider type, endpoint, credential or login-file reference. Multiple connections can use the same provider.
- `models`
  Primary, safety judge, safety advisor, classifier, verifier, planning, execution and curation assignments. Each selects a connection/model or explicitly inherits another role. See the [model and Settings guide](docs/models-settings.md).
- `provider` (legacy model configuration)
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
- `memory_capture`
  Capture server-address declarations in Chat: `declarations` (default), `explicit_only`, or `off`. See [conversational server addresses](docs/memory-model.md#conversational-server-addresses).
- `state_db_path`
- `debug_prompt_dumps`
  When true, instrumented execution, planning, verification, curation, and safety-review calls write prompt dumps as Markdown and HTML. The task classifier and setup suggestions are not captured.
- `prompt_dump_dir`
  Directory for prompt dump artifacts, grouped by date and run. Each run folder includes an `index.html` master page linking the individual Markdown and HTML dumps. The index starts with estimated prompt tokens and is updated with actual prompt, completion, total, and cached token counts when providers return usage. Defaults to `~/.cvkeharness/prompt_dumps`.
- `prompt_dump_retention_days`
  Retention window for debug prompt dumps. Dumps are pruned automatically and secret-looking values are redacted before persistence.
- `routing_min_confidence`
- `security`
  Canonical security profile and per-setting overrides. `reasonable` is the default. See [security controls](docs/security-controls.md).
- `safety_mode`
  Deprecated compatibility input. It is migrated into `security` when the new section is absent.
- `safety_model`
- `max_tokens`
- `max_iterations`
- `log_level`
- `allowed_commands`
- `recovery`
  Operator-defined roots, impact budgets, service adapters, snapshot targets, and enrolled fleet transports; see the [recovery guide](docs/recovery.md).
- `web_search`
  Optional public web research tools. Disabled by default. Set `web_search.enabled: true`, keep `provider: tavily`, and provide `api_keys.tavily` or `TAVILY_API_KEY`. Defaults are `max_results: 5`, `search_depth: basic`, and `max_fetched_chars: 12000`; request/config caps are 10 results and 30000 fetched characters. `allowed_domains` and `blocked_domains` constrain public search/fetch targets.

### Routing behavior

- Every role uses its configured connection/model or explicit inheritance. With automatic routing disabled, configured role assignments are used directly.
- For legacy provider-scoped connections, enabled routing scores candidates from local history and checks routing approval before selecting another model.
- If confidence is too low, the runtime falls back to the default.
- If a strong unapproved candidate is found, the CLI asks for one-off approval.
- A prompt-approved model is recorded as `approved_once`; only deliberately durable model approvals are reused later.
- Named connections retain their configured assignments when historical statistics cannot distinguish their endpoint/account. Their results do not contaminate provider-only automatic routing statistics.

## Prompt Stack

For execution runs, the system prompt is layered in this order:

1. compiled guidance prefix from built-in rules plus `guidance.md`
2. stable per-turn tool policy and schemas
3. compact host-target-memory brief loaded from canonical SQLite state
4. volatile turn context, conversation history, and optional planning notes

Instrumented agent phase calls record a stable-prefix hash and full prompt hash,
plus cached-token usage when the provider reports it. This makes provider-side
cache behavior measurable without opening raw prompt dumps; unknown usage is
not evidence of a cache miss.

## Tooling Model

The default tool surface is intentionally small.

### `shell_execute`

The shell tool:

- validates shell syntax
- parses supported chaining operators like `&&`, `||`, `;`, and `|`
- classifies redirection and other shell effects, and rejects malformed or unsupported constructs
- evaluates every effect through the immutable effective security policy
- treats LLM review as advisory and routes `ask` decisions to human approval
- consumes only exact, expiring, single-use persisted grants or exact process-local session grants
- records telemetry and tool outcomes
- provides the main target discovery signal for remote SSH work

Choose **LLM advisor** (`llm_advisor`) in setup or **Settings → Security →
Security profile** to add plain-language advice to commands that require human
approval. Select its model and provider under **Settings → Models → Safety
advisor**, save, and start a new chat session (`/new`) to apply the change.

The advisor explains the full action, including command chains, pipelines,
redirections, and embedded scripts, and recommends **approve** or **reject**
with risks and uncertainty. It receives the command and policy context with
recognized credentials masked, has no tools, and never grants approval. The
profile uses Reasonable's policy defaults; allowed actions still run and denied
actions stay blocked. Failed, timed-out, or invalid advice leaves the human
approval gate in place. See [the advisor guide](docs/llm-advisor.md).

The **LLM judge** (`llm_judge`) preset allows known reads and sets most other
actions to `llm_review`, including file changes, scripts, network access, and
system or remote mutations. Credential and raw-device access remain denied.
Select the model under **Settings → Models → Safety judge**. A `SAFE` verdict
still requires human approval; rejected or failed reviews block execution.
See [preset details](docs/models-settings.md#llm-judge-preset).

### `memory_record_finding`

The memory note tool:

- submits a concise untrusted candidate to canonical operational state
- is meant for a narrow target-scoped observation that an operator can review
- should not be used for raw logs, speculative thoughts, or verbose summaries
- keeps ad hoc notes provisional rather than executable

### `memory_remember_target`

This tool saves an endpoint label declared in the current direct
user message. It obeys `memory_capture` and the memory-write policy, and cannot
infer declarations from tool output or model arguments. Saved labels do not
verify a live machine, promote operational knowledge, or authorize a connection.
See the [memory guide](docs/memory-model.md#conversational-server-addresses).

### Scheduling, recovery, and arithmetic

- `schedule_manage` manages internal agent jobs; `system_cron_manage` manages the current user's crontab with an additional exact-diff confirmation.
- `recovery_manage` and `recovery_fleet` expose configured typed recovery operations on supported platforms.
- `safety_calculate` checks exact arithmetic and units without changing a system.

Scheduling requires available SQLite state; recovery also requires an effective
security policy and a supported executor. Registered tools pass through policy
checks, and tool schemas are filtered for each task.

### `web_search` and `web_fetch`

The optional Tavily-backed web tools:

- are registered only when `web_search.enabled` is true and a Tavily key is available
- use direct Go HTTP calls to Tavily, not shell commands or external binaries
- return bounded structured JSON with URLs, snippets/content, request IDs, usage credits, and truncation flags
- reject likely secrets before making requests
- block `web_fetch` for localhost, private/link-local/metadata, bare internal, and configured blocked domains
- are intended for public documentation, release notes, issue trackers, and error-message research, not target discovery or internal network probing

## Documentation

- [Security controls](docs/security-controls.md): profiles, per-setting overrides, and approval behavior
- [Models and Settings](docs/models-settings.md): connections, model roles, and the Settings workspace
- [LLM advisor](docs/llm-advisor.md): plain-language advice on actions that need approval
- [Memory model](docs/memory-model.md): target-scoped operational memory and its review lifecycle
- [Recovery](docs/recovery.md): recoverable file, NGINX, SSH, and Btrfs operations and their limits
- [Scheduled jobs and cron](docs/scheduled-jobs-and-cron.md): internal jobs and user crontab management
- [Chat and activity](docs/chat-activity.md): how Chat and per-turn Activity fit together

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
  Structured target-aware memory, review lifecycle, fail-closed retrieval, validated import/export, migration, and rollback
- `state/`
  SQLite persistence for runs, stats, routing, approvals, structured operational memory, and snapshots

### Providers and tools

- `provider/`
  Provider interface plus Codex ChatGPT subscription, OpenRouter, OpenAI Responses API, and LM Studio implementations
- `tools/`
  Policy-gated registry, shell, memory, scheduling, web, recovery, and arithmetic tools
- `scheduler/` and `systemcron/`
  Internal job scheduling and current-user crontab management
- `recovery/`
  Typed file, service, snapshot, and fleet executors with persisted recovery evidence
- `securitypolicy/`
  Security profiles, control catalog, and immutable effective policy

### Safety and evaluation

- `safety/`
  Red-team harness and safety scorecard generation
- `docs/`
  Operator guides; `redteam`, `scorecard`, and the fuzz tool also write their reports here by default

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
  Generated-view parsing, rendering, drift repair, migration, snapshots, import/export, and rollback
- `memory/review.go`
  Candidate promotion, rejection, revocation, deletion, and target binding
- `memory/safety.go`
  Lifecycle defaults, expiry, provenance integrity, and sensitive-content filtering
- `state/store.go`
  The SQLite schema and persistence layer
- `state/store_operational_memory.go`
  Structured target-aware memory persistence
- `router/router.go`
  How candidate models are scored and selected
- `config/config.go`
  Config shape and defaults

## Development

### Run tests

```bash
go test ./...
```

In sandboxed environments where the default Go build cache is not writable, set `GOCACHE` to a writable directory, for example `GOCACHE=/tmp/cvke-go-build go test ./...`.

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

## Project Status

The harness now supports routed execution plus target-aware operational memory, but it is still intentionally compact:

- routing is heuristic, not fully autonomous
- the default tool surface is narrow
- operational memory is SQLite-canonical with local Markdown views
- retrieval is structured-first and bounded, not semantic-first
- SQLite carries the machine-structured indexing and operational history
- provider support is focused on a shared abstraction rather than provider-specific features

That keeps the system inspectable, testable, and easy to extend.

## Security

Please report vulnerabilities privately as described in [SECURITY.md](SECURITY.md), not in public issues.

## License

CvkeHarness is released under the [MIT License](LICENSE).
