# CvkeHarness Operations Console Design System

## Physical scene and theme

An operator works in a terminal during focused daytime configuration and late incident response, sometimes at only 80 columns. A warm charcoal dark theme reduces glare while remaining quieter and more approachable than a conventional black-and-neon terminal.

## Color strategy

Restrained, tinted neutrals with semantic accents used only for focus and state.

- Canvas: warm near-charcoal, never pure black.
- Raised/selected surface: slightly lighter warm charcoal.
- Primary text: stone.
- Muted text and dividers: warm gray with strong contrast.
- Focus and primary action: restrained amber.
- Success and verified: sage.
- Warning and error: terracotta, with explicit labels/icons.

Terminal rendering must remain understandable when colors are unavailable.

## Typography

Use the terminal's native monospace. Establish hierarchy through weight, spacing, concise uppercase eyebrow labels, and restrained color. Body copy should wrap at roughly 65 to 72 characters where space permits.

## Layout

- Shared top identity line, stage/context line, working surface, and keyboard footer.
- Setup has four grouped stages: Connect, Safety, Capabilities, Ready.
- At 80 columns use one column, compact spacing, wrapped row descriptions, and no horizontal clipping.
- At 100 columns provide wider explanations and calmer spacing.
- At 120 columns the Chat workspace may show a secondary context pane.
- Avoid containers unless they establish an actual interaction boundary, such as the composer or expanded tool output.

## Components

- Selection row: pointer plus label plus concise consequence; background and color are secondary cues.
- Status: icon plus explicit text (`RUNNING`, `VERIFIED`, `FAILED`, `APPROVAL REQUIRED`).
- Setup action: consistent Back and Continue labels, with blocked reasons stated inline.
- Composer: persistent multiline input with send/newline hints.
- Chat command palette: compact inline suggestions above the composer when input starts with `/`; filtered as the operator types, with a responsive row cap so the composer remains visible.
- Tool call: compact inline summary, collapsible details, duration and outcome text.
- Empty/loading/error states: explain what happened and the next available keyboard action.

## Interaction

- Keyboard-only operation is complete, not a fallback.
- Enter activates or sends; arrows and familiar Vim keys navigate; Esc goes back or cancels; explicit hints remain visible.
- In the chat command palette, arrows change the selected command, Enter completes a partial command or runs an exact one, and Esc closes suggestions without clearing the composer.
- Asynchronous runtime work crosses Bubble Tea boundaries through typed messages and commands.
- Do not imply token streaming when the provider/runtime only returns complete turns.
- Saving configuration is a separate action from applying install or daemon changes.

## Console refinement, September 2026

The console respects the host terminal's background and uses adaptive foreground and selected-surface colors. Light terminals receive dark stone text and darker semantic accents; dark terminals receive brighter stone text. The representative review canvases are warm charcoal and warm off-white, not forced terminal backgrounds.

Page titles use bright neutral text. Reserve amber for actions and focus. Every selected row and active tab has a pointer in addition to its color treatment. Lists use a task-first line and a secondary status/context line; Overview groups attention, scheduling, and completed outcomes. Settings separates values from the selected field's explanation. Inputs have a consistent rounded boundary, with active focus emphasized.

The review artifact is `docs/console-design-review.html`; implementation and verification notes are in `docs/console-visual-design.md`.

Navigation uses Ctrl+O for a global workspace/action switcher, Alt+Left/Right for visited workspaces, and optional tab clicks. Opening an overlay preserves the underlying editor; Esc cancels the overlay. Motion is confined to small activity indicators while work is in progress. Approval waits and idle states are static. Operators can disable animation for the session or set `CVKE_REDUCED_MOTION=1` at launch.
