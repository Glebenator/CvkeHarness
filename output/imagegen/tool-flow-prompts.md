# Tool flow image concepts

Generated with the built-in Imagegen tool. These are synthetic design concepts, not screenshots of the current console.

## A / Individual steps

Use case: ui-mockup.
Create one high-quality design exploration image of a plausible keyboard-driven terminal UI for CvkeHarness, a local operations assistant. Single straight-on flat landscape frame around 1600x1100, no perspective or hardware. Crisp readable native monospace type. Warm charcoal canvas, stone body text, muted warm gray dividers, sage success, restrained amber focus. Calm and economical. Generous readable text, no giant typography. A small editorial direction label sits above the terminal window. Top terminal chrome exactly "CvkeHarness   Chat". Context beneath exactly "staging-api · read only". Conversation proceeds top-to-bottom, with two turns, all fitting legibly. Native TUI typography, simple lines, no web dashboard, decorative card grid, gradients, glow, shadows, neon, large icons or browser address bar. Persistent message composer at bottom with "Ask CvkeHarness…". No historical tool selector pinned to the composer. All calls visibly belong to their originating prompt; completed calls scroll with the conversation. This is a design concept, not a photo. Carefully typeset exact supplied text with sharp legible letters.
Direction label above window exactly "A / Individual steps in the conversation".
This concept exposes tool calls directly in one chronological conversation column. NO right side pane and NO aggregate '2 tools' summaries.
Turn 1: label "YOU", prompt "Check API health". Immediately below, individual call row "▾ ✓ curl /health   0.4s". This first call is visibly expanded inline with a thin neutral full box, short output exactly "HTTP 200 OK", "{ status: healthy }", "Exit code: 0". Below that, second collapsed individual call row "▸ ✓ systemctl status api   1.2s". Then label "CVKEHARNESS" and answer "The API is healthy. All three instances are serving requests."
Leave a clear gap then turn 2: label "YOU", prompt "What about the worker?". Individual collapsed call "▸ ✓ workerctl status   0.6s". Then label "CVKEHARNESS" and answer "The worker is running. The queue is empty."
Keep all these activities inside the transcript and earlier calls ABOVE the second prompt. Composer below both turns. Footer "↑↓ scroll    Enter details    Esc back".
Small neutral editorial subtitle below the window exactly "Every action stays visible in its place." Emphasize sequential individual steps, compact tool rows, transparent execution. One window only; no split comparisons or extra diagrams.

## B / Turn summaries

Use case: ui-mockup.
Create one high-quality design exploration image of a plausible keyboard-driven terminal UI for CvkeHarness, a local operations assistant. Single straight-on flat landscape frame around 1600x1100, no perspective or hardware. Crisp readable native monospace type. Warm charcoal canvas, stone body text, muted warm gray dividers, sage success, restrained amber focus. Calm and economical. Generous readable text, no giant typography. A small editorial direction label sits above the terminal window. Top terminal chrome exactly "CvkeHarness   Chat". Context beneath exactly "staging-api · read only". Conversation proceeds top-to-bottom, with two turns, all fitting legibly. Native TUI typography, simple lines, no web dashboard, decorative card grid, gradients, glow, shadows, neon, large icons or browser address bar. Persistent message composer at bottom with "Ask CvkeHarness…". No historical tool selector pinned to the composer. All calls visibly belong to their originating prompt; completed calls scroll with the conversation. This is a design concept, not a photo. Carefully typeset exact supplied text with sharp legible letters.
Direction label above window exactly "B / One activity summary per turn".
This concept is a very calm SINGLE conversation column. NO right activity pane. Tools are aggregated into a compact summary under each user prompt.
Turn 1: label "YOU", prompt "Check API health". Directly under it one COLLAPSED summary "▸ 2 tools · verified · 1.6s". Individual tool commands for turn 1 are hidden. Then label "CVKEHARNESS" and answer "The API is healthy. All three instances are serving requests."
Clear gap then turn 2: label "YOU", prompt "What about the worker?". An EXPANDED activity summary "▾ 1 tool · verified · 0.6s". Just under this summary, one slightly indented selected command row "✓ workerctl status   0.6s" and a quiet action hint "Enter: inspect output". Use restrained amber only on the selected row's pointer or label. Do NOT dump raw output into the conversation. Then label "CVKEHARNESS" and answer "The worker is running. The queue is empty."
Earlier completed summary stays above the second user prompt. Persistent composer at bottom. Footer "↑↓ scroll    Ctrl+T inspect    Esc back".
Small neutral editorial subtitle below the window exactly "Quiet summaries, detailed output on demand." This design should clearly demonstrate that per-turn activity can be opened to choose a call, with full output accessed through an inspector rather than inflating the conversation. Keep main view single-window, no pop-up or inspector overlay in this image. Very economical vertical density, clear text hierarchy, readable grouping.

## C / Separate activity workspace

Exact C generation prompt is saved alongside this file in tool-flow-c-prompt.txt.

