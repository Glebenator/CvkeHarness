# End-to-end user journeys

This suite runs the compiled `cvkeharness` executable as a user would. It is
kept behind the `e2e` build tag because it builds the binary, opens real
pseudo-terminals, executes harmless allowlisted `echo` commands, and writes isolated
SQLite/config/export artifacts.

Run it from the repository root:

```bash
./scripts/test-e2e.sh
```

Or invoke Go directly:

```bash
go test -tags=e2e ./e2e -v
```

## Covered journeys

| Journey | User-visible contract | Side-effect assertion |
| --- | --- | --- |
| Command discovery | Root help exposes setup, bounded run, Console, and approvals | None |
| First-run failure | An unconfigured task tells the user to run setup | No config is created |
| Guided setup | Keyboard navigation reaches the shared named connection editor and explicit provider chooser at 80, 100, and 120 columns | Quitting early saves nothing |
| Cached Codex onboarding | Setup creates a named connection and saves Primary and Safety judge through the same searchable picker at 80, 100, and 120 columns | Synthetic cached auth/model fixtures only; saved bindings resolve the exact model IDs without provider requests |
| Configuration-only Settings | Both `settings` and `console --view settings` open the same Models workspace before a provider is configured; Connections and Add remain reachable | Opening and quitting creates neither a config nor a runtime database |
| Local Console chat commands | Help, memory, tools, unknown-command handling, and exit remain usable | Zero model requests; zero turns persisted |
| Tool-backed Console chat | A model-requested allowlisted command shows output and a verified final response | Tool outcome and turn are persisted |
| Activity inspection | At 80, 100, 120, and 144 columns, `Ctrl+T` opens Activity, `Enter` opens output, and two `Esc` presses return to composing | Inspection and local `/help` cause no additional model requests |
| Tool-free follow-up | After a long tool-backed response and a short direct reply, `Ctrl+T` at Latest opens the newest turn with no tool calls at 80 and 100 columns | Earlier tool evidence remains separate from the new turn |
| Manual approval continuation | The inline policy reason and exact action are shown; `a` creates one scoped grant and continues the same turn | The exact grant is atomically consumed; no legacy reusable approval is persisted |
| Unapproved interruption | Leaving an approval ungranted and interrupting the turn never runs the proposed action; Activity exposes the cancellation reason | A filesystem marker is not created; blocked work and the interrupted outcome remain inspectable |
| Chat export | `/export` produces a readable transcript | Export file is mode `0600` |
| Approval management | `commands approve` is visible in `commands list` | Approval survives a second process |
| Recovery approval and offline restore | The Console applies a prepared recovery operation after explicit approval, then the CLI restores it with the model server stopped | Only a temporary fixture is changed and its original contents are restored |

## Isolation and safety

- Every test gets a temporary `HOME`; the real `~/.cvkeharness` is never read or written.
- Model calls go only to an in-process LM Studio-compatible HTTP stub.
- Executed shell actions are `echo E2E_TOOL_OK` and `echo E2E_ACTIVITY_EVIDENCE`.
- Approval management records `echo E2E_APPROVED` without executing it.
- The rejection journey requests `touch` but rejects it and asserts that its
  marker file was never created.
- The suite does not require provider credentials or public network access.
- Pseudo-terminal coverage currently targets macOS and Linux; the file is
  excluded on Windows.

## Deliberate baseline limits

Console Activity journeys run through the real executable and PTY at 80, 100,
120, and 144 columns, covering both the full-width Activity view and split panes.
Package tests cover detailed focus, scrolling, mouse geometry, cancellation,
approval, and output layout behavior. Setup coverage includes both early exit
and review/save with synthetic cached Codex fixtures. The separate
[Settings frame validation](../output/settings-validation/README.md) exercises
cross-connection role selection, staged changes, custom IDs, provider protection,
catalog failure/reload, and setup review at 80×24 and 120×32.

The model boundary is hermetic. These checks do not establish live-provider
authentication, availability, or model behavior. A PTY also does not reproduce
every native terminal emulator's scrollback, trackpad gestures, font rendering,
or key interception. Docker and VM recovery suites require their separate
explicit build tags and are not part of this local `e2e` run.
