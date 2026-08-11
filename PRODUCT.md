# CvkeHarness Product Context

register: product

## Product purpose

CvkeHarness is a local-first Go operations agent. The Guided Console should help an operator reach a safe working agent quickly, then converse with it while keeping the active target, model, tools, approval posture, and verification state legible.

## Users

Technical operators configuring and using a DevOps operations agent. They are often focused, cautious, keyboard-driven, and responsible for systems where ambiguous targets or hidden side effects are unacceptable.

## Personality

Calm, precise, and trustworthy. Copy should explain consequences without alarmism. The interface should feel economical like Raycast, keyboard-clear like Lazygit, and as careful with explanatory copy as Stripe.

## Strategic principles

- Preserve existing safety, approval, routing, memory, state, and telemetry boundaries.
- Make target and execution context persistently visible.
- Distinguish saving configuration from installing dependencies or starting a daemon.
- Present one primary setup decision at a time with safe recommended defaults.
- Reveal advanced settings progressively.
- Use text and icons alongside color for every meaningful status.
- Stay fully usable by keyboard at 80 columns, become roomier at 100 columns, and add optional context at 120 columns.

## Anti-references

Avoid cyberpunk or Matrix terminal styling, generic card dashboards, long step rails, tiny metadata, decorative panels, color-only status, mouse-dependent controls, gradients, glass, neon, nested cards, side-stripe accents, and decorative dashboard grids.

## Scope

This branch covers the four-stage setup experience, integrated chat, and shared reusable TUI foundations. Settings is not fully redesigned here, but foundations should support that later work.
