# End-to-end user journeys

This suite runs the compiled `cvkeharness` executable as a user would. It is
kept behind the `e2e` build tag because it builds the binary, opens real
pseudo-terminals, executes one allowlisted `echo` command, and writes isolated
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
| Guided setup | Keyboard navigation reaches provider selection at 80, 100, and 120 columns | Quitting early saves nothing |
| Local Console chat commands | Help, memory, tools, unknown-command handling, and exit remain usable | Zero model requests; zero turns persisted |
| Tool-backed Console chat | A model-requested allowlisted command shows output and a verified final response | Tool outcome and turn are persisted |
| Manual approval continuation | The inline policy reason and exact action are shown; `a` creates one scoped grant and continues the same turn | The exact grant is atomically consumed; no legacy reusable approval is persisted |
| Unapproved interruption | Leaving an approval ungranted and interrupting the turn never runs the proposed action | A filesystem marker is not created; blocked work and the interrupted outcome remain inspectable |
| Chat export | `/export` produces a readable transcript | Export file is mode `0600` |
| Approval management | `commands approve` is visible in `commands list` | Approval survives a second process |

## Isolation and safety

- Every test gets a temporary `HOME`; the real `~/.cvkeharness` is never read or written.
- Model calls go only to an in-process LM Studio-compatible HTTP stub.
- The only shell action is the allowlisted command `echo E2E_TOOL_OK`.
- The rejection journey requests `touch` but rejects it and asserts that its
  marker file was never created.
- The suite does not require provider credentials or public network access.
- Pseudo-terminal coverage currently targets macOS and Linux; the file is
  excluded on Windows.

## Deliberate baseline limits

The setup PTY coverage stops at provider selection; it does not yet exercise
credential validation or review/save. The executable suite drives the Bubble
Tea Console Chat at a representative 100-column viewport; broader focus,
scrolling, cancellation, approval, and tool-row layout cases remain covered by
package tests at 80, 100, and 120 columns. The model boundary is hermetic, so
these tests are not evidence of live-provider authentication or availability.
