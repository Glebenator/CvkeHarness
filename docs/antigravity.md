# Personal Antigravity provider

CvkeHarness can request Gemini completions directly using your Google Antigravity
OAuth session. This is an **unofficial** integration. Google's current terms
prohibit third-party subscription access and may suspend Antigravity/Gemini CLI
access. Personal use does not change that policy. There is no API-key billing
fallback, account rotation, caller-owned project creation, or quota retry loop.

## Sign in

The prototype ships without OAuth application credentials. Before login or
token refresh, supply both `CVKEHARNESS_ANTIGRAVITY_CLIENT_ID` and
`CVKEHARNESS_ANTIGRAVITY_CLIENT_SECRET` through your private runtime environment.
These identify the OAuth application; they are separate from your account's
access/refresh tokens. Do not put their values in source files, tracked config,
or command examples. Missing values stop login before opening its callback
listener and stop token exchange before any request is sent.

Providing these values does not fix Google's `UNSUPPORTED_CLIENT` rejection or
establish subscription access. A caller-owned Cloud project is not a substitute
for supported client eligibility. The provider remains an unsupported prototype.

With private client configuration available, build the app, then run:

```sh
go build -o cvkeharness .
./cvkeharness antigravity login
```

Open the printed Google authorization URL in your browser and complete consent.
The command listens only on IPv4 loopback, port 51121, validates OAuth state and
PKCE, and times out after five minutes. Your browser must run on the same host.
It checks the production and Antigravity Code Assist endpoints for your project.
If neither returns one and Google offers a default managed tier with no current
tier, login completes that tier's onboarding and waits briefly for the returned
operation. It never guesses a project ID or selects a different tier.

A successful browser callback means only that the authorization code arrived.
The terminal's final message confirms whether credentials were actually saved.
Missing project IDs can be an integration compatibility issue even when Google's
app works; the error includes non-sensitive discovery counts for diagnosis.

Credentials are written atomically with mode 0600 to
`~/.cvkeharness/antigravity-auth.json`, independently of Google's own app caches.
Use `CVKEHARNESS_ANTIGRAVITY_AUTH` to choose another file. Expiring access tokens
are refreshed automatically; refreshes are serialized within a running process.
Avoid concurrent login/refresh operations from multiple CvkeHarness processes.
Do not share this file or put it in source control.

```sh
./cvkeharness antigravity status
./cvkeharness settings
```

Select `antigravity`, then enter an exact `gemini-...` model ID available in your
Antigravity account. The model list deliberately does not claim to have verified
your entitlements. No API key or connection URL is needed. Set the advisory/safety
model to a Gemini model too, and review any explicit planning, execution,
curation, or approved routing models left over from another provider.

For example, the relevant configuration fields are:

```yaml
provider: antigravity
default_model: gemini-3.8-flash
safety_model: gemini-3.8-flash
routing_enabled: false
max_tokens: 8192
```

The example model is not a guarantee of account availability. Start a new task
when switching providers: native tool-call state is not portable between them.

## Behavior and limitations

- Uses the Antigravity streaming endpoint and buffers one completed response,
  consistent with the app's Provider interface.
- Preserves native response parts, including thought signatures, between tool
  calls. Private thought text is not included in the assistant's visible text.
- Returns tool requests to the existing CvkeHarness execution/approval path;
  does not launch an independent coding agent or grant extra tool permissions.
- Rejects malformed/incomplete/blocked streams and truncated tool calls.
- Reports authentication, forbidden-access, and exhausted-quota errors without
  echoing Google's response bodies or user tokens.
- Supports Gemini text/tool workflows only. Claude models and media input are
  outside this provider's scope.
- Internal endpoints, OAuth clients, protocol versions, and model names can
  change without notice. A passing mocked test is not a live-service validation.

Protocol reference inspected during implementation:
[community provider source](https://github.com/nihar5hah/pi-mono-gemini-cli/tree/main/packages/ai/src)
and [Google terms](https://antigravity.google/terms).

The discovery/onboarding sequence is based on [Google Code Assist setup](https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/code_assist/setup.ts). Onboarding is bounded and access-denied or quota responses stop the flow.
