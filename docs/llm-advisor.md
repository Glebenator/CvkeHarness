# LLM advisor

The `llm_advisor` security profile adds an explanation to each action that needs
human approval. Its policy defaults match Reasonable. You can customize its
individual controls in Settings as with other profiles.

1. Open `cvkeharness settings` and select **Security → Security profile →
   llm_advisor**. Confirm the profile choice; applying a preset clears prior
   per-control overrides.
2. In **Models → Safety advisor**, choose a saved connection and model, or inherit
   Primary. The advisor can use a different provider from the agent and judge.
3. Save. Start a new console session with `/new`, or start a new CLI run.

First-run setup also offers LLM advisor and opens its model picker immediately
after the safety choice.

The corresponding configuration fields are:

```yaml
security:
  profile: llm_advisor
models:
  safety_advisor:
    inherit: primary
# Or choose an existing named connection:
#   safety_advisor:
#     connection: review-api
#     model: your-provider-native-model-id
```

For older configurations without a `security` section, `safety_mode:
llm_advisor` migrates to this profile. The `security` section is authoritative
when both are present.

## Reviewing an action

Before approval, the advisor receives the full command or tool action, detected
effects, policy reason, and local working directory. It is prompted to explain
the sequence, affected files and destinations, network transfers, privileges,
destructive changes, and unknowns. The prompt targets at most 60 words for simple
commands and 120 for complex actions: one sentence about the outcome and an
**approve** or **reject** recommendation with a short reason. Steps appear only
for complex actions; risks and uncertainty appear only when material to the
decision. Generic caveats and repeated facts are discouraged. These are prompt
instructions, not hard truncation limits. The explanation describes expected behavior; the advisor does not run
commands or inspect the environment.

The console places the exact command first in a bold, highlighted block, with
advisor explanations in regular-weight text underneath. The full review remains
scrollable. Arrow/Page keys, Home, and End navigate the review; `d` shows policy
details. `a` grants one exact approval and continues the turn. Escape closes the
dialog without approving, and Ctrl+X interrupts the pending turn. Terminal
prompts show the same advice with Reject selected by default. Deferred jobs keep
the advice in their blocked-work reason for later inspection.

## Authorization and failures

The advisor cannot grant approval, change policy, execute tools, or widen a
grant. Either recommendation still requires a human decision. Already allowed
actions need no advice; denied actions remain denied. In this mode even controls
set to `llm_review` use the explanatory advisor followed by human approval.
An existing exact approval grant is consumed normally without a second review.

The review has a 45-second timeout. Provider errors, missing responses, tool-call
responses, and malformed or incomplete advice display **Advice unavailable** and
fall back to the same human gate. Canceling the run stops the review. An advisor
failure never runs the command or silently substitutes an approve recommendation.

Recognized credential patterns are masked before the review request. This is
pattern-based redaction, not a guarantee that arbitrary private data is removed:
the selected provider receives the remaining command and policy context. The
advisor sees that content as untrusted data and has no execution tools. Its
explanation can still be wrong, especially when behavior depends on remote
scripts, environment variables, filesystem state, or unavailable context.
