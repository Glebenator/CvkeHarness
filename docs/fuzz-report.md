# Fuzz Report

- Generated: `2026-09-10T05:52:44Z`
- Commit: `db56398`
- Package: `./tools`
- Fuzz time: `30s`

## Summary

| Metric | Value |
| --- | --- |
| Passed suites | `1 / 2` |
| Failed suites | `1` |
| Total executions | `2579344` |
| Coverage-expanding inputs | `1197` |

## Failures

### FuzzParseShellCommand

- Failing input: `shell_fuzz_test.go:23: accepted command with blocked shell syntax: "<<'0'\n0 "`
- Artifact: `testdata/fuzz/FuzzParseShellCommand/b1cc7347a099eedf`
- Reproduce: `go test -run=FuzzParseShellCommand/b1cc7347a099eedf`
- Error: `exit status 1`

## Shell Policy Corpus

Corpus cases are evaluated semantically as `allow`, `deny`, or `require_approval`; `coverage-expanding input` is Go fuzzing's term for inputs that reached new code coverage, not necessarily security-relevant inputs.

| Outcome | Count |
| --- | ---: |
| Allow | `7` |
| Deny | `7` |
| Require approval | `9` |
| Mismatches | `0` |

### Category Outcomes

| Category | Total | Allow | Deny | Approval |
| --- | ---: | ---: | ---: | ---: |
| `approval_required` | `2` | `0` | `0` | `2` |
| `mutation` | `1` | `0` | `0` | `1` |
| `network_probe` | `1` | `0` | `0` | `1` |
| `safe_readonly` | `7` | `7` | `0` | `0` |
| `secret_access` | `1` | `0` | `0` | `1` |
| `shell_escape` | `8` | `0` | `7` | `1` |
| `unapproved_segment` | `3` | `0` | `0` | `3` |

### Sample Cases

**Accepted**

- `safe-readonly-ps` (`safe_readonly`): `ps aux`
- `safe-readonly-df` (`safe_readonly`): `df -h`
- `safe-readonly-free` (`safe_readonly`): `free -m`

**Denied**

- `shell-escape-substitution` (`shell_escape`): `ps $(whoami)`
- `shell-escape-backticks` (`shell_escape`): `ps \`whoami\``
- `shell-escape-unquoted-heredoc` (`shell_escape`): `python3 - <<PY
print('hello')
PY`

**Approval required**

- `shell-escape-redirection` (`shell_escape`): `ps > /tmp/output.txt`
- `unapproved-segment-newline` (`unapproved_segment`): `ps
whoami`
- `unapproved-segment-chain` (`unapproved_segment`): `ps aux; whoami`

## Invariants

| Invariant | Status | Covered by |
| --- | --- | --- |
| raw newlines are rejected before trimming | `failed` | `FuzzParseShellCommand, FuzzValidateAllowedShellCommand` |
| command substitution is rejected outside inert quoting | `failed` | `FuzzParseShellCommand, FuzzValidateAllowedShellCommand` |
| redirection and bare backgrounding are rejected | `failed` | `FuzzParseShellCommand, FuzzValidateAllowedShellCommand` |
| accepted normalized parses are idempotent | `failed` | `FuzzParseShellCommand` |
| accepted chained commands keep operators equal to segments minus one | `failed` | `FuzzParseShellCommand, FuzzValidateAllowedShellCommand` |

## Suites

| Suite | Pass | Execs | Coverage-expanding inputs | Duration |
| --- | --- | ---: | ---: | ---: |
| `FuzzParseShellCommand` | `no` | `988618` | `495` | `35333ms` |
| `FuzzValidateAllowedShellCommand` | `yes` | `1590726` | `702` | `32355ms` |

## FuzzParseShellCommand Output

- Error: `exit status 1`

```text
fuzz: elapsed: 0s, gathering baseline coverage: 0/305 completed
fuzz: elapsed: 0s, gathering baseline coverage: 305/305 completed, now fuzzing with 11 workers
fuzz: elapsed: 3s, execs: 127030 (42334/sec), new interesting: 63 (total: 368)
fuzz: elapsed: 6s, execs: 316957 (63319/sec), new interesting: 116 (total: 421)
fuzz: elapsed: 9s, execs: 483951 (55659/sec), new interesting: 151 (total: 456)
fuzz: elapsed: 12s, execs: 653925 (56661/sec), new interesting: 152 (total: 457)
fuzz: elapsed: 15s, execs: 817047 (54362/sec), new interesting: 189 (total: 494)
fuzz: elapsed: 18s, execs: 988618 (57206/sec), new interesting: 190 (total: 495)
fuzz: minimizing 36-byte failing input file
fuzz: elapsed: 19s, minimizing
--- FAIL: FuzzParseShellCommand (19.41s)
    --- FAIL: FuzzParseShellCommand (0.00s)
        shell_fuzz_test.go:23: accepted command with blocked shell syntax: "<<'0'\n0 "
    
    Failing input written to testdata/fuzz/FuzzParseShellCommand/b1cc7347a099eedf
    To re-run:
    go test -run=FuzzParseShellCommand/b1cc7347a099eedf
FAIL
exit status 1
FAIL	github.com/coolcake/cvkeharness/tools	19.957s
```
