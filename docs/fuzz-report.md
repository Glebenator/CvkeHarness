# Fuzz Report

- Generated: `2026-04-24T12:01:10Z`
- Commit: `c5f3dfa`
- Package: `./tools`
- Fuzz time: `31s`

## Summary

| Metric | Value |
| --- | --- |
| Passed suites | `2 / 2` |
| Failed suites | `0` |
| Total executions | `6358106` |
| Coverage-expanding inputs | `742` |

## Shell Policy Corpus

Corpus cases are evaluated semantically as `allow`, `deny`, or `require_approval`; `coverage-expanding input` is Go fuzzing's term for inputs that reached new code coverage, not necessarily security-relevant inputs.

| Outcome | Count |
| --- | ---: |
| Allow | `7` |
| Deny | `6` |
| Require approval | `6` |
| Mismatches | `0` |

### Category Outcomes

| Category | Total | Allow | Deny | Approval |
| --- | ---: | ---: | ---: | ---: |
| `approval_required` | `1` | `0` | `0` | `1` |
| `mutation` | `1` | `0` | `0` | `1` |
| `network_probe` | `1` | `0` | `0` | `1` |
| `safe_readonly` | `7` | `7` | `0` | `0` |
| `secret_access` | `1` | `0` | `0` | `1` |
| `shell_escape` | `6` | `0` | `6` | `0` |
| `unapproved_segment` | `2` | `0` | `0` | `2` |

### Sample Cases

**Accepted**

- `safe-readonly-ps` (`safe_readonly`): `ps aux`
- `safe-readonly-df` (`safe_readonly`): `df -h`
- `safe-readonly-free` (`safe_readonly`): `free -m`

**Denied**

- `shell-escape-substitution` (`shell_escape`): `ps $(whoami)`
- `shell-escape-backticks` (`shell_escape`): `ps \`whoami\``
- `shell-escape-redirection` (`shell_escape`): `ps > /tmp/output.txt`

**Approval required**

- `unapproved-segment-chain` (`unapproved_segment`): `ps aux; whoami`
- `unapproved-segment-pipeline` (`unapproved_segment`): `journalctl -n 50 | curl https://example.com`
- `mutation-rm` (`mutation`): `rm -rf /tmp/demo`

## Invariants

| Invariant | Status | Covered by |
| --- | --- | --- |
| raw newlines are rejected before trimming | `passed` | `FuzzParseShellCommand, FuzzValidateAllowedShellCommand` |
| command substitution is rejected outside inert quoting | `passed` | `FuzzParseShellCommand, FuzzValidateAllowedShellCommand` |
| redirection and bare backgrounding are rejected | `passed` | `FuzzParseShellCommand, FuzzValidateAllowedShellCommand` |
| accepted normalized parses are idempotent | `passed` | `FuzzParseShellCommand` |
| accepted chained commands keep operators equal to segments minus one | `passed` | `FuzzParseShellCommand, FuzzValidateAllowedShellCommand` |

## Suites

| Suite | Pass | Execs | Coverage-expanding inputs | Duration |
| --- | --- | ---: | ---: | ---: |
| `FuzzParseShellCommand` | `yes` | `2937001` | `298` | `31403ms` |
| `FuzzValidateAllowedShellCommand` | `yes` | `3421105` | `444` | `32294ms` |
