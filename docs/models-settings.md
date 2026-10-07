# Models, connections, and Settings

`cvkeharness settings` and Console → Settings open the same workspace. First-run
`cvkeharness setup` keeps its guided Safety, Capabilities and Ready stages, and
uses the same connection editor and model picker.

Settings has four sections: **Models**, **Connections**, **Security**, and
**Runtime**. Left focuses the section list; Up/Down chooses a section and
Right/Enter focuses its content. Tab/Shift+Tab toggles between sections and
content. Security keeps Left/Right for changing values, so use Tab there to
return to sections. `[` / `]` also changes section. Within content, Up/Down moves,
Enter edits, and `s` saves. Esc closes an editor or cancels a confirmation;
otherwise it focuses the Console top bar without resetting the section.
Left/Right then selects a workspace and Enter focuses it.
`r` restores saved settings. Changes stay in a draft until Save;
closing a child editor cancels only that editor's pending changes. Existing
chat sessions keep their configuration snapshot; use `/new` after saving.

## Assigning providers independently

A connection owns a provider type, endpoint, credential or local login-file
reference, and display name. Its stable ID is separate from its display name.
Connections can be reused by several roles. Multiple connections can use the
same provider, including different LM Studio servers or API accounts.

A model assignment is either a **connection plus a provider-native model ID**
or an explicit **inheritance relationship**. Selecting an OpenRouter judge
does not change the primary's Codex connection. Model IDs such as
`anthropic/claude-sonnet-4.6` are kept intact.

| Role | Default | Purpose |
| --- | --- | --- |
| Primary | Explicit connection and model | Default for other agent roles |
| Safety judge | Uses Primary | Advisory reviews for security controls using LLM review |
| Safety advisor | Uses Primary | Explains gated actions and recommends approve/reject in LLM advisor mode; humans decide |
| Classifier | Uses Safety judge | Classifies tasks in LLM-judge mode before execution |
| Verifier | Uses Execution | Checks completion evidence; inherits the model actually selected for execution |
| Planning | Uses Primary | Planning phase when routing is enabled |
| Execution | Uses Primary | Agent execution, including console chat |
| Curation | Uses Primary | LLM-based memory curators; the normal structured memory curator does not call an LLM |

Primary, Safety judge, and Safety advisor are always visible. Press `a` to show the advanced
roles. Every role opens the same picker:

- Type to search model IDs and names. Arrows and page keys move through results.
- `Ctrl+K` selects a saved connection, and `Ctrl+R` reloads that connection's catalog.
- Enter keeps the selection in the Settings draft. Escape cancels.
- Inherited roles offer an explicit “Use …” choice.
- Custom model entry starts with the current search text and preserves the exact ID.
- A configured model remains available even when absent from the catalog.

Catalogs identify their source and cache/fallback state. Catalog availability
does not establish that authentication or a model call will succeed. Codex uses
its local CLI model cache; the configured login file can be reused without
copying subscription tokens into the harness configuration.

## LLM judge preset

Select **Security → Security profile → llm_judge** to use the judge for most
actions that should not run automatically. Choose its model and provider in
**Models → Safety judge**, save, and start a new session with `/new`. Setup also
offers the preset and opens the judge model picker.

| Actions | Preset decision |
| --- | --- |
| Known read-only diagnostics | Allow |
| Unknown commands, scripts, file creation/overwrite/append/deletion | LLM review |
| Privilege, service, package, network, remote, cloud, container, database, and scheduled changes | LLM review |
| Credential access and raw-device operations | Deny |

Critical-path protection can escalate an action to direct human approval.
Credential-path protection stays enabled, approval reuse is disabled, and limits
match Reasonable. Individual overrides remain available.

This preset uses the existing judge behavior: `DANGEROUS`, invalid responses,
and provider errors block execution; `SAFE` proceeds to human approval. Unlike
[LLM advisor](llm-advisor.md), it uses the binary judge rather than generating an
explanation and recommendation. The preset does not grant automatic execution
authority to the model.

```yaml
security:
  profile: llm_judge
models:
  safety_judge:
    inherit: primary
# Or set connection and model to choose a different judge.
```

The default remains Reasonable. Existing legacy `safety_mode: llm_judge`
configurations keep their migration behavior; select `security.profile:
llm_judge` explicitly to enable the new preset.

## Managing connections

In Connections, `a` adds a connection and Enter edits one. Provider selection is
explicit. A connection used by model roles keeps its provider type: add another
connection and select a model there to change provider. Endpoint and credential
changes affect every role using that connection. Credentials remain hidden.

`d` removes an unused connection from the draft. Connections used directly or
through inheritance cannot be removed until those roles are reassigned. Saving
validates role references, inheritance, connection definitions, and required
API keys. It does not call an LLM, install dependencies, or start a daemon.

The configuration file is written atomically with mode `0600`. Provider login
files remain external references. Runtime readiness checks each distinct
connection used by the roles, including local login-file presence.

Settings remains accessible when provider setup is incomplete. In that case it
opens in configuration-only mode without starting a runtime or opening the state
database. Save and reopen the console to start work. Malformed YAML is reported
without overwriting the file.

## Configuration and compatibility

Example model configuration, with placeholder credentials:

```yaml
connections:
  subscription:
    name: Codex subscription
    provider: codex
  review-api:
    name: OpenRouter
    provider: openrouter
    api_key: REPLACE_LOCALLY
  laptop:
    name: LM Studio laptop
    provider: lmstudio
    base_url: http://127.0.0.1:1234/v1
models:
  primary:
    connection: subscription
    model: gpt-5.2-codex
  safety_judge:
    connection: review-api
    model: anthropic/claude-sonnet-4.6
  classifier:
    inherit: safety_judge
  verifier:
    inherit: execution
  execution:
    inherit: primary
```

Model IDs above are examples; use the catalog available to the selected
connection or enter its exact model ID.

Legacy `provider`, `default_model`, `safety_model`, API-key maps and phase-model
fields remain readable. Opening the editor materializes named connections and
explicit role bindings in its draft, preserving effective choices. New bindings
take precedence. Migration is persisted only when configuration is saved.
Migrated files contain the canonical connections and roles without stale legacy
model fields; unrelated keys such as the web-search credential are retained.

Named connection identity travels with runtime selections and approval
references. Existing historical routing statistics are provider/model scoped;
they cannot prove which custom connection produced a result. Automatic routing
therefore retains a configured named connection rather than substituting
another endpoint or account based on those old statistics. Explicit role
assignments continue to work. This applies to every explicit connection, including
IDs such as `openrouter` or `lmstudio`. Unmigrated legacy configurations retain
their existing provider-scoped routing behavior. Connection IDs are also included
in prompt telemetry and persisted phase explanations.

## Implementation boundaries

- `config/models.go`: connection schema, role inheritance, migration and validation.
- `internal/modelruntime`: provider construction for resolved connections.
- `internal/modelcatalog`: one catalog service, independent of setup and Settings.
- `internal/modelui`: reusable picker and connection editor; no persistence.
- `internal/tui/settings_workspace.go`: shared Settings navigation and draft transaction.
- `internal/setuptui`: guided onboarding that mounts the shared components.

The previous standalone raw-terminal settings menu and its duplicate model
catalogs have been removed.
