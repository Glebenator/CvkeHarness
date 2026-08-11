# CvkeHarness Guided Console Design System

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
- At 120 columns chat may show a secondary context pane.
- Avoid containers unless they establish an actual interaction boundary, such as the composer or expanded tool output.

## Components

- Selection row: pointer plus label plus concise consequence; background and color are secondary cues.
- Status: icon plus explicit text (`RUNNING`, `VERIFIED`, `FAILED`, `APPROVAL REQUIRED`).
- Setup action: consistent Back and Continue labels, with blocked reasons stated inline.
- Composer: persistent multiline input with send/newline hints.
- Tool call: compact inline summary, collapsible details, duration and outcome text.
- Empty/loading/error states: explain what happened and the next available keyboard action.

## Interaction

- Keyboard-only operation is complete, not a fallback.
- Enter activates or sends; arrows and familiar Vim keys navigate; Esc goes back or cancels; explicit hints remain visible.
- Asynchronous runtime work crosses Bubble Tea boundaries through typed messages and commands.
- Do not imply token streaming when the provider/runtime only returns complete turns.
- Saving configuration is a separate action from applying install or daemon changes.
