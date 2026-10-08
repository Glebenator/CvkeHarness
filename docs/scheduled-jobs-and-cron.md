# Scheduled Jobs And System Cron

This guide explains CvkeHarness scheduling: durable
internal jobs for agent-managed recurring work, plus explicit current-user
crontab management for users who want OS-level cron.

The model can request scheduling, but deterministic runtime code owns
persistence, timers, state transitions, audit logging, and safety gates.

## Scheduling paths

CvkeHarness has two scheduling paths.

| Path | Use it for | Default? | Execution owner |
| --- | --- | --- | --- |
| Internal scheduled jobs | Health checks, recurring agent tasks, reminders, periodic summaries | Yes | CvkeHarness SQLite scheduler |
| System crontab | User explicitly asks for OS/user/system cron | No | Current user's crontab |

The default is intentionally the internal scheduler. It keeps recurring work
inside the same harness boundary as normal runs: routing, memory, tool safety,
shell approvals, and run recording still apply.

System crontab support is available for DevOps workflows that need real cron,
but the adapter only manages the invoking user's crontab. It does not edit
another user's crontab, `/etc/cron.d`, systemd timers, or launchd. Invoking it as
root targets root's own crontab.

## Internal Scheduled Jobs

Internal jobs are stored in SQLite and run through the normal agent runtime.

Supported schedule kinds:

- `at`: one-shot future RFC3339 timestamp, such as `2099-01-01T12:00:00Z`
- `every`: Go duration, such as `5m`, `1h`, or `24h`
- `cron`: basic five-field cron expression, such as `*/5 * * * *`

Internal schedules use UTC. `at` accepts an explicit RFC3339 offset and stores
the corresponding UTC instant; a timestamp at or before creation time has no
next run. Cron accepts numeric values, wildcards, lists, ranges, and steps;
names, seconds, `@daily`-style aliases, and per-job timezones are unsupported.
All five fields must match: when both day-of-month and day-of-week are restricted,
the internal scheduler requires both, unlike the OR rule used by many system
cron implementations. Sunday can be `0` or `7`. The next-match search is bounded
to one year.

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
cvkeharness jobs update <job-id> --spec 10m
cvkeharness jobs run <job-id>
cvkeharness jobs run-loop --interval 30s
cvkeharness jobs run-loop --once
cvkeharness daemon --interval 30s
cvkeharness jobs pause <job-id>
cvkeharness jobs resume <job-id>
cvkeharness jobs remove <job-id>
cvkeharness jobs runs <job-id>
cvkeharness jobs health
```

Creating a job does not start a worker. Keep `cvkeharness jobs run-loop` or
`cvkeharness daemon` running, or install the Linux daemon service below. Both
commands poll every 30 seconds by default; `--once` executes the currently due
jobs and exits. Due jobs run serially within each process. Each execution records
a scheduled job run and a regular agent run.

An `every` interval is measured from completion of the previous scheduled run.
Cron chooses its next match after completion. On restart, an overdue recurring
job runs once, then computes a fresh next run; missed occurrences are not
replayed individually. Scheduled `at` jobs are disabled after their attempt,
including failed or blocked attempts, and remain in history. A manual `jobs run`
preserves the existing schedule; resuming an `at` job whose timestamp has passed
does not schedule it again. There is no automatic retry/backoff policy or per-job
maximum runtime.

Scheduled execution defers actions that need human approval instead of prompting
on stdin. A blocked job records the reason and exact blocked work, and is excluded
from normal due-job polling. Inspect it in Console or with `jobs health` and
`jobs runs <job-id>`. After reviewing the captured action, grant it once with:

```bash
cvkeharness commands approve-work <blocked-work-id>
cvkeharness jobs run <job-id>
```

The grant expires after 15 minutes and binds the original executor host,
principal, directory, action/effects, and policy. Approval unblocks the job but
does not itself execute it; a manual run retries the prompt through normal policy.
Changed actions or scopes need a new approval. Enabled recurring jobs can also
run at their next due time after unblocking. Pausing/resuming alone does not
clear a blocked approval. See [security controls](security-controls.md).

### Linux systemd daemon

On Linux hosts with systemd, CvkeHarness can install a lightweight service unit
for the internal scheduler daemon:

```bash
cvkeharness daemon install
systemctl --user enable cvkeharness.service
systemctl --user start cvkeharness.service
```

The user service is written to
`~/.config/systemd/user/cvkeharness.service`. For always-on user services after
logout, either pass `--enable-linger` during install or run the printed
`loginctl` command yourself.

System services are explicit and require a target user:

```bash
sudo cvkeharness daemon install --system --user appuser
sudo systemctl enable cvkeharness.service
sudo systemctl start cvkeharness.service
```

The service runs with the target user's home and configuration. Complete setup
for that user and ensure the installed executable is accessible to it. The
install command also requires usable configuration for the invoking account;
`sudo` may select root's home, so do not assume it reads `appuser`'s configuration.

The daemon uses SQLite claims with a five-minute lease and one-minute heartbeat
renewal by default. An active claim excludes other workers; expired claims can
be reclaimed after interruption. This is not an exactly-once guarantee for
external effects: a crash after an action but before recording completion, or a
lost lease while work continues, can lead to repeated work. Use idempotent job
prompts and inspect uncertain outcomes before retrying.

The CLI also provides `daemon start`, `stop`, `restart`, `status`, and
`uninstall`; pass `--system --user appuser` when managing a system service.
Install writes the unit and reloads systemd without enabling or starting it.
`jobs health` reports per-job telemetry, including blocked/overdue state,
claims, stale claims, and heartbeat freshness; it is not a systemd status check.

## System Crontab Management

The `crontab` executable must be available on `PATH`. Native Windows does not
provide it by default; this adapter does not manage Windows Task Scheduler.

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
confirmation before installation. System cron uses the host cron daemon's
timing/environment rules, not the internal scheduler's UTC calculation.
Installed commands later run under cron without the agent runtime, completion
verifier, or shell-approval loop. Entry creation validates syntax, not the
safety of the future command; review the command and its execution context.

## Agent Tools

Both tools are registered when SQLite state is available. Their advertised
schemas are filtered by task, and registry policy checks scheduled mutations
before execution. Direct CLI job management is an operator action.

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
confirmation. Registry approval does not replace the tool's second confirmation
of the diff prepared from the current crontab. That interactive step also remains
in permissive profiles, so use an interactive operator workflow for cron writes.

## Persistence And Audit

SQLite stores:

- scheduled job definitions
- scheduled job run history
- claim ownership, expiry, heartbeats, and blocked-work references
- scheduler telemetry used by `jobs health` and Console
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

Current safety choices:

- Internal scheduler is the default.
- System cron is current-user only.
- System cron writes always require confirmation.
- Crontab commands are not executed when entries are created.
- Crontab commands are validated as single-line cron entries.
- Newlines, carriage returns, and NUL bytes are rejected in cron commands.
- Existing crontab content is preserved unless explicitly targeted.
- Tests use fake crontab runners instead of touching the host crontab.

This keeps the feature useful for DevOps work while avoiding the largest
foot-guns: silent OS scheduler mutation, another user's cron edits, and broad platform
automation before the safety story is ready.

## Verification

The feature includes tests for:

- `at`, `every`, and five-field cron next-run calculation
- internal job creation, pause, resume, due execution, success, and failure
- scheduled run history
- claim exclusion/expiry, heartbeat renewal, and blocked-job handling
- crontab parsing with comments, env vars, disabled entries, managed entries,
  and unmanaged entries
- crontab add, update, disable, and remove through a fake runner
- malformed cron schedules and multiline command rejection

Run the local suite with:

```bash
go test ./...
```

## Current limits and future work

- Claims and telemetry are implemented; crash-safe, exactly-once external effects are not.
- Retry/backoff, per-job runtime limits, replay of every missed occurrence, configurable concurrency, and jitter remain future work.
- Per-job timezones, model/routing/token/tool overrides, labels, and deletion after a one-shot run are not implemented.
- Use the existing `jobs health` and `jobs runs` commands; `jobs status`, `jobs logs`, JSON output flags, and failure notifications are not implemented.
- Crontab backup/restore, import/sync, and privileged root/system cron adapters remain future work. Running the current-user adapter as root targets root's own crontab; it does not provide a separate system-cron backend.
- Linux systemd service support runs the internal scheduler. Systemd timers and macOS launchd adapters are not implemented.
