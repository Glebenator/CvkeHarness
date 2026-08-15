# CvkeHarness Product Context

register: product

## Product purpose

CvkeHarness is a local-first Go operations agent with two deliberate interaction contracts. `run` executes one bounded task and exits. The operations console supports ongoing conversation and control while keeping the active target, model, tools, approval posture, and verification state legible.

## Users

Technical operators configuring and using a DevOps operations agent. They are often focused, cautious, keyboard-driven, and responsible for systems where ambiguous targets or hidden side effects are unacceptable.

## Personality

Calm, precise, and trustworthy. Copy should explain consequences without alarmism. The interface should feel economical like Raycast, keyboard-clear like Lazygit, and as careful with explanatory copy as Stripe.

## Strategic principles

- Preserve existing safety, approval, routing, memory, state, and telemetry boundaries.
- Keep one-shot execution distinct from the stateful operations console.
- Make target and execution context persistently visible.
- Distinguish saving configuration from installing dependencies or starting a daemon.
- Present one primary setup decision at a time with safe recommended defaults.
- Reveal advanced settings progressively.
- Use text and icons alongside color for every meaningful status.
- Stay fully usable by keyboard at 80 columns, become roomier at 100 columns, and add optional context at 120 columns.

## Anti-references

Avoid cyberpunk or Matrix terminal styling, generic card dashboards, long step rails, tiny metadata, decorative panels, color-only status, mouse-dependent controls, gradients, glass, neon, nested cards, side-stripe accents, and decorative dashboard grids.

## Scope

The current product surface covers the four-stage setup experience, the operations console with integrated Chat, and the bounded `run` command. Settings is not fully redesigned here, but the foundations should support that later work.
