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
    cli["Command surfaces\ncmd/run.go, cmd/tui.go,\ncmd/chat_session.go, cmd/memory.go,\ncmd/models.go, cmd/commands.go,\ncmd/scorecard.go, cmd/redteam.go"]
    cfg["config.Config\n~/.cvkeharness/config.yaml"]
    agent["agent.Agent"]
    router["router.Router"]
    tools["tools.Registry\nshell_execute\nmemory_record_finding"]
    memory["memory.Manager"]
    store["state.Store\n~/.cvkeharness/state.db"]
    provider["provider.Provider\nCodex, OpenRouter, OpenAI, or LM Studio"]
    files["guidance.md + generated views\ntargets.md\nplaybooks.md\nfindings.md\ncautions.md"]
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

The runtime keeps two persistence roles distinct:

- `guidance.md` for user-authored prompt context plus generated Markdown views for operator inspection and explicit validated import
- SQLite for canonical operational memory, routing stats, exact action grants, chat history, and runtime records

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
    stateLoad["LoadOperationalMemory()\nfrom canonical SQLite\nfail closed if unavailable"]
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
- target identity can tighten mid-run after observed `ssh`, `scp`, or `rsync` commands
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

That means a model can be preferred for execution on debugging tasks with `shell_execute`, while another model still wins for planning or chat.

## 5. Operational Memory Lifecycle

Memory is target-aware, canonical in SQLite, and review-gated.

```mermaid
flowchart TD
    task["Task or chat turn"]
    resolve["Resolve exact target\nruntime host or provisional remote target"]
    run["Execute tools and collect bounded outcomes"]
    verifier{"Completion verifier\nand explicit postcondition satisfied?"}
    fact["Typed host-fact candidate"]
    playbook["Playbook candidate\nwith verifier evidence + success check"]
    failure{"Concrete failure or denial?"}
    caution["Short-lived caution candidate"]
    note["memory_record_finding"]
    finding["Untrusted finding candidate"]
    sqlite["Persist candidate in canonical SQLite"]
    views["Regenerate Markdown views\nand snapshot drift"]
    review{"Operator review"}
    active["Bounded active memory"]
    terminal["Rejected, revoked, or expired"]

    task --> resolve --> run
    run --> fact --> sqlite
    run --> verifier
    verifier -- "yes" --> playbook --> sqlite
    run --> failure
    failure -- "yes" --> caution --> sqlite
    note --> finding --> sqlite
    sqlite --> views --> review
    review -- "promote exact record" --> active
    review -- "reject" --> terminal
    active -- "revoke or expire" --> terminal
```

Important behaviors:

- remote targets begin with `environment=unknown` and cannot contribute retrievable memory until an operator binds environment and remote identity labels
- all model-, tool-, and probe-derived records begin as candidates
- exit code zero alone does not produce a promotable playbook; verifier evidence and a meaningful success check are required
- candidates never enter prompts before explicit promotion
- generated Markdown is not a runtime fallback authority

## 6. Retrieval Gates

The retrieval system deliberately favors fail-closed precision over breadth.

```mermaid
flowchart TD
    request["Target-aware retrieval request"]
    sqlite["Load canonical SQLite state"]
    target{"Live, unexpired, exact\ntarget + environment?"}
    integrity{"Active status, trusted, unexpired,\nvalid evidence hash?"}
    playbook{"For playbook: meaningful\nsuccess check present?"}
    select["Select at most one target summary,\none playbook, one caution,\nand one fallback finding"]
    render["Render historical verify-first brief\nnever whole files"]
    withhold["Withhold target-scoped memory"]

    request --> sqlite --> target
    target -- "no" --> withhold
    target -- "yes" --> integrity
    integrity -- "no" --> withhold
    integrity -- "yes" --> playbook
    playbook -- "no" --> withhold
    playbook -- "yes or not a playbook" --> select --> render
```

Candidate, rejected, revoked, expired, untrusted, wrong-environment, and tampered records are excluded from every injection path, including target summaries. There is no direct-use mode.

## 7. Safety Model

Safety is not just one guard. It is a stack of boundaries around shell access plus separate evaluation commands.

```mermaid
flowchart TD
    call["Model requests shell_execute"]
    parse["Parse command and\nclassify concrete effects"]
    policy["Evaluate immutable security policy\ndeny > ask > llm_review > allow"]
    blocked["Deny or malformed action"]
    judge["Optional advisory LLM review\nnever authority"]
    user["Human approval\nexact action + effects + policy +\nhost + principal + working directory"]
    grant["Process-local exact grant or\n15-minute atomic single-use\nsecurity_action_grant"]
    exec["Run sh -c with timeout\ncapture and stream output"]
    events["Emit structured events\nand telemetry"]

    call --> parse --> policy
    policy -- "deny" --> blocked
    policy -- "allow" --> exec --> events
    policy -- "llm_review" --> judge --> user
    policy -- "ask" --> user
    user -- "reject" --> blocked
    user -- "approve exact action" --> grant --> exec
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

CvkeHarness is a phase-routed tool-using LLM runtime whose behavior is shaped by a layered prompt stack, effect-aware safety rails, exact scoped grants, and compact target-aware operational memory that is canonical in SQLite and exposed through generated views.

If you want to re-enter the code quickly, this is the best reading order:

1. `cmd/run.go`
2. `agent/agent.go`
3. `memory/manager_retrieval.go`
4. `memory/manager_persist.go`
5. `tools/shell.go`
6. `state/store.go`
7. `state/store_operational_memory.go`
8. `safety/scorecard.go` and `safety/redteam.go`
