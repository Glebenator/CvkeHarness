# CvkeHarness Visual Guide

This guide is a companion to:

- [README.md](../README.md)
- [memory-model.md](memory-model.md)
- [architecture.md](architecture.md)

It focuses on the parts of the project that matter most when you are trying to rebuild the mental model quickly:

- context management
- the agent loop
- model routing
- target-aware memory
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
    files["Readable managed files\nguidance.md\ntargets.md\nplaybooks.md\nfindings.md\ncautions.md"]
    safety["safety package\nscorecard + redteam"]
    telemetry["telemetry/live/events.jsonl\ncanonical runtime events"]

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

- readable files for durable operator understanding
- SQLite for routing stats, approvals, indexing, chat history, and operational memory lookup

## 2. Context Management

The project does not build one giant prompt. It assembles context in layers and keeps the retrieved part small.

```mermaid
flowchart TD
    task["User task or chat turn"]
    classify["core.ClassifyTask\ninspection, debugging,\nshell_heavy, policy_sensitive,\nlong_horizon, summarization, general"]
    resolve["ResolveTarget()\nruntime host or remote target"]
    retrieval["core.RetrievalContext\nphase + task_class + active_model\n+ runtime_host_id + target_id\n+ target_kind + tool_names + trouble"]
    ensure["memory.Manager.EnsureFiles()"]
    files["Read guidance.md"]
    stateLoad["LoadOperationalMemory()\nor parse markdown fallback"]
    rank["Select one compact brief\nruntime host summary\n+ optional target summary\n+ optional playbook\n+ optional caution\n+ optional finding"]
    stack["buildPromptPlan()\n1. compiled guidance prefix\n2. stable tool policy\n3. host-target-memory brief\n4. volatile turn context"]
    chat["agent.ChatState"]
    refresh["One refresh after tool trouble\nor policy denial"]

    task --> classify --> resolve --> retrieval
    retrieval --> ensure --> files
    retrieval --> stateLoad --> rank --> stack
    files --> stack --> chat
    chat --> refresh
    refresh --> retrieval
```

What matters here:

- `builtInRules()` is fixed runtime policy and keeps baseline behavior stable
- `guidance.md` is the user-authored operating surface
- retrieval is structured-first, not semantic-first
- the runtime host and the active target are modeled separately
- mid-run refresh is allowed once after tool trouble so the model can see a tighter brief for the failing target/tool

## 3. Agent Loop

`agent.Agent` is the center of gravity for the project.

```mermaid
flowchart TD
    start["Run(task)"]
    classify["Classify task"]
    resolve["Resolve initial target"]
    planning{"Routing enabled?"}
    planPhase["Planning phase\nsingle model call\nno tools\nreturns concise notes"]
    execSelect["Select execution model"]
    retrieve["Retrieve execution brief"]
    build["Build system prompt stack\n+ user task"]
    loop["Execution loop\nup to MaxIterations"]
    model["Provider.ChatCompletion()"]
    done{"Tool calls?"}
    finish["Return assistant output"]
    toolExec["Execute requested tools"]
    retarget["Resolve target again if shell command reveals more"]
    trouble{"Denied or repeated failure?"}
    refresh["Refresh compact brief once"]
    curate["Deterministic CurateRunOutcome()\nplaybooks + cautions + facts + findings"]
    record["Record run, phase stats,\nand tool outcomes"]

    start --> classify --> resolve --> planning
    planning -- "yes" --> planPhase --> execSelect
    planning -- "no" --> execSelect
    execSelect --> retrieve --> build --> loop --> model --> done
    done -- "no" --> finish --> curate --> record
    done -- "yes" --> toolExec --> retarget --> trouble
    trouble -- "yes" --> refresh --> loop
    trouble -- "no" --> loop
```

Key details:

- planning is optional and tool-free
- execution is iterative and tool-driven
- the endpoint label can tighten mid-run after observed `ssh`, `scp`, or `rsync` commands
- the default runtime now curates memory deterministically from observed outcomes rather than asking a model to invent durable structure

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
    persist["Save non-reusable\none-off routing decision"]
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

That means a model can be preferred for execution on debugging tasks with `shell_execute`, while another model still wins for planning or chat.

## 5. Operational Memory Lifecycle

Operational memory is target- and environment-scoped planning context. It is separate from managed policy and command authorization.

```mermaid
flowchart TD
    task["Task or chat turn"]
    resolve["Resolve target\nruntime host or remote target"]
    run["Execute tools and collect outcomes"]
    facts["Extract typed facts\nfrom explicit probes"]
    factgate{"Operator reviewed\nprobe evidence?"}
    activefact["Promote scoped fact"]
    success{"Successful sequence with\nexplicit postcondition?"}
    playbook["Create playbook candidate"]
    failure{"Concrete failure or denial?"}
    caution["Create short-lived\ncaution candidate"]
    note["Optional ad hoc note\nmemory_record_finding"]
    findings["Create untrusted\nfinding candidate"]
    inbox["Operator review inbox"]
    decision{"Promote, reject,\nor leave pending?"}
    active["Active, scoped,\nexpiring memory"]
    views["Regenerate readable\nMarkdown views"]

    task --> resolve --> run
    run --> facts --> factgate
    factgate -- "yes" --> activefact --> active
    factgate -- "no" --> inbox
    run --> success
    success -- "yes" --> playbook
    run --> failure
    failure -- "yes" --> caution
    note --> findings
    playbook --> inbox
    caution --> inbox
    findings --> inbox
    inbox --> decision
    decision -- "promote" --> active
    decision -- "reject" --> views
    active --> views
```

Important behaviors:

- SQLite is canonical; Markdown files are generated views and explicit import sources
- targets require a deterministic endpoint-label ID, environment, and operator-confirmed remote identity label before active memory can support planning
- endpoint labels are not live machine fingerprints; the operator still verifies the endpoint before mutation
- model-authored notes, successful command sequences, and failure text enter the review inbox as untrusted candidates
- typed facts from explicit probes remain candidates until operator review; probe output cannot silently rewrite target identity
- playbooks require a success check before promotion, and every retrieved playbook remains a historical verify-first hint
- `memory_record_finding` cannot create policy, approvals, credentials, host mappings, or active memory

## 6. Retrieval Priorities

The retrieval system deliberately favors precision over breadth.

```mermaid
flowchart TD
    request["Target-aware retrieval request"]
    runtime["Always include\nruntime-host summary"]
    exact1["1. exact target + intent + tool"]
    exact2["2. exact target + intent"]
    exact3["3. exact target + tool"]
    caution["4. exact target caution"]
    fallback["5. fallback finding\nonly if no strong playbook"]
    render["Render compact brief\nnever whole files"]

    request --> runtime --> exact1 --> render
    request --> exact2 --> render
    request --> exact3 --> render
    request --> caution --> render
    request --> fallback --> render
```

Freshness buckets:

- `fresh`
- `stale`
- `cold`

Rendering behavior:

- active, unexpired, integrity-valid records are considered only for an exact target and environment match
- every playbook renders as a historical verify-first hint
- candidate, rejected, revoked, expired, tampered, and unrelated target memory does not enter the brief

## 7. Safety Model

Safety is not just one guard. It is a stack of boundaries around shell access plus separate evaluation commands.

```mermaid
flowchart TD
    call["Model requests shell_execute"]
    parse["ParseShellCommand()\nsegment command\nreject blocked syntax"]
    blocked["Blocked syntax\nredirection, substitution,\nbackticks, raw newlines,\nsingle &, malformed chains"]
    allow["Allowlist, same-session approval,\nor explicit CLI exception?"]
    gate["Secondary approval gate"]
    judge["LLM judge\nSAFE or DANGEROUS"]
    user["User confirm\nreject, approve once,\napprove and remember"]
    session{"Exact session ID\npresent?"}
    remember["Persist same-session approval\ninto scoped_command_approvals"]
    exec["Run sh -c with timeout\ncapture and stream output"]
    events["Emit structured events\nand telemetry"]

    call --> parse
    parse -- "invalid" --> blocked
    parse -- "valid" --> allow
    allow -- "yes" --> exec --> events
    allow -- "no" --> gate
    gate --> judge
    gate --> user
    judge --> exec
    user --> session
    session -- "yes, remember" --> remember
    session -- "no or approve once" --> exec
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

## 8. Practical Mental Model

If we compress the whole codebase into one sentence, it is this:

CvkeHarness is a phase-routed tool-using LLM runtime whose behavior is shaped by a layered prompt stack, approval-aware safety rails, and a compact target-aware operational memory system backed by readable markdown and SQLite indexing.

If you want to re-enter the code quickly, this is the best reading order:

1. `cmd/run.go`
2. `agent/agent.go`
3. `memory/manager_retrieval.go`
4. `memory/manager_persist.go`
5. `tools/shell.go`
6. `state/store.go`
7. `state/store_operational_memory.go`
8. `safety/scorecard.go` and `safety/redteam.go`
