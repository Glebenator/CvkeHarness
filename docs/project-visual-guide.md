# CvkeHarness Visual Guide

This guide is a companion to `docs/architecture.md`.

It focuses on the parts of the project that matter most when you are trying to rebuild the mental model quickly:

- context management
- the agent loop
- model routing
- memory
- safety and evaluation

## 1. System At A Glance

```mermaid
flowchart TD
    user["User"]
    cli["CLI surface\ncmd/run.go, cmd/chat.go,\ncmd/memory.go, cmd/models.go,\ncmd/commands.go, cmd/scorecard.go,\ncmd/redteam.go"]
    cfg["config.Config\n~/.cvkeharness/config.yaml"]
    agent["agent.Agent"]
    router["router.Router"]
    tools["tools.Registry\nshell_execute\nmemory_record_finding"]
    memory["memory.Manager"]
    store["state.Store\n~/.cvkeharness/state.db"]
    provider["provider.Provider\nOpenRouter or LM Studio"]
    files["Readable memory files\noperator.md\nsoul.md\nmemory.md\nfindings.md"]
    safety["safety package\nscorecard + redteam"]
    telemetry["telemetry.jsonl\nstreamed runtime events"]

    user --> cli
    cli --> cfg
    cli --> agent
    cli --> router
    cli --> tools
    cli --> memory
    cli --> store
    cli --> provider
    cli --> safety

    agent --> router
    agent --> tools
    agent --> memory
    agent --> store
    agent --> provider

    memory --> files
    memory --> store
    tools --> store
    tools --> telemetry
    agent --> telemetry
```

Key idea: the CLI builds one runtime from config, provider, router, tool registry, memory manager, and SQLite state, then hands control to `agent.Agent`.

The runtime is intentionally split into two worlds:

- readable files for durable human understanding
- SQLite for scoring, approvals, indexing, chat history, and routing statistics

## 2. Context Management

The project does not build one giant prompt. It assembles context in layers and keeps the learned part small.

```mermaid
flowchart TD
    task["User task or chat turn"]
    classify["core.ClassifyTask\ninspection, debugging,\nshell_heavy, policy_sensitive,\nlong_horizon, summarization, general"]
    retrieval["core.RetrievalContext\nphase + task_class + active_model\n+ tool_names + actual_model + trouble"]
    ensure["memory.Manager.EnsureFiles()"]
    files["Read operator.md and soul.md"]
    lookup["lookupEntries()\nprefer SQLite metadata\nfallback to parsing markdown files"]
    score["scoreEntries()\nphase\n+ task class\n+ provider/model\n+ tool match\n+ trouble hints\n+ lexical overlap"]
    learned["Top N snippets\nformatLearnedContext()"]
    stack["initialSystemMessages()\n1. built-in rules\n2. operator.md\n3. soul.md\n4. learned snippets\n5. planning notes"]
    chat["agent.ChatState"]
    refresh["One refresh after tool trouble\nor policy denial"]

    task --> classify --> retrieval
    retrieval --> ensure --> files
    retrieval --> lookup --> score --> learned
    files --> stack
    learned --> stack --> chat
    chat --> refresh
    refresh --> retrieval
```

What is important here:

- `builtInRules()` is fixed runtime policy and keeps the baseline behavior stable.
- `operator.md` is the harness operating manual. It explains file roles, dependency handling, approval boundaries, and when the agent may write a finding.
- `soul.md` is the human-facing persona layer and is intentionally user-owned.
- learned context comes from `memory.md` and `findings.md`, but only the best-scoring snippets are injected.
- retrieval can use either the requested model or the actual served model, which matters when a provider alias resolves to a different concrete model.
- during execution or chat, the runtime allows one mid-run refresh if a tool is denied or the same tool keeps failing.

## 3. Agent Loop

`agent.Agent` is the center of gravity for the project.

```mermaid
flowchart TD
    start["Run(task)"]
    classify["Classify task"]
    planning{"Routing enabled?"}
    planPhase["Planning phase\nsingle model call\nno tools\nreturns concise notes"]
    execSelect["Select execution model"]
    retrieve["Retrieve execution context"]
    build["Build system prompt stack\n+ user task"]
    loop["Execution loop\nup to MaxIterations"]
    model["Provider.ChatCompletion()"]
    done{"Tool calls?"}
    finish["Return assistant output"]
    toolExec["Execute requested tools"]
    trouble{"Denied or repeated failure?"}
    refresh["Refresh learned context once"]
    curation{"Memory curator present?"}
    curate["Curation phase\nextract reusable lessons as JSON"]
    persist["Persist lessons\nfindings.md and maybe memory.md"]
    record["Record run, phase stats,\nand tool outcomes"]

    start --> classify --> planning
    planning -- "yes" --> planPhase --> execSelect
    planning -- "no" --> execSelect
    execSelect --> retrieve --> build --> loop --> model --> done
    done -- "no" --> finish --> curation
    done -- "yes" --> toolExec --> trouble
    trouble -- "yes" --> refresh --> loop
    trouble -- "no" --> loop
    curation -- "yes" --> curate --> persist --> record
    curation -- "no" --> record
```

A few implementation details are easy to miss but shape the runtime a lot:

- planning is optional and only runs when routing is enabled and a router exists
- planning uses an empty toolset profile on purpose, so route selection for planning is not biased by execution tools
- execution is iterative and tool-driven; the loop stops only when the model returns an assistant message with no tool calls
- curation is separated from execution so the model that acts does not also have to decide what becomes reusable memory
- all phase records and tool outcomes are folded back into `state.Store.RecordRun()`, which updates `model_stats`

## 4. Model Routing

Routing is local, heuristic, and approval-aware. It is not an unconstrained auto-router.

```mermaid
flowchart TD
    phase["Phase\nplanning, execution,\nchat, memory_curation"]
    default["Phase default model\nfrom config"]
    enabled{"Routing enabled?"}
    stats["Load model_stats for\nphase + task_class + toolset"]
    score["Score candidates\nsuccess_rate * 100\n- denial_rate * 40\n- latency penalty"]
    confidence["Confidence = min(runs / 4, 1.0)"]
    approved{"Top candidate approved\nand confidence >= threshold?"}
    recommend{"Top candidate unapproved,\npositive score, and confident?"}
    prompt["Prompt user for one-off approval"]
    persist["Save approved_once\nmodel approval in SQLite"]
    useTop["Use routed model"]
    useDefault["Use default model"]

    phase --> default --> enabled
    enabled -- "no" --> useDefault
    enabled -- "yes" --> stats --> score --> confidence --> approved
    approved -- "yes" --> useTop
    approved -- "no" --> recommend
    recommend -- "yes" --> prompt
    prompt -- "approved" --> persist --> useTop
    prompt -- "rejected" --> useDefault
    recommend -- "no" --> useDefault
```

The route profile is more specific than just "best model overall". It keys off:

- phase
- task class
- toolset

That means a model can be preferred for execution on debugging tasks with `shell_execute`, while another model can still win for planning or chat.

Important boundary: unapproved models do not silently take over. The router can recommend them, but the user still has to approve a confident new candidate before the runtime switches.

## 5. Memory Lifecycle

Memory is deliberately split between provisional notes and durable memory.

```mermaid
flowchart TD
    run["Run or chat turn"]
    curate["Curation phase returns lessons"]
    filter["filterPersistableLessons()\nreject low-confidence or generic advice"]
    findings["Write to findings.md\nand save memory_entries rows"]
    repeat{"Observed repeatedly?"}
    promote["Promote to memory.md\nand raise confidence floor"]
    snapshot["Snapshot files before writes"]
    reindex["Reindex markdown into SQLite"]
    rollback["Rollback restores snapshot\nthen reindexes"]
    fallback["If SQLite is unavailable,\nparse markdown directly"]

    run --> curate --> filter --> findings
    findings --> repeat
    repeat -- "yes" --> promote
    repeat -- "no" --> reindex
    promote --> snapshot --> reindex
    findings --> snapshot
    reindex --> rollback
    fallback --> findings
    fallback --> reindex
```

The memory policy is more disciplined than "append everything forever":

- `findings.md` is the staging area for narrow reusable notes
- `memory.md` is reserved for durable lessons, often after repetition
- repeated lessons across multiple models can be promoted to global memory
- snapshots are taken before managed file writes, so rollback is a first-class workflow
- if SQLite fails, retrieval still works by parsing the Markdown files directly

In practice this gives the project a nice property: humans can read and edit the memory files, but the runtime still gets normalized metadata such as scope, confidence, recency, and seen counts.

## 6. Safety Model

Safety is not just one guard. It is a stack of boundaries around shell access plus separate evaluation commands.

```mermaid
flowchart TD
    call["Model requests shell_execute"]
    parse["ParseShellCommand()\nsegment command\nreject blocked syntax"]
    blocked["Blocked syntax\nredirection, substitution,\nbackticks, raw newlines,\nsingle &, malformed chains"]
    allow["Allowlist or learned\napproved segments?"]
    gate["Secondary approval gate"]
    judge["LLM judge\nSAFE or DANGEROUS"]
    user["User confirm\nreject, approve once,\napprove and remember"]
    remember["Persist approved segments\ninto command_approvals"]
    exec["Run sh -c with timeout\ncapture and stream output"]
    events["Emit structured events\nand telemetry"]

    call --> parse
    parse -- "invalid" --> blocked
    parse -- "valid" --> allow
    allow -- "yes" --> exec --> events
    allow -- "no" --> gate
    gate --> judge
    gate --> user
    judge --> remember
    user --> remember
    remember --> exec
```

The separate safety commands then evaluate those rails from two angles:

```mermaid
flowchart LR
    scorecard["cvkeharness scorecard"]
    corpus["Deterministic shell corpus"]
    rails["Shell validator + allowlist rules"]
    report1["docs/safety-scorecard.md\nand .json"]

    redteam["cvkeharness redteam"]
    model["Live model under test"]
    shadow["Shadow shell tool\nsimulated risky outcomes"]
    report2["docs/redteam-report.md\nand .json"]

    scorecard --> corpus --> rails --> report1
    redteam --> model --> shadow --> report2
```

The practical split is:

- `scorecard` answers "what do the static rails allow or deny right now?"
- `redteam` answers "how does a real model behave when it actively tries to escape those rails?"

## 7. Practical Mental Model

If we compress the whole codebase into one sentence, it is this:

CvkeHarness is a phase-routed tool-using LLM runtime whose behavior is shaped by a layered prompt stack, a small local memory system, approval-aware safety rails, and SQLite-backed learning about what worked before.

If you want to re-enter the code quickly, this is the best reading order:

1. `cmd/run.go`
2. `agent/agent.go`
3. `memory/manager_retrieval.go`
4. `router/router.go`
5. `tools/shell.go`
6. `state/store.go`
7. `safety/scorecard.go` and `safety/redteam.go`
