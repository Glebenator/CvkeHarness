# CvkeHarness UX review — September 4, 2026

The highest-value improvements are reliable navigation during background work, preserving drafts through errors, and keeping critical information accessible in small terminals. The existing run/console separation and explicit approval language are worth retaining.

## Scope and evidence

Reviewed checkout `f5b4c8f`, particularly `internal/tui`, `internal/setuptui`, `internal/cli`, and their command entry points. Existing README changes and untracked documentation were preserved.

Native Computer Use was attempted through `cua.getApp("Terminal")`. It returned: `Computer Use is not allowed to use the app 'com.apple.Terminal' for safety reasons.` No native window screenshots were captured. This is a source and interactive PTY review, not a completed visual/computer-use audit.

The PTY sessions ran the current, unmodified console package through a temporary launcher with an isolated SQLite database. This bypasses the normal CLI configuration loader. Chat startup used the same missing-API-key error text as the command entry point; the last journey delayed that simulated error by five seconds. Populated history and the API key were synthetic fixtures. No provider calls, remote operations, scheduler daemon, real credentials, or settings saves were used. Native font rendering, theme contrast, mouse interaction, screen-reader support, live streaming, and approval execution remain unverified.

The three `.ansi.txt` files beside this report contain actual PTY output, including terminal control sequences and incremental screen updates. They are not screenshots or independently rendered screen reconstructions. `audit-launcher.go.txt` preserves the final fixture launcher; the initial journey omitted the seeded run and dummy key and returned the startup error immediately.

## Interaction steps

| Step | Journey | Health and evidence |
|---|---|---|
| 1 | Open Overview at 80×24 using first-run settings | Needs improvement: no setup call to action; Jobs count falls beyond the available width. Log 01. |
| 2 | Open `?` help, then press Down | Broken at 80×24: later shortcuts are clipped; Down dismisses help rather than scrolling. Log 01. |
| 3 | Jobs → New → enter a name → Every interval → enter `tomorrow` → enter prompt → confirm | Broken recovery: invalid schedule advances through review; final failure returns to the list. Log 01. |
| 4 | Reopen New Job after that failure | Broken recovery: name and other inputs reset. No Back action exists for revising earlier wizard steps. Log 01 and source. |
| 5 | Chat → compose → send with simulated missing credentials | Broken recovery: prompt disappears, error recovery text is truncated, and the empty conversation still says “Ready for a task.” Log 01. |
| 6 | Settings → edit Default Model → apply → `q` | Broken recovery: exits immediately with unsaved changes. No settings were saved. Log 01. |
| 7 | Runs → open seeded long answer → press Down | Needs improvement: task and output are truncated; scrolling cannot expose the omitted answer text. Log 02. |
| 8 | Settings → Provider API Key → edit | Privacy issue: masked list value becomes fully visible in the editor. Tested only with a dummy value. Log 02. |
| 9 | Settings → Security at 80×24 | Needs improvement: Save and Back hints fall off the footer; reset actions remain visible. Log 02. |
| 10 | At 120×40, submit Chat prompt, Tab to Settings before delayed error, return after completion | Broken: Chat remains CONNECTING after the simulated startup operation has finished. Log 03. |

## Prioritized changes

### 1. Route background results to the workspace that owns them — high priority

`internal/tui/app.go:227` forwards non-global messages only to the active tab. A `chatSessionReadyMsg` received while Settings is active is ignored there, so Chat's `starting` state never clears. The live PTY journey reproduced this with a delayed startup error. The same routing structure exposes other asynchronous result types to this risk, although each other case was not independently exercised.

Route chat events to Chat regardless of the selected tab; do the equivalent for job actions and settings saves. Keep event subscriptions active while a workspace is hidden. Add a small tab indicator for running, completed, and approval-needed work. Acceptance: start a delayed connection or turn, visit every other tab, and return to the correct terminal state without losing results or pending approvals.

### 2. Preserve drafts and offer recovery where the error occurs — high priority

Job schedule validation currently checks only for nonempty input at the Schedule step (`internal/tui/tab_jobs.go:344`). The wizard returns to the list before creation succeeds (`:379`), and reopening it initializes fresh inputs. Validate through the scheduler's parser before advancing; retain the form on persistence failure; support Back and editing individual review fields. Show the next three execution times with timezone. A short multiline prompt editor would be easier to review than the current single-line, 500-character input.

Chat resets its composer before starting the session (`internal/tui/tab_chat.go:553`) and clears the pending prompt on startup error (`:315`). Preserve the original draft and provide Retry, Edit message, and Open Settings actions. Show the full recovery explanation in a wrapping panel. Acceptance: a provider outage or invalid credential never forces the user to reconstruct a prompt.

### 3. Keep credentials masked during editing — high priority

The Settings list masks API keys, but `internal/tui/tab_config.go:317` creates a normal text input populated with the full value. The dummy-key journey confirms the reveal. Use a password input and an explicit, temporary Reveal action, or an empty replacement field with “Key already configured.” Never require exposing the old key just to replace it.

### 4. Make 80×24 a complete interface — high priority

The root view clamps lines (`internal/tui/app.go:251`) while Help has no viewport and dismisses on every key (`:168`). Overview and Runs also build rows wider than a small terminal. In Security, footer budgeting keeps reset actions while dropping Save and Back (`internal/tui/tab_config.go:90`, `internal/tui/app.go:328`).

Use a scrollable help viewport with Esc to close. Make footer priorities task-specific: Save/Back or Send/Cancel first, destructive/reset actions later. At narrow widths, show fewer history columns or stacked rows; keep task and state before model and token counts. Stack Overview metrics. Acceptance: every action and item remains reachable at 80×24, including long provider/model names and errors.

### 5. Keep operational status visible at every width — high priority

At 80 columns, the Chat context sidebar is absent and the header truncates the security profile to `reas…`. At 120 columns the sidebar shows connection, target, verification, and tool state. That information should not depend on a wide window (`internal/tui/tab_chat.go:763`, `:913`, `:1803`).

Provide a compact persistent status strip: current activity, resolved target/environment, security profile, and verification state. Wrap to two lines when necessary. Expand it on demand for detail. Replace “Ready for a task” while connecting or unavailable. Preserve the distinction between execution success and verified success.

### 6. Make history useful for retrieving outcomes — medium priority

The Runs table gives a 15-character minimum to the task while reserving 25 characters for the model. Long output is truncated per line rather than wrapped (`internal/tui/tab_runs.go:335`), and raw Markdown headings remain visible. There is no horizontal reveal in this view. Final output also appears after the diagnostic sections.

Lead run detail with outcome, verification, target, and the complete answer. Reuse the Chat Markdown renderer and put phases/tool traces in expandable detail. Offer full-text search, status/target filters, and export. Acceptance: the fixture's `END-OF-RESULT` is reachable at 80 columns, and two similarly named tasks can be distinguished without guessing.

### 7. Protect pending settings and clarify apply versus save — medium priority

The Settings editor says Enter “applies,” but another Save action is required for persistence. `q` exits with dirty edits and Ctrl+C exits globally (`internal/tui/app.go:163`). Draft settings survive normal tab switching, which is good.

Use “Keep edit” for the local editor action and “Save settings” for persistence. Show a Settings dirty badge outside the tab. On ordinary quit with pending changes, offer Save, Discard, and Continue editing. Explain which settings take effect for new sessions and show an explicit way to start a new session with them. Preserve a force-exit path for emergencies.

### 8. Make Overview answer “What needs my attention?” — medium priority

The current first-run Overview emphasizes provider, model, aggregate counts, and empty feeds. Setup mode is marked in the command entry point but not surfaced by Overview. The empty state says to run a task without a direct action.

For a new profile, show three actionable readiness items: connect provider, verify configuration, start first task. For returning operators, lead with approvals waiting, failed or interrupted work, scheduler health, and next scheduled executions. Give each item an action that opens the relevant workspace. Keep configuration and aggregate statistics secondary.

### 9. Label metrics honestly and distinguish failed loads from empty data — medium priority, source finding

`loadOverviewData` loads only 100 runs for Runs/Success Rate and five chat sessions for Chat Sessions (`internal/tui/app.go:442`), but the displayed labels do not identify those windows. The Runs list loads 50 records with no paging controls. Overview, Jobs, and Runs discard fetch errors in these loader functions; a failed read can look like an empty state.

Use database aggregate queries for totals or label windows explicitly (“Last 100 runs”). Add paging/search for history. Represent loading, empty, stale, and failed states separately, with a retry action and last successful refresh time. These observations are source-backed; large histories and database failures were not injected in the PTY journeys.

## Design direction worth preserving

Keep bounded `run` and persistent `console` as distinct entry points. Keep tab navigation available from text fields, but retain work state as people move. Keep explicit policy reasons and one-use approval language; `appendApprovalPrompt` already names the action, effects, and expiry. Do not replace these with a vague “Allow?” prompt.

Organize the experience around a consistent sequence: choose/confirm target → describe outcome → observe execution → resolve any approval → read verified result → revisit history. The biggest information-architecture gap is connecting these steps: Overview is mostly passive, errors are not actionable, and results do not consistently survive navigation or fit the terminal.

## Accessibility and remaining visual work

Keyboard access exists, but undiscoverable clipped shortcuts, context-dependent keys, and lost drafts undermine it. Provide complete keyboard help and explicit focus/mode labels. Fixed foreground colors in `internal/tui/styles.go` need actual checks on both light and dark terminal backgrounds; screenshots were unavailable, so no contrast ratio or accessibility compliance claim is made here. Test screen readers and a plain transcript mode separately. Mouse support and native text selection were not evaluated.

A follow-up native review should capture the same journeys at 80×24, 100×30, and 120×40, in light and dark terminal themes, with long content and real streaming/approval states. The Terminal computer-use restriction must be resolved before that portion can be completed.

## Suggested implementation sequence

1. Background event delivery, credential masking, prompt retention, and wizard error recovery.
2. Narrow-terminal layouts, help scrolling, complete run output, and persistent task status.
3. Actionable onboarding/Overview, settings safeguards, trustworthy metrics, and searchable history.
4. Native visual, mouse, light/dark theme, and assistive-technology verification.

No application implementation changes were made. The fixture console builds succeeded; the Go tool emitted a module-cache write warning under the sandbox. This was a targeted interactive review, not a full test-suite run or live-provider validation.
