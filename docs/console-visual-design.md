# Console visual and interaction design

The September 4 visual pass builds on the earlier reliability work. The console now uses a clearer task hierarchy, grouped activity, consistent selection and input surfaces, and colors that adapt to a light or dark terminal background.

[Open the interactive before/after component review](console-design-review.html). It includes seven screens, three terminal sizes, and both themes. These are actual Go component renders with synthetic data, not a browser replacement for the console. The viewer changes rendered states; it cannot execute tasks.

## Changes

- Navigation has an explicit active pointer and a selected background, so the active workspace is recognizable without color.
- Headings use bright neutral text. Amber identifies focus and actions; semantic colors identify outcomes. Body and secondary text are brighter on dark terminals, with separate dark text colors for light backgrounds.
- Overview separates Needs attention, Scheduled next, and Recent results. The task is the primary line; status and the next action sit below it. Selection remains visible while moving through long activity lists.
- Runs and Jobs share two-line rows with a consistent selection area. Task names get the full primary row, while status, time, and verification become secondary information.
- Settings uses two readable columns instead of competing setting/value/notes columns. The selected field's explanation appears below the list. Empty values say Not set; toggles say On or Off. The edit surface is visually distinct from navigation.
- Job creation asks one clear question at each step. Schedule choices have separate explanations; focused fields and multiline prompts use the same input boundary. A preserved draft has one Continue action.
- Chat opens with a concrete task example, separates operational status from the conversation, and highlights the composer only when it has focus. Introductory guidance disappears once tool activity exists.
- Truncation now respects terminal cell widths and ANSI boundaries, preventing wide characters from overflowing or a selected-row style from leaking into the following row.

## Verification

- Inspected actual component renders through browser Computer Use, including Overview, Settings, Chat, job creation, Runs and run detail across light/dark and compact/wide states.
- The opt-in preview exporter checks every rendered row against its terminal width and checks screen height: 42 combinations across 80×24, 100×30, and 120×40.
- Interactive PTY checks exercised the real console at 80×24: navigation, job name entry, schedule selection, Back retaining the name, draft closure, Chat focus, and the slash palette. All data was isolated; no provider call or settings save was performed.
- The focused UI suite, full Go suite, executable E2E journeys, race checks for the UI, and vet passed.

Native Terminal access remained denied. Browser rendering does not establish native terminal font, theme-detection, mouse, or screen-reader behavior. The console respects the terminal background; the viewer's two backgrounds are representative examples, not forced application canvases.

## Refreshing component previews

The opt-in utility uses synthetic data and has no command-execution path:

```sh
CVKE_PREVIEW_OUTPUT=/tmp/console-preview.json go test -tags=uxpreview ./internal/tui -run TestExportConsolePreview -count=1
```

The standalone HTML review retains the before/after snapshots from this pass.

## Navigation and motion follow-up

- Ctrl+O opens a searchable workspace/action switcher from any editor. Esc closes it with the original draft and editing state intact. Workspace-name matches rank before description matches. Actions open workspaces, continue/create a draft, open history search, show help, or change session motion preferences; they do not submit work.
- Alt+Left and Alt+Right revisit workspace history. Visiting a new workspace after going back replaces the forward branch. History is capped at 32 entries.
- Tab labels accept mouse clicks. Clicking the current tab preserves input focus. The existing Tab/Shift+Tab and contextual arrows remain available. F1 opens help from input modes.
- Small 120 ms activity indicators accompany connecting, running, and saving. They stop when the activity completes or pauses for approval. Idle screens do not retain an animation timer, and stale queued frames cannot restart one.
- Search for motion in Ctrl+O to switch to static activity indicators for the session, or launch with `CVKE_REDUCED_MOTION=1`. Screens and transcript content do not slide, fade, or move during navigation.
- The review now includes Switcher and Connecting states. Play motion cycles the actual Go-rendered indicator frames without running an agent. The exporter checks 54 screen/theme/size combinations.

Tests cover editor preservation, stale switcher input, draft continuation, search ranking, workspace history, tab hitboxes, background results under the switcher, and animation shutdown/reduced motion. PTY verification exercised Ctrl+O, filtering, Chat opening, animated delayed startup, error recovery, and Alt+Left/Right. Mouse hitboxes were tested against rendered tab widths; native mouse operation remains outside the available access.
