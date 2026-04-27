# Scheduled Jobs And System Cron

This guide explains the scheduling feature added to CvkeHarness: durable
internal jobs for agent-managed recurring work, plus explicit current-user
crontab management for users who want OS-level cron.

The design follows the same core lesson as OpenClaw's scheduler: the model can
request scheduling, but deterministic runtime code owns persistence, timers,
state transitions, audit logging, and safety gates.

## What Was Added

CvkeHarness now has two scheduling paths.

| Path | Use it for | Default? | Execution owner |
| --- | --- | --- | --- |
| Internal scheduled jobs | Health checks, recurring agent tasks, reminders, periodic summaries | Yes | CvkeHarness SQLite scheduler |
| System crontab | User explicitly asks for OS/user/system cron | No | Current user's crontab |

The default is intentionally the internal scheduler. It keeps recurring work
inside the same harness boundary as normal runs: routing, memory, tool safety,
shell approvals, and run recording still apply.

System crontab support is available for DevOps workflows that need real cron,
but v1 only manages the current user's crontab. It does not touch root cron,
`/etc/cron.d`, systemd timers, or launchd.

## Internal Scheduled Jobs

Internal jobs are stored in SQLite and run through the normal agent runtime.

Supported schedule kinds:

- `at`: one-shot RFC3339 timestamp, such as `2026-04-27T12:00:00Z`
- `every`: Go duration, such as `5m`, `1h`, or `24h`
- `cron`: basic five-field cron expression, such as `*/5 * * * *`

Example:

```bash
cvkeharness jobs add \
  --name "Health check" \
  --kind every \
  --spec 5m \
  --prompt "Check the local server health endpoint and summarize failures."
```

Useful commands:

```bash
cvkeharness jobs list
cvkeharness jobs run <job-id>
cvkeharness jobs run-loop
cvkeharness daemon
cvkeharness jobs pause <job-id>
cvkeharness jobs resume <job-id>
cvkeharness jobs remove <job-id>
cvkeharness jobs runs <job-id>
```

`cvkeharness jobs run-loop` and `cvkeharness daemon` poll for due jobs and run
them. Each run creates a scheduled job run record and also flows through the
regular agent run recorder.

## System Crontab Management

System cron support lives behind explicit user intent. The agent tool
description tells the model to use system cron only when the user asks for
OS-level, user-crontab, or system-crontab scheduling.

The implementation uses:

```bash
crontab -l
crontab -
```

through `exec.Command`, not shell interpolation.

It preserves:

- comments
- environment lines
- blank lines
- unmanaged cron entries
- disabled cron entries

When CvkeHarness creates a crontab entry, it adds metadata:

```cron
# Health check
# cvkeharness:id=cron_...
*/5 * * * * curl -fsS http://localhost:8080/health
```

Useful commands:

```bash
cvkeharness cron list
cvkeharness cron show

cvkeharness cron dry-run \
  --action add \
  --schedule "*/5 * * * *" \
  --command "curl -fsS http://localhost:8080/health" \
  --name "Health check"

cvkeharness cron add \
  --schedule "*/5 * * * *" \
  --command "curl -fsS http://localhost:8080/health" \
  --name "Health check"

cvkeharness cron update <target> \
  --schedule "*/10 * * * *" \
  --command "curl -fsS http://localhost:8080/ready"

cvkeharness cron disable <target>
cvkeharness cron enable <target>
cvkeharness cron remove <target>
```

Targets can be a CvkeHarness-managed cron ID, a line number, or a stable hash
for unmanaged entries.

Every system crontab write prints a before/after diff and requires interactive
confirmation before installation.

## Agent Tools

Two tools were added to the default registry when SQLite state is available.

### `schedule_manage`

Use this for normal recurring agent work.

Actions:

- `list`
- `add`
- `update`
- `remove`
- `run_now`
- `pause`
- `resume`
- `runs`

Example agent intent:

> Check the server health every five minutes.

Expected behavior: create an internal scheduled job unless the user explicitly
asks for crontab.

### `system_cron_manage`

Use this only for user-crontab work.

Actions:

- `list`
- `show`
- `add`
- `update`
- `remove`
- `enable`
- `disable`
- `dry_run`

Example agent intent:

> Add this to my user crontab.

Expected behavior: prepare a system crontab mutation, show a diff, and require
confirmation.

## Persistence And Audit

SQLite now stores:

- scheduled job definitions
- scheduled job run history
- system crontab mutation audit records

System cron audit records include:

- action
- target
- previous crontab snippet
- proposed/new crontab snippet
- success or failure
- error message
- initiating tool
- timestamp

This keeps OS-level scheduling visible to the harness without making cron the
default backend.

## Safety Model

V1 safety choices:

- Internal scheduler is the default.
- System cron is current-user only.
- System cron writes always require confirmation.
- Crontab commands are not executed when entries are created.
- Crontab commands are validated as single-line cron entries.
- Newlines, carriage returns, and NUL bytes are rejected in cron commands.
- Existing crontab content is preserved unless explicitly targeted.
- Tests use fake crontab runners instead of touching the host crontab.

This keeps the feature useful for DevOps work while avoiding the largest
foot-guns: silent OS scheduler mutation, root cron edits, and broad platform
automation before the safety story is ready.

## Tests Added

The feature includes tests for:

- `at`, `every`, and five-field cron next-run calculation
- internal job creation, pause, resume, due execution, success, and failure
- scheduled run history
- crontab parsing with comments, env vars, disabled entries, managed entries,
  and unmanaged entries
- crontab add, update, disable, and remove through a fake runner
- malformed cron schedules and multiline command rejection

The full suite was verified with:

```bash
go test ./...
```

## What Should Be Done In V2

V2 should make the feature more reliable, observable, and production-friendly
without losing the lightweight design.

### Scheduler Reliability

- Add job claiming/locking so multiple daemons cannot run the same job at once.
- Track `running_at`, timeout, and stale-run recovery in SQLite.
- Add bounded retry policy with backoff for internal scheduled jobs.
- Add missed-run catch-up behavior after daemon downtime.
- Add max runtime per job and cancellation support.
- Add concurrency limits for daemon execution.

### Scheduling Features

- Support timezone-aware cron schedules.
- Support richer cron syntax through a well-tested library if acceptable.
- Add deterministic jitter/stagger for top-of-hour recurring jobs.
- Add `delete_after_run` for one-shot jobs.
- Add per-job model, routing, max token, and tool allowlist overrides.
- Add structured job labels/tags for filtering.

### System Cron Hardening

- Improve unmanaged entry targeting with explicit `show <target>` and safer
  confirmation text.
- Add crontab backup/restore commands.
- Add import/sync commands to mirror selected crontab entries into SQLite audit
  metadata.
- Add optional policy modes: read-only, confirm-writes, and unattended writes.
- Add root/system cron only behind a separate privileged mode and explicit user
  opt-in.

### Observability

- Add `cvkeharness jobs status` with daemon health, next wake time, running jobs,
  and recent failures.
- Add `cvkeharness jobs logs <job-id>` for compact run history.
- Add failure notifications through existing chat or future webhook channels.
- Add structured JSON output flags for automation.
- Add metrics-style summaries: success rate, average duration, last failure.

### Agent Experience

- Teach the agent to summarize scheduled work after creation with the next run
  time and management commands.
- Add stronger tool output schemas so the model can reason about schedules
  without parsing prose.
- Let the agent choose a lightweight health-check template for common cases.
- Add explicit distinction between "schedule an agent task" and "install an OS
  cron entry" in system prompts or tool guidance.

### Platform Integrations

- Add optional systemd timer support on Linux.
- Add optional launchd support on macOS.
- Keep these as separate adapters, not replacements for the internal scheduler.
- Maintain the same rule: internal scheduler by default, platform scheduler only
  on explicit request.

