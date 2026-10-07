# Security Policy

CvkeHarness lets a language model propose shell commands and other actions against local and remote systems, so policy bypasses are treated as security bugs.

## Reporting a vulnerability

Please do not open a public issue. Report privately through [GitHub private vulnerability reporting](https://github.com/Glebenator/CvkeHarness/security/advisories/new).

Include the version or commit, your security profile and relevant overrides, and the smallest input that reproduces the problem. Redact credentials, hostnames, and other private operational data.

## In scope

- Actions that run without the approval the active security profile requires
- Shell parsing or classification gaps that let a denied effect through
- Credential leaks into prompts, telemetry, exports, prompt dumps, or SQLite state
- Operational-memory poisoning that bypasses the review lifecycle or integrity checks
- `web_fetch` reaching localhost, private, link-local, or metadata addresses

## Out of scope

- Behavior that the selected profile explicitly allows, including everything permitted by `less_strict`, `minimal`, or `yolo`
- Commands a human operator approved
- Model output quality or refusals that cause no policy bypass
