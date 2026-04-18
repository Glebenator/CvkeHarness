# CvkeHarness Safety And Hardening Plan

This is a living implementation plan for evolving CvkeHarness into a safer,
more reliable LLM-powered DevOps harness.

The current codebase is small and understandable, which is a strong starting
point. The main gaps are not architectural complexity; they are execution
policy, tool-call safety, deterministic tool behavior, and test coverage.

## Goals

- Keep the harness lightweight and easy to reason about.
- Make tool execution safe by default.
- Separate read-only inspection from mutating actions.
- Make tool outputs predictable for models.
- Add enough tests that safety regressions are hard to introduce.
- Improve the system incrementally without requiring a rewrite.

## Current Risk Summary

### High priority

- `shell_execute` is not safe enough today.
  It only validates the first token, then runs the full string via `sh -c`,
  which leaves room for command chaining, redirection, substitution, and other
  shell features.

- Mutating actions are model-autonomous.
  `docker_restart_container` is always available and there is no approval gate,
  dry-run mode, or read-only policy boundary.

- Healthcheck tools can probe arbitrary network targets.
  With a remote model provider, this creates internal network and SSRF-style
  risk.

### Medium priority

- Tool arguments are not centrally validated at runtime.
- Tool outputs are mostly prose instead of stable JSON.
- HTTP retry behavior is not strong enough for repeated POST requests.
- There are no tests covering tool safety, policy, or agent-loop behavior.

### Low priority

- Tracked compiled binaries in the repo root should be removed from source
  control once we are ready to clean up repo hygiene.

## Guiding Principles

1. Default to least privilege.
2. Read-only operations should be easy; mutating operations should be explicit.
3. Prefer typed arguments and structured outputs over free-form text.
4. Safety checks should live in the harness, not only in prompts.
5. Every hardening change should come with tests.
6. Avoid broad rewrites when a narrow improvement will do.

## Workstreams

## 1. Execution Policy

Introduce a central policy layer that classifies every tool and decides whether
the current run can execute it.

Target states:

- Tools declare a risk class such as `read_only`, `network_probe`, `mutating`,
  or `dangerous`.
- The CLI can run in modes such as:
  - `inspect`
  - `approve-mutations`
  - `full-auto`
- Mutating tools are denied unless the selected mode allows them.
- Denials are returned to the model in a structured way so it can recover.

Suggested implementation shape:

- Add metadata to the `Tool` interface or wrap tools in a registration struct.
- Add a policy evaluation step before `Execute`.
- Add CLI flags for execution mode.

## 2. Shell Tool Redesign

Replace the current shell behavior with an argv-based safe executor.

Target states:

- Do not use `sh -c` for model-supplied input.
- Parse into command plus arguments.
- Allowlist exact commands, approved subcommands, and approved flags.
- Block shell metacharacters entirely.
- Return normalized structured output with truncation metadata.

Suggested implementation shape:

- Replace `"command": "..."` with:
  - `command`
  - `args`
- Add per-command policy definitions.
- Keep timeouts and output limits, but report them in JSON.

## 3. Network Safety

Constrain outbound checks so the harness cannot be used as a general network
scanner.

Target states:

- Healthcheck tools validate targets before dialing.
- By default, block local metadata endpoints and sensitive private ranges unless
  explicitly allowed by config.
- Optionally require explicit allowlists for URLs, hosts, ports, or CIDRs.
- Record the resolved target in output for auditability.

Suggested implementation shape:

- Add config fields for:
  - allowed URL prefixes
  - allowed hosts
  - allowed CIDRs
  - allowed ports
- Add a shared target validation package used by HTTP and TCP tools.

## 4. Structured Tool Contracts

Make tool execution more deterministic for the model.

Target states:

- Every tool returns stable JSON.
- Success and failure are machine-readable.
- Important fields are explicit instead of embedded in prose.

Suggested result shape:

```json
{
  "ok": true,
  "tool": "http_healthcheck",
  "summary": "HTTP 200 from /health",
  "data": {
    "status_code": 200,
    "duration_ms": 34
  },
  "truncated": false
}
```

Suggested implementation shape:

- Introduce a shared tool result envelope.
- Migrate tools one by one, starting with shell and healthcheck.
- Keep summaries concise so model context stays small.

## 5. Runtime Validation

Validate tool-call arguments in the harness even if the model already saw a
schema.

Target states:

- Required fields are checked centrally.
- Unknown or malformed arguments are rejected consistently.
- Tools receive already-validated typed input where possible.

Suggested implementation shape:

- Add a validator step in the registry before dispatch.
- Consider moving tool schemas from raw JSON blobs into typed Go structs plus
  generated JSON schema later.

## 6. Agent Loop Reliability

Improve how the agent plans and recovers without making the loop heavy.

Target states:

- System prompt explicitly teaches safe tool usage and approval boundaries.
- Tool errors are structured and actionable.
- The agent can distinguish blocked-by-policy from operational failure.
- Mutating plans can be surfaced before execution in approval modes.

Suggested implementation shape:

- Extend the system prompt with tool safety rules.
- Add optional "plan first" behavior for mutating requests.
- Preserve small message history, but improve semantic quality of tool messages.

## 7. Provider And HTTP Resilience

Strengthen provider behavior around retries and request handling.

Target states:

- Retry logic safely supports repeated POST requests.
- Backoff respects context cancellation.
- Provider errors are normalized.
- Request and response sizes are bounded where practical.

Suggested implementation shape:

- Ensure request bodies can be replayed safely on retry.
- Replace `time.Sleep` with context-aware waiting.
- Add tests for retry-on-429 and retry-on-5xx behavior.

## 8. Test Coverage

Add tests focused on safety and regression prevention first.

Minimum useful test matrix:

- shell tool blocks metacharacters
- shell tool only allows approved command patterns
- mutating tool denied in inspect mode
- healthcheck blocks disallowed targets
- registry rejects malformed tool-call payloads
- agent loop handles policy denial gracefully
- provider retry behavior works for transient server failures

Suggested implementation shape:

- Create fake provider and fake tools for agent-loop tests.
- Add table-driven tests for policy and validation behavior.
- Treat every new safety rule as incomplete until a test exists.

## 9. Repo Hygiene

Clean up source control and delivery basics once the safety work is underway.

Tasks:

- Remove tracked compiled binaries from the repo.
- Add or update `.gitignore`.
- Add a short architecture note describing the execution flow.
- Add a contributor note explaining the safety model.

## Phased Roadmap

## Phase 1: Immediate Safety Baseline

Objective:
Reduce the biggest risks without changing the whole architecture.

Tasks:

- Add tool risk classification.
- Add execution mode flag and deny mutating tools by default.
- Temporarily hard-block shell metacharacters.
- Add first safety tests around shell and policy.
- Improve agent messaging for policy-denied actions.

Exit criteria:

- Read-only inspection works as before.
- Restarts are blocked unless explicitly enabled.
- Shell command chaining is no longer possible.

## Phase 2: Stronger Tool Contracts

Objective:
Make tool use more deterministic and less prompt-dependent.

Tasks:

- Convert shell tool to command plus args.
- Add shared structured tool result envelope.
- Add runtime validation in registry dispatch.
- Convert healthcheck tools to structured results.

Exit criteria:

- Core tools return stable JSON.
- Tool arguments are rejected consistently before execution.

## Phase 3: Network Hardening

Objective:
Make network probing intentional rather than open-ended.

Tasks:

- Add target validation package.
- Add config-based allowlists for hosts, CIDRs, and ports.
- Block sensitive local and metadata endpoints by default.
- Add tests for blocked and allowed targets.

Exit criteria:

- Healthcheck behavior is policy-driven and auditable.

## Phase 4: Provider Reliability And UX

Objective:
Make long-running usage and failure recovery more robust.

Tasks:

- Fix replay-safe HTTP retry behavior.
- Normalize provider errors.
- Improve logs around retries, policy denials, and tool execution summaries.
- Add a plan-first approval UX for mutating actions.

Exit criteria:

- Transient provider failures are handled cleanly.
- The user can understand why a tool was denied or retried.

## Phase 5: Expand DevOps Capability Safely

Objective:
Add more useful operations only after the safety foundation is in place.

Candidate additions:

- container logs tool
- container health summary tool
- deploy/status tools with approval requirements
- richer diagnostics for systemd and journal access

Rule:
No new mutating tool should be added without policy metadata, tests, and a
clear approval story.

## Suggested First Tickets

These are small enough to tackle gradually:

1. Add execution mode and block `docker_restart_container` by default.
2. Add tests for allowed vs blocked shell inputs.
3. Reject shell metacharacters before execution.
4. Introduce a shared JSON tool result format.
5. Convert `http_healthcheck` to structured output.
6. Add target validation for private and metadata IP ranges.
7. Make HTTP retry backoff context-aware.
8. Remove tracked binaries and add `.gitignore`.

## Definition Of Done For This Plan

A change is considered complete when:

- behavior is implemented
- tests cover the new rule or contract
- the user-facing behavior is still understandable
- the change does not silently widen tool power

## Notes

- We should prefer steady hardening over a large redesign.
- The first milestone should focus on reducing unsafe execution paths.
- Once the safety model is stable, adding more DevOps tools will be much safer.
