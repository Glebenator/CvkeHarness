# Chat and activity

Chat keeps the conversation in chronological order. Every prompt has an activity
link for its own turn, including turns that stop without an assistant reply.
Tool commands, output, durations, errors, and detailed verification live in
Activity.

At 120 columns and wider, Activity occupies the right side of Chat. At narrower
widths, opening Activity switches to a full-width view; returning restores the
conversation and any composer draft.

## Reading and inspecting

Activity initially **follows the latest turn**. A new turn with no tool calls
shows that empty state instead of the previous turn's tools. Opening a turn,
selecting a tool, or manually scrolling pins the displayed turn. The header names
the turn and explicitly distinguishes **PINNED** from **FOLLOWING LATEST**.

The conversation and Activity have independent scroll positions. Arrows, page
keys, and the mouse wheel scroll without changing tool selection. Wheel input
affects the pane under the pointer. Space never opens tool details; in the
composer it inserts a space normally.

| Action | Keyboard / mouse |
| --- | --- |
| Open activity for the turn being read | Ctrl+T, or click its activity link |
| Return from Activity to conversation | Ctrl+T |
| Open selected tool output | Enter, or click a tool row |
| Back from output to tools; then back to chat | Esc |
| Select previous / next tool in Activity | P / N |
| Inspect previous / next turn | [ / ] |
| Scroll the focused pane | Up / Down, Page Up / Page Down, Home / End |
| Scroll a pane without changing keyboard focus | Mouse wheel over that pane |
| Follow the latest turn in Activity | F, or click Follow latest |
| Jump conversation to latest and resume following Activity | Ctrl+End |
| Focus the composer from the conversation | Enter, or click the composer |
| Toggle composing / reading without leaving Chat | Tab / Shift+Tab |
| Focus top bar, select workspace, focus its content | Esc, Left/Right, Enter |

Reading earlier conversation content pauses automatic following. Runtime events
and turn completion preserve that position. Output inspection also preserves
its position as output arrives or a tool completes. Completing a tool does not
close its inspector.

## Approval and context

Pending approval keeps its exact action, policy reason, and approval controls in
the conversation. Activity can be inspected without interrupting the pending
work: Esc first returns through the inspection views. Ctrl+X interrupts the
active turn while Chat is focused. Esc closes Activity or command suggestions
first, then focuses the top bar without interrupting work. A successful approval
receipt stays visible in the header until the next turn.

Ctrl+G continues to expose full session context; Ctrl+H opens saved conversation
history. Tab and Shift+Tab toggle composing and reading while idle in Chat;
workspace selection is available after Esc focuses the top bar.

Approval review opens a centered dialog with the exact command first, under **COMMAND TO APPROVE**, in bold on a highlighted background. Advisor explanations appear underneath in regular-weight text. The complete command wraps without abbreviation; long reviews scroll with arrows, Page Up/Down, Home/End, or the mouse wheel. Secrets remain masked. `a` approves the exact action once; Esc returns without approving, and `a` then reopens review. `d` reveals policy reasons and effects. Historical Activity keeps its own focus and cannot approve a hidden current request.

The passive right sidebar is a compact task-progress summary, capped at 42 columns. Ctrl+T opens the detailed activity evidence.
