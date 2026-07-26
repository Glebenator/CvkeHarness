# CvkeHarness Memory Model

CvkeHarness memory is a target-scoped planning aid. It is not an authorization system.

> Memory may influence what the model proposes. Managed policy and explicit approval determine what the harness may execute.

## Four state planes

CvkeHarness keeps four concepts separate:

1. **Managed policy**: configured safety mode, static command allowlist, approval gates, tool validation, and other operator-owned enforcement. Models and memory cannot edit this plane.
2. **Live fleet inventory**: deterministic endpoint-label IDs, environment, transport, and operator-confirmed remote identity labels.
3. **Operational knowledge**: target-scoped facts, playbooks, findings, and cautions. This is historical context, filtered before retrieval and presented as a hint.
4. **Run state**: current task, tool outcomes, telemetry, and resumable blocked work. It is short-lived execution context, not durable operational truth.

## Canonical persistence

SQLite at the configured `state_db_path` is canonical for live fleet inventory and operational knowledge. The runtime fails closed if SQLite is unavailable.

The memory directory contains generated operator views:

- `guidance.md`: user-authored operating guidance. It is part of prompt context, not policy.
- `targets.md`: generated target inventory and fact view.
- `playbooks.md`: generated playbook view.
- `findings.md`: generated finding and candidate view.
- `cautions.md`: generated caution and candidate view.

Editing a generated Markdown view does not change live memory. Apply manual edits only through the explicit validated import boundary:

```bash
cvkeharness memory export ./memory-review
# edit the exported Markdown
cvkeharness memory import ./memory-review
```

Import validates target scope, environment, status, trust, expiry, playbook success checks, and known secret markers before atomically replacing canonical operational state. Unchanged records must retain valid integrity metadata. Deliberately edited content receives `source=operator_import`, a validated-import evidence reference, and a recalculated integrity hash. A validation rejection leaves SQLite unchanged. Generated view files are written with atomic file replacement; if a view write fails after the database commit, the import returns an error but SQLite contains the imported state, and `cvkeharness memory export` regenerates the views.

## Target identity

Each live target has:

- an opaque `target_id` derived from the endpoint labels known to CvkeHarness;
- an `environment`, such as `production` or `staging`;
- a transport, such as `ssh` or `local`;
- a transport-specific `remote_identity`, such as `ops@api-01`;
- a bounded verification time and expiry.

CvkeHarness does not collect a live machine fingerprint, SSH host-key fingerprint, or cloud instance identity. A target binding proves only that an operator associated an endpoint label with an environment; it does not cryptographically prove which machine currently answers that endpoint. Operators must still verify the live endpoint before mutation.

New remote targets are provisional and use `environment=unknown`. Provisional or ambiguous targets cannot use operational memory or reusable command approvals. Bind a target deliberately exactly once:

```bash
cvkeharness memory target set-environment \
  target-7f31d4b8c0a1 production ops@api-01
```

Resolution uses explicit command or prose endpoint labels. An already-bound target cannot be rebound with this command. If a requested environment conflicts with the stored endpoint label, or if the same label maps to more than one environment, resolution returns ambiguous and withholds target-scoped memory.

## Operational knowledge metadata

Facts, playbooks, findings, and cautions carry the minimum retrieval metadata:

- target ID and environment;
- status: `candidate`, `active`, `rejected`, `revoked`, or `expired`;
- source and evidence reference;
- content integrity hash;
- trust: `untrusted`, `operator`, or `verified`;
- observed and verified timestamps where applicable;
- expiry.

Read-time gates require an active, unexpired target, exact target and environment match, active status, operator or verified trust, unexpired knowledge, and a valid integrity hash. Playbooks also require an explicit success check. Rejected, revoked, expired, untrusted, wrong-scope, or tampered records are not retrieved.

## Candidate lifecycle

Model-authored notes and learned procedures enter the review inbox:

```text
candidate -> operator review -> active -> expired or revoked
                    \-> rejected
```

The `memory_record_finding` tool submits an untrusted candidate. Failed tool output creates a redacted, short-lived caution candidate. Successful shell sequences create playbook candidates. None of these candidates enter prompt retrieval until an operator promotes them.

Inspect and review:

```bash
cvkeharness memory inbox
cvkeharness memory promote finding <id>
cvkeharness memory reject caution <id>
cvkeharness memory revoke playbook <id>
cvkeharness memory delete finding <id>
```

Facts use `target_id:key` as their review ID. Promotion is refused when target scope is unknown or mismatched. Playbook promotion is also refused without an explicit success check. Only candidates can be promoted or rejected, and only active records can be revoked. Promotion records operator trust and a bounded expiry; it does not invent a verification timestamp.

## Verification semantics

Exit code zero means a tool process returned successfully. It does not prove that a service is healthy, a rollout completed, or the requested state exists.

- Typed low-risk probes, such as `hostname` or parsed OS identity output, create bounded candidates. Probe output never rewrites target identity or enters retrieval without operator promotion.
- A shell sequence remains a candidate playbook.
- A completion verifier plus an explicit postcondition can strengthen a candidate, but operator promotion is still required.
- Successful unrelated tool calls never verify target identity.
- Every retrieved playbook is rendered as a historical, verify-first hint. There is no direct-use mode.

## Retrieval

Retrieval is deterministic and small:

1. built-in safety rules;
2. compiled `guidance.md`;
3. one runtime-host summary;
4. at most one exact-scope target summary;
5. at most one playbook;
6. at most one caution;
7. at most one fallback finding when no strong playbook is available.

Whole memory files are never injected. Prompt text explicitly states that operational memory is historical context, not policy or authorization.

## Approvals are not memory

An LLM safety judgment may allow a command under the configured safety mode, but it cannot create a durable approval. `approved_once` is never reusable.

A reusable approval must be deliberately created by a user and is bound to:

- exact normalized command and action;
- exact stored target ID;
- exact environment;
- exact current remote identity;
- approval policy version;
- expiry, with a maximum CLI TTL of 24 hours.

Only a narrow set of target-level commands can be remembered. Runtime interpolation, shell globbing, and path-dependent commands are excluded because identical text could resolve to different values or files later.

Interactive remembered approvals also require the exact current chat session ID. They are not persisted or reused when session context is absent, and another session cannot reuse them. `cvkeharness commands approve` is different: it creates an explicit operator policy exception without a session ID. That exception can apply across sessions, but only inside its remaining exact target, environment, remote identity, command, action, policy-version, and expiry scope.

```bash
cvkeharness commands approve "systemctl restart api" \
  --target target-7f31d4b8c0a1 \
  --environment production \
  --ttl 1h
```

The default allowlist includes diagnostic `systemctl` actions only. Mutating actions such as restart require approval.

## Privacy and poisoning resistance

- Obvious credential markers are redacted before candidate persistence.
- Commands containing likely secrets are not learned as playbooks or facts.
- Third-party and failed tool output is evidence, not instruction, and remains candidate-only.
- Evidence hashes detect accidental or manual content changes before retrieval.
- Avoid placing credentials, private keys, bearer tokens, or unnecessary personal data in guidance, candidates, commands, or imported Markdown.
- Exported Markdown may contain host identities and operational history. Protect it like internal infrastructure documentation.

## Operator recovery

- Inspect current state: `cvkeharness memory show`
- Review candidates: `cvkeharness memory inbox`
- Correct records: export, edit, then run validated import
- Stop retrieval immediately: `cvkeharness memory revoke <kind> <id>`
- Remove a record from canonical operational state: `cvkeharness memory delete <kind> <id>`. Generated-view snapshots remain as local audit history and the current CLI does not purge them.
- Regenerate stale views: `cvkeharness memory export`

For the complete user guide, open `docs/memory-guide.html` in a browser.
