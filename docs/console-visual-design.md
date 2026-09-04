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
