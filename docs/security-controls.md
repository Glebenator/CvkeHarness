# Security profiles and controls

> Detailed standalone reference: [Security controls HTML guide](security-controls.html)

CvkeHarness treats the model as a fallible operator. The primary risks are the wrong target, destructive side effects, over-broad scope, stale state, duplicate mutations, exposed credentials, and an unavailable recovery path—not a model “escaping.”

This design is grounded in effect control:

- [YoloFS](https://arxiv.org/abs/2604.13536) found that prompts and command-string filters do not reliably constrain filesystem effects; staging, snapshots, and progressive permissions made hidden effects reviewable in its selected incident corpus.
- [AgentDojo](https://papers.neurips.cc/paper_files/paper/2024/file/97091a5177d8dc64b1da8bf3e1f6fb54-Paper-Datasets_and_Benchmarks_Track.pdf) demonstrates both ordinary agent failures and redirection through untrusted tool data.
- The [GPT-5-Codex system card](https://cdn.openai.com/pdf/97cc5669-7a25-4e63-b15f-5fd5bdc4d149/gpt-5-codex-system-card.pdf) and [Claude Code sandbox documentation](https://code.claude.com/docs/en/sandboxing) likewise use sandbox boundaries, network restrictions, approvals, and logs rather than trusting model training alone.
- A second LLM is useful as an advisor, not as the authorization boundary. Sequential-harm research found that prompt engineering and monitor ensembles did not reliably eliminate monitoring failures ([OpenReview](https://openreview.net/forum?id=PGsM81SWHt)).

## Configuration model

`security.profile` selects one bundle. `security.overrides` stores only individually changed values. Runtime startup resolves both into one immutable effective policy with a short hash. There is no separate “bundle authorization” path.

The profiles are:

| Profile | Intended posture |
| --- | --- |
| `extra_strict` | Known reads run. Opaque and destructive actions are denied. Most mutations are denied or require a person. |
| `reasonable` | Default. Reads and new-file creation run; overwrite, delete, privilege, service, package, network, cloud, container, database, and scheduled mutations ask. Credential and raw-device access are denied. |
| `less_strict` | Routine local and recoverable changes run. Delete, privilege, cloud, remote, database, and credential effects still ask. Unknown commands may receive advisory LLM review. |
| `minimal` | Most effects run. Critical paths, raw devices, and credential access still interrupt. |
| `yolo` | CvkeHarness approval and deletion guards are disabled. Operating-system permissions, cloud RBAC, resource locks, and other external protections still apply. |

Changing one control produces `<profile> + N overrides`. Applying another profile requires confirmation and clears the previous overrides. YOLO always requires explicit confirmation.

## Enforced settings

The current catalog contains 25 settings in seven groups:

- Commands: known reads, unknown commands, and script interpreters.
- Filesystem: create, overwrite/truncate, append, delete, critical-path protection, and credential-path protection.
- System: privilege/ownership changes, service changes, package lifecycle changes, and raw-device access.
- Network and remote: outbound access, remote mutation, cloud change, container change, and destructive database statements.
- Autonomy: scheduled-job and crontab mutation.
- Approvals: exact action reuse for the current process plus explicit, 15-minute, single-use deferred grants. Grants bind the action/effect digest, effective policy, host, principal, and working directory. New-policy approvals are never persisted as unscoped command strings, and an LLM judgment never creates reusable authority.
- Limits: command bytes, command segments, wall-clock timeout, and captured output bytes.

Decisions are ordered `deny > ask > llm_review > allow`. The strictest effect in an action wins. `llm_review` runs an advisory review and still requires human approval; it is not authority by itself. Tool advertisement is not authorization: schedule, system-cron, memory-write, web, and future unclassified tools pass through the registry policy before execution, while shell commands receive deeper effect analysis.

Shell redirection is classified instead of blocked by punctuation:

- `2>/dev/null` and `2>&1`: descriptor plumbing; no persistent write.
- `> new-file`: create.
- `> existing-file`: truncate/overwrite.
- `>> file`: append.
- `< file`: read, with credential-path escalation where applicable.

The classifier also covers common deletion and mutation paths including `rm`, `find -delete`, command wrappers, interpreter deletion primitives, `git clean`, `rsync --delete`, service/journal mutations, package managers, cloud CLIs, containers, and destructive SQL. Unknown actions follow the configured unknown-effect decision rather than inheriting permission from an allowlisted executable name.

When background work pauses for a person, `commands approve-work <id>` reconstructs the exact captured action, rechecks it under the current policy, and issues one grant for the original executor scope. Only a digest and masked summary are stored in the grant table; changing the action, effects, policy, host, principal, or directory invalidates it. A spent or expired grant cannot authorize a retry. Secret-bearing blocked payloads are masked at persistence and may need to be rerun interactively instead of resumed.

## Setup and Settings interaction

Guided setup keeps the existing four stages: Connect, Safety, Capabilities, Ready.

Safety presents the five profiles and a concise consequence. Press `A` to reveal all individual controls; use Left/Right or Space to change one, and `R` to restore its profile value. Ready shows the effective profile, override count, and policy hash. Saving configuration remains separate from applying install or daemon actions.

The dashboard Settings page has a dedicated Security subpage. It shows each control’s effective value and source (`profile` or `override`). `r` resets one override, `R` requires a second press before resetting all, and profile application requires Enter. Changes apply to new sessions after save; an active session retains its immutable snapshot.

## Invariants and current limits

These correctness and privacy invariants are not profile toggles:

- invalid or unknown policy values fail closed;
- configuration writes are atomic and mode `0600`;
- the state database and authorization metadata are mode `0600`;
- shell output is bounded and redacted before it is emitted;
- model review cannot persist approval;
- security changes cannot silently alter an already-running session;
- the UI states consequences in text rather than color alone.

The current implementation does **not** claim an OS-enforced sandbox, copy-on-write staging filesystem, automatic snapshot/trash recovery, exact delete enumeration/byte budgets, remote target fingerprinting, provider-native dry-run/delete protection, or executable-content hashing. Those controls are valuable, but showing enabled toggles before the enforcement mechanism exists would be security theater.

Recommended follow-on order:

1. Extend the action envelope with resolved remote target identity, executable-content hashes, scope, and recoverability.
2. Add delete enumeration, protected-root budgets, a recovery manifest, and at least one real Trash/snapshot provider.
3. Add an OS-enforced filesystem/process/network sandbox and fail closed where the selected profile requires it.
4. Add typed cloud, Kubernetes, database, and service adapters with plan/apply, dry-run, preconditions, idempotency keys, and provider-native recovery checks.
5. Add redacted tamper-evident authorization/result audit and postcondition verification.
