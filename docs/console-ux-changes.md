# Console UX improvements

Implemented September 4, 2026 following the interactive console review.

## Behavior changes

- Asynchronous chat, job, settings, and history results reach their owning workspace while another tab is open. Tabs signal new results, active chat work, waiting approvals, and unsaved settings.
- Chat startup failures preserve the full draft, show the complete recovery message, and expose Settings and retry actions.
- Chat shows activity, verification, resolved target/environment, and session security at compact widths. Ctrl+G expands session context. Target resolution is delivered through correlated runtime events, not inferred from display text. Unresolved targets remain explicitly unresolved.
- Saved configuration labels do not overwrite the policy label of an already-running chat session. Use `/new` in Chat to start with saved settings.
- API keys stay masked while editing. Enter keeps an edit; saving settings is a separate action. Ordinary quit offers Save, Discard, or Continue editing for pending settings. Ctrl+C remains force quit.
- Help scrolls with arrows, Page Up/Down, Home and End; Esc closes it. Footers prioritize navigation and local completion/recovery controls, with help discoverable outside text input.
- Job schedules are validated at the Schedule step with the scheduler's own parser. Past one-time schedules are rejected. Review shows the next three executions in UTC, or the sole future execution for one-time jobs.
- Job prompts support multiple lines with Ctrl+J. Ctrl+B revisits earlier steps. Esc closes a draft; `n` reopens it. A failed save retains the complete form, and an in-flight save cannot be submitted twice.
- History searches all persisted runs, including task, answer, error, and tool-command text. `f` filters success/failure; `t` cycles recorded targets and unknown; brackets page through results. Refreshes do not change an open run detail to a different record.
- Run detail wraps the complete answer and renders Markdown. Diagnostics are optional with `d`; `e` exports the stored answer and tool details to a private, redacted file beside the state database.
- Overview surfaces pending approvals, recent failed work, scheduler problems, upcoming jobs, and completed outcomes. Enter opens the selected run/job. Persisted approvals retain their existing approval boundary; opening Overview never grants authority.
- First use offers Settings, a local configuration check, and Chat. Aggregate counts cover the whole persisted history rather than a recent sample. Database read errors are distinct from empty states and expose retry instructions.
- Jobs and Runs use compact rows that prioritize task/name and state. Settings keeps the selected field description visible below its list.

## Keyboard reference

| Workspace | Keys |
|---|---|
| Global | Tab / Shift+Tab switch; arrows switch outside editing; 1–5 jump; `?` help; `q` quit with settings check; Ctrl+C force quit |
| Overview | Enter open; `c` Chat; `s` Settings; `v` local config check; `r` retry |
| Jobs | `n` create/reopen draft; Enter details; `r` run; `p` pause/resume; `x` delete; Ctrl+R retry load |
| Job draft | Enter continue/create; Ctrl+B back; Ctrl+J newline in prompt; Esc close draft; arrows scroll review |
| Runs | `/` search; `f` status; `t` target; `[` / `]` pages; `r` retry; Enter open |
| Run detail | `d` diagnostics; `e` export; arrows / PgUp / PgDn / Home / End scroll; Esc back |
| Chat | Enter compose/send; Ctrl+J newline; Ctrl+G context; Ctrl+H history; Esc leave composer/interrupt; `/new` fresh session |
| Settings | Enter edit; Enter keep edit; `s` save from list; Esc cancel editor/back; `r` reset pending edits |

## Validation

- Full Go suite: `go test ./...` passed.
- Executable journeys: `go test -tags=e2e ./e2e -count=1` passed with isolated profiles and local mock providers.
- Race detection passed for `internal/tui`, `state`, `agent`, and `internal/chatexport`.
- `go vet ./...` passed.
- Interactive PTY checks exercised 80×24, 100×30, and 120×40 layouts with the existing console package, a temporary database, dummy credentials, seeded history, and a delayed simulated connection error.
- The PTY checks confirmed the background-completion fix, preserved message, masked editor, schedule validation, multiline prompt, Back/review flow, local job creation, complete answer tail, private export, help scrolling, Security Save/Back hints, context disclosure, unsaved-settings quit flow, and cycling between all/unknown/recorded targets.
- Regression tests cover record paging beyond the initial page, totals beyond the former 100-run/five-session limits, database failures, private export, input retention, footer fitting, short wide-terminal layout, target migration, exact target filtering, and stale filter responses.

The sandbox initially prevented tests from opening local HTTP fixture ports. They passed with loopback access. No remote operation, real-provider task, saved user-configuration change, or deployment was performed during manual verification.

## Limits

Native Terminal computer-use access was denied, so this is PTY and automated validation. Native font/contrast, mouse selection, light/dark terminal themes, and assistive technology still require a native review. Existing automated mouse-wheel coverage remains passing.

New runs store the last resolved target, environment, and ambiguity flag. History filters exact recorded identities; this is not a complete inventory of every target touched by a multi-target run. Older records remain unknown rather than guessing a target from task wording. Live Chat uses resolved target events. Configuration checks on Overview are local, not a provider connectivity test.
