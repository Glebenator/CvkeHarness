# Recovery implementation and evidence

Status: all seven bounded implementation layers are implemented and validated.
The full combined gate and subsequent affected-workflow regressions passed.
This is an acceptance ledger for the recovery goal, not a public-release
certification for the whole application.

## Product contract

Supported operations have explicit preconditions, target identity, impact limits, a durable recovery record, and verified outcomes. Arbitrary shell execution is not automatically reversible. Recovery must not depend on a model, network provider, or conversation history. Existing scoped authorization remains authoritative.

## Milestones

- [x] Initial regular-file executor: exact manifests, bounded edits/creation/quarantine deletion, private artifacts, supported metadata, stale-state/restore-conflict checks, symlink/mount checks. Broader metadata, directory enumeration, external-writer atomicity and cleanup remain to harden.
- [x] Initial durable lifecycle: SQLite revisioned journal, preparation/readiness/application/verification/commit/unknown/restore/failure, deterministic CLI, interruption and partial-apply tests.
- [x] Runtime integration: operator roots/budgets, typed local and enrolled remote tools, exact current-policy approvals, target-context refusals, Overview inspection and offline recovery. File permission classification is conservative; finer classification could reduce prompts.
- [x] Initial Linux service transaction: a real standalone NGINX adapter with candidate validation, one config replacement, bound-master reload, fixed health checks from new workers, and one deterministic rollback. Arbitrary NGINX configurations remain unsupported.
- [x] Initial target-local timed recovery: dedicated Linux OpenSSH port changes, persisted boot-relative deadlines, kernel-observed new-transport confirmation, conflict-aware automatic restoration, and tested init/supervisor restart and VM reboot behavior.
- [x] Numerical safety: exact decimal/unit arithmetic, bounded results, fresh filesystem measurements, staging/restoration headroom, and NGINX worker/connection, descriptor and reload-overlap checks. Workload RAM/CPU prediction and cgroup quota guarantees are outside supported coverage.
- [x] Impact control: aggregate file/byte/service/host limits, serial rollout, target-side serialization, durable model/controller repair ceilings and protected recovery/transport assets.
- [x] Initial native snapshots: real Btrfs ioctls, bounded supported inventories, read-only checkpoints, whole-tree exchange with displaced-tree retention, persistent identities, explicit interruption recovery and genuine VM evidence. Broader filesystems/application consistency remain unsupported.
- [x] End-to-end fault lab: compiled CLI/PTY and mock provider loop; disposable Docker targets; real Linux VM for kernel/boot/network/filesystem claims.

## Validation matrix

Record the command, result and scope here as each milestone is completed. Required failure cases include process death at durable stage boundaries, disconnect after apply, conflicting writers, symlink swaps, backup storage exhaustion, numerical errors, partial application and failed restoration. A passing narrow suite cannot stand in for an untested adapter or a missing VM run.

### 2026-09-10: initial file engine

- `go test ./recovery`: passed on macOS, including all three file actions, stale plans, partial restoration conflicts, interrupted stage reconciliation, damaged backups, target/policy/digest binding, symlink/hardlink rejection, directory swaps and combined byte budgets.
- `go test -tags=e2e ./e2e -run '^TestRecoveryConsole' -v`: passed. Real Console/PTY, scripted provider, exact one-time approval, independently verified application, then CLI restoration after the provider was shut down.
- `bash scripts/test-recovery-docker.sh`: passed on Docker Desktop Linux, with nine deliberate process-death stages plus real backup-space exhaustion and conflicting-writer restoration failure. Disposable container removed after test.
- Final `go test -count=1 ./...`, `go test -race ./...`, and `go vet ./...`: passed after integration and approval-visibility refinements.
- Final `go test -count=1 -tags=e2e,recoverydocker ./e2e -v`: passed all existing journeys, the new console/offline-recovery journey, and all 11 Docker failure cases. Owned lab containers were confirmed removed.
- Diff whitespace check passed for changed code and new documentation. The pre-existing trailing whitespace in `docs/fuzz-report.md` was left untouched.
- The pre-existing saved fuzz input `tools/testdata/fuzz/FuzzParseShellCommand/b1cc7347a099eedf` exposed unsafe heredoc boundary trimming. The parser/executor/grant normalization now preserve heredoc trailing whitespace; focused regressions passed. Existing corpus/report artifacts were preserved.
- The real Console journey exposed an approval receipt arriving after a fast operation completed. Receipts now stay associated with the requested work, remain visible for the turn, and do not regress a completed turn to RESUMING; a regression test covers late and stale receipts.

Read [the operating guide](recovery.md) for explicit limitations. Later entries
record the subsequently completed timed SSH, snapshot, resource and fleet work.

### 2026-09-10: initial NGINX service transaction

- Implemented a deliberately bounded standalone HTTP adapter, operator-defined service names/health checks, immutable executable/process/runtime-directory identities, Linux pidfd capability checks and signaling, baseline/staged/installed validation, new-worker health attestation, one automatic rollback and offline recovery.
- Agent service actions have separate effect classification and exact review; file-only grants cannot authorize service changes. Registered service configs require typed service transactions. The offline CLI derives the required action kind from the immutable manifest.
- `go test -count=1 ./...`: passed during integration. Focused parser/policy tests reject dangerous directives, malformed syntax, changed runtime references and invalid numeric bounds.
- `go test -count=1 -tags=e2e,recoverydocker ./e2e -run '^TestRecoveryDocker' -v`: passed the file lab and initial NGINX lab. Fault cases now require exit code 86, preventing ordinary execution failures from masquerading as injected process death.
- Extended NGINX lab passed all eight cases: commit/offline restore with the app config removed, unhealthy-candidate automatic rollback, four service-stage deaths, conflicting-writer refusal with explicit conflict resolution, and a stopped master producing `recovery_failed` even when file restoration succeeds.
- The lab uses the official NGINX image pinned to manifest digest `sha256:dc5069ad14f19660b141b21236140b91656bf89bbc3e2417c70ae650cd66104c`, with no host mounts, external network or published ports. Synthetic fixture ownership is created inside the cap-dropped target; host UID preservation by `docker cp` initially exercised a real permission failure and safe rollback.
- Public image download initially stalled in `docker-credential-desktop`. Only that test pull/helper was stopped; an empty temporary Docker client config downloaded the public image without accessing saved registry credentials. No host credential or privilege changes were made.
- After capability/directory hardening, the full CLI/PTY/Docker E2E suite, `go test -race ./...`, and `go vet ./...` passed. The full recovery goal remains active.

### 2026-09-10: initial numerical safety and fresh capacity checks

- Added deterministic `safety_calculate` agent tool and provider-independent `recovery calculate` CLI. Decimal strings use rational arithmetic, explicit dimensional rules and integer rounding (`exact`, `floor`, `ceil`, `nearest_even`). Tests cover SI/binary-unit differences, time units, percentages, signed rounding, leading-zero decimal interpretation, fractional refusal, division by zero, signed-64-bit overflow and incorrect expected values.
- File application now samples actual target filesystems after preparation and immediately before application. It conservatively budgets candidate and restoration staging rounded to allocation blocks, metadata headroom and an operator free-space reserve (16 MiB default). Quarantine deletion gets no space-reclamation credit. Measurements are durable, bounded, revision-bound records separate from the immutable approval manifest and visible through inspection.
- An insufficient-capacity refusal leaves the operation ready and target files unchanged. Subsequent explicit application samples again. Recovery is not stranded by a new-operation capacity reserve. This is a precondition, not an allocation reservation or a prediction of quotas/other writers.
- `go test -count=1 ./...`: passed after numeric integration.
- `go test -count=1 -tags=e2e,recoverydocker ./e2e -v`: passed all CLI/PTY journeys, 14 file/numeric fault-lab cases and eight actual NGINX scenarios. New cases prove a real 64 KiB target filesystem is measured and refused before mutation, reject mistaken numerical input through the compiled CLI, and terminate at the new capacity-check boundary. The original real backup ENOSPC scenario remains covered.
- Final `go test -race ./...` and `go vet ./...` passed after numerical integration and agent guidance changes. The diff whitespace check passed (excluding the preserved pre-existing report whitespace), and Docker confirmed no remaining containers carrying the recovery-lab label. No VM, timed SSH/network recovery, snapshot adapter or fleet controller has been implemented in this milestone.

### 2026-09-10: genuine VM lab and native Btrfs snapshots

- Built the owned `cvkeharness-recovery-vm:alpine323` image (ID
  `sha256:274042b3c6be851fcb804ecb15831c162606c2df8d64206121242c0994836f36`).
  It contains QEMU 10.1.5-r0, guest Linux 6.18.50-0-virt, Btrfs-progs 6.17.1-r0
  and OpenSSH 10.2_p1-r0, based on pinned Alpine 3.23.4. Package revisions are
  resolved during image build, so the base pin alone is not a complete package
  lock. The public-image build used a temporary Docker config with the installed
  Buildx plugin path, without host registry credentials.
- The runtime container has no external network, published ports, mounts,
  Docker socket or host devices. QEMU TCG boots a separate AArch64 kernel with
  two owned 512 MiB virtual disks (ext4 state and Btrfs data). SSH forwarding
  binds container loopback and uses generated lab-only keys. This uses QEMU's
  documented [system emulation](https://www.qemu.org/docs/master/about/emulation.html)
  and [virt platform](https://www.qemu.org/docs/master/system/arm/virt.html).
- Actual compiled CLI execution through SSH proves a distinct guest boot ID,
  process death after file application, persistent state/target identity,
  guest reboot, new SSH connection and provider-independent restoration.
- Implemented native Btrfs capability detection, bounded file/directory
  inventories, filesystem/subvolume UUID and persistent namespace bindings,
  native read-only checkpoints, fresh capacity evidence and exact new restore
  plans. Restore uses native writable cloning plus atomic namespace exchange,
  retaining the entire displaced tree. The Linux adapter uses held descriptors
  and typed ioctls; no external Btrfs command executes production mutations.
- VM evidence covers restored deleted/edited files, preservation of introduced
  files in the displaced tree, repeat restoration, process death during
  checkpoint/restore, recovery after exchange and guest reboot without app
  config, stale-writer refusal, newly reviewed restoration retaining that
  writer, symlink/nested-subvolume/directory-bind/file-bind mount refusal,
  nested credential-path refusal and tampering despite restoring the read-only
  flag afterward. Independent `btrfs property get` verifies native read-only
  state; independent file reads verify contents.
- The VM tests found two native ABI/identity details: GET_SUBVOL_INFO exposes
  root-item read-only bit 0, while SNAP_CREATE_V2 uses ioctl flag bit 1; cloning
  a read-only checkpoint can update its root generation. The adapter checks
  UUIDs and the complete supported inventory instead of treating generation as
  an immutable content hash. See the Linux
  [UAPI layouts](https://github.com/torvalds/linux/blob/v6.18/include/uapi/linux/btrfs.h),
  [root flags](https://github.com/torvalds/linux/blob/v6.18/include/uapi/linux/btrfs_tree.h)
  and [snapshot creation](https://github.com/torvalds/linux/blob/v6.18/fs/btrfs/transaction.c).
- `go test -count=1 ./...`, `go test -race ./...` and `go vet ./...` passed after
  snapshot integration. `go test -count=1 -tags=e2e,recoverydocker,recoveryvm
  ./e2e -v` passed all actual CLI/PTY/provider-loop journeys, 14 Docker file/math
  cases, eight NGINX cases and both genuine VM tests (107 seconds).
- The final checkpoint-receipt crash case also passed in the VM (54 seconds
  for the expanded native suite). Recovery reuses the verified persisted
  receipt instead of consuming another bounded evidence slot. Focused race
  tests for recovery/tools/cmd/config and `go vet ./...` passed afterward.
  Formatting/diff checks passed, excluding the preserved pre-existing report
  whitespace. Test-owned Docker containers were removed.

### 2026-09-11: target-local managed SSH recovery

- Implemented a dedicated root-owned OpenSSH supervisor, strict port-only
  configuration changes, fixed executable/authentication identities, pidfd
  reload, bound listening-socket/banner checks, a durable watchdog reference
  preceding mutation, and a boot-relative 15–300 second confirmation window.
  Heartbeats cannot overwrite the pending deadline. Competing recovery
  mutations/preparations sharing that state are refused while it is armed.
- Confirmation walks kernel process ancestry to the bound listener, requires
  new `sshd-session` start times and an established socket on the proposed
  port, and rechecks deadline and manifest conditions. It records bounded
  process evidence. An old multiplexed transport with a spoofed SSH environment
  was independently rejected in the VM; a fresh connection committed.
- The supervisor owns listener startup. The lab init loop restarts it and starts
  it after persistent disks mount. It restores pending changes before launching
  SSH after process restart or guest reboot. This exact integration is tested;
  arbitrary Linux init/systemd configurations are not claimed as verified.
- Added CLI supervision, service discovery, confirmation and typed apply/recover
  dispatch; `recovery_manage` SSH actions carry distinct service/network/file
  effects and reject mixed adapters. Console exposes awaiting-confirmation
  state and explicit instructions without triggering actions. `inspect` has a
  persisted deadline record with an advisory UTC estimate; `CLOCK_BOOTTIME`
  remains the clock that controls decisions.
- The first VM run exposed SQLite NULL being scanned directly into
  `json.RawMessage` for an idle watchdog. Scanning through nullable byte slices
  fixed startup. A reopen/heartbeat/arming/CAS test verifies deadline persistence
  and ensures another operation cannot disarm it. The broken owned VM was
  stopped and removed before testing a freshly compiled binary.
- The updated owned VM image is
  `sha256:66f5b377f64e41d74fe581a6c0dcee2b6f41fde6b7da4762d87c5bcb66718a5b`.
  It adds the init-supervised SSH service and a second container-loopback
  forwarding port, retaining the previous kernel/package versions and host
  isolation. No system SSH service or host security setting was changed.
- Initial full SSH VM suite passed all nine scenarios in 209 seconds: fresh
  transport confirmation/offline restoration; process deaths after arming,
  application, signaling and awaiting confirmation; stale-watchdog refusal;
  conflicting-writer preservation with sticky failure and explicit recovery;
  watchdog process restart; and guest reboot before confirmation. Target
  configuration bytes, connection behavior and persisted status were inspected
  independently. Full local tests also passed after SSH integration.
- `bash scripts/test-recovery-all.sh` passed full local tests, the race suite,
  vet, and the combined CLI/PTY/Docker/VM gate (312 seconds for E2E). That run
  included the original nine SSH cases, both other VM suites, 14 Docker file/math
  cases and eight NGINX cases. The script does not silently skip the VM.
- Four subsequently added SSH boundary cases passed in a targeted VM run
  (163 seconds including build): death before file application, death after
  reload evidence, death during restoration, and lost acknowledgement after
  durable confirmation. Confirmation remained committed past the deadline;
  repeated recovery after death at its final journal boundary was idempotent.
  Focused recovery/tool/console tests and vet passed after the extra fault hooks.
  Docker inspection confirmed no remaining containers with the recovery label.

### 2026-09-11: bounded fleet and service resource checks

- Implemented a serial controller for exact, already-prepared file/NGINX
  operations on enrolled SSH targets. Pinned host keys, expected machine/
  principal identities, transport assets, target manifests and controller policy
  are bound to the reviewed batch. The remote CLI checks `--expect-target`
  before action dispatch. Ambient SSH configuration, agent access, multiplexing,
  forwarding and model-supplied command strings are excluded.
- Preparation computes original-plus-candidate bytes, files, services and hosts;
  both reviewed and current operator limits constrain application. Every host
  is inspected before rollout and immediately before dispatch. Target executors
  independently enforce their original live preconditions and capacity checks.
- SQLite claims prevent wrapping the same target operation into another batch
  in this controller state. Revisioned dispatch/repair events precede network
  transmission. Unknown/failing outcomes stop subsequent targets. A dispatched
  batch cannot be applied again; reconciliation only inspects. Explicit reverse-
  order recovery has a durable per-target ceiling, default two attempts, maximum
  three. Local model recovery actions also reserve durable attempt records;
  exhaustion leaves direct operator CLI recovery available.
- Added `recovery_fleet`, CLI `recovery fleet`, controller-context checks,
  exact review summaries, remote/service/file/network/credential policy effects,
  and Overview batch inspection. The first lab run correctly hit the default
  reasonable profile's credential denial; only the synthetic lab policy was
  updated to ask before using its generated key. Product defaults were preserved.
- The actual compiled CLI fleet suite passed all 11 cases (24 seconds): serial
  commit/offline restore; actual SSH session death after target commit; five
  controller dispatch/repair crash boundaries; conflicting target state;
  aggregate budget refusal; wrong host key/expected executor; conflicting
  restoration and exhausted durable repair budget. Three owned containers use
  one internal Docker network, generated keys, no exposed ports/mounts/socket,
  and only target OpenSSH's setuid/setgid/chroot capabilities. This is container
  transport evidence, not a fleet reboot or live-provider claim.
- NGINX demand is derived from the validated grammar with bounded integer
  multiplication. Operator limits cover workers, per-worker/total connections,
  descriptor reserve and old/new worker overlap. Before changing configuration,
  the executor reads the bound master's CPU affinity, soft descriptor limit and
  live worker children, then persists its decision. It never raises limits.
  Affinity does not measure cgroup CPU quota; these checks do not estimate RAM
  consumption or reserve resources against other programs.
- All 11 NGINX Docker cases passed (19 seconds). Three new cases establish a
  real 512-descriptor target limit and refusal without mutation, operator
  connection-impact refusal, and death after a read-only resource measurement.
  Existing health rollback, interruption, writer conflict and dead-master cases
  remained green. The combined fleet/NGINX targeted run took 53 seconds including
  the E2E executable build.
- Full local tests, `go test -race ./...`, and `go vet ./...` passed after fleet,
  service resources and local model-repair limits. A restricted first local run
  could not bind an unrelated HTTP test socket; rerunning with the test's local
  socket access succeeded. No application failure was hidden as an environment
  skip.
- The final audit added compiled-CLI tests for death after durable file recovery,
  a two-file operation dying after its first replacement, and a parent symlink
  swap toward a synthetic sensitive file. All three passed in a disposable
  container (18 seconds including build). The old file and sentinel bytes were
  independently read, not inferred from the journal.
- The first final combined gate exposed `SQLITE_BUSY` while the SSH watchdog
  heartbeat competed with a journal evidence transaction. The pending guard
  remained armed and correctly blocked the next preparation; the suite failed
  instead of treating that ordinary error as injected process death. The other
  container suites, CLI journey and file/snapshot VM tests passed in that run.
  State connections now receive the busy timeout individually, and write
  transactions use `BEGIN IMMEDIATE` before their initial reads. This follows
  SQLite's documented [read-to-write snapshot upgrade behavior](https://www.sqlite.org/isolation.html).
  Deterministic competing-writer, pooled-connection and concurrent evidence/
  heartbeat regression tests passed. Post-arming SSH journal errors now attempt
  to persist an explicit unknown outcome while preserving the watchdog.
- The protection audit added explicit refusal for replacing the running
  recovery executable through file or snapshot operations and closed the
  snapshot path around a configured NGINX executable. Focused tests verify
  refusal before touching synthetic executable files.
- The complete `bash scripts/test-recovery-all.sh` gate passed after the SQLite
  fix: full local tests, race, vet, and all CLI/PTY/Docker/VM workflows. E2E took
  482 seconds, including all 13 SSH cases, 17 file/math cases, 11 fleet cases,
  11 NGINX cases and both file/native-snapshot VM suites. The previously failing
  SSH boundary passed with the same injected-death assertion.
- Final refinement: recovering a still-ready file/NGINX target verifies the
  unchanged original and records terminal cancellation under its local lock.
  This resolves controller death before transmission and prevents a delayed
  apply from executing after recovery. It does not overwrite a conflicting
  writer. The affected compiled CLI/PTY, all Docker suites and native snapshot
  VM suite passed against the latest source (118 seconds). Full local tests,
  the race suite and vet also passed after this refinement and executable
  protection changes. This latter targeted run does not replace the preceding
  complete SSH/reboot gate; together they cover the final changes.
- Final Docker inspection found no remaining recovery-labelled containers or
  internal networks. Formatting and whitespace checks passed, excluding only
  the preserved pre-existing report whitespace. Existing unrelated changes
  were retained. No commits, pushes, deployment or host security changes were
  performed; the compiled test executables and destructive scenarios stayed in
  temporary/owned lab targets.

## Requirement audit and coverage boundary

| Goal requirement | Implementation and evidence | Deliberate boundary |
| --- | --- | --- |
| Typed reversible file changes | Immutable paths/hashes/metadata, private backups, quarantine; file/unit/Docker tests including partial application, symlink swap and actual ENOSPC | Regular files and supported metadata; no arbitrary shell rollback or atomic exclusion of external writers |
| Durable lifecycle/offline recovery | SQLite revision checks, explicit unknown state, CLI without provider; interrupted CLI/VM restart tests | Recovery assets and target storage must remain available |
| Transactional service configuration | Native Linux NGINX validation, bound reload, new-worker health, one automatic rollback; 11 real NGINX cases | Supported standalone HTTP grammar; no arbitrary includes/modules/database/service adapters |
| Target-local timed recovery | Persist-before-mutate SSH guard, kernel-observed new connection, watchdog/init restart and actual guest reboot tests | Dedicated root-owned key-only OpenSSH port change; the tested init integration is required |
| Numerical/capacity/impact safety | Exact rational units/rounding; real filesystem measurements; NGINX demand, descriptor, affinity and reload-overlap checks | No claim to forecast workload RAM/CPU or reserve resources |
| Batches and repair limits | Serial exact remote batches, aggregate budgets, durable dispatch/claims and repair slots; 11 fleet cases and local budget-restart tests | Per-batch/controller-state limits; direct operator recovery remains possible; no universal distributed transaction |
| Real native snapshots | Btrfs ioctls, capability refusal, read-only checkpoints, bounded inventory and retained tree exchange; real kernel/boot VM tests | Dedicated supported subvolumes; no application consistency or independent disaster backup |
| Runtime/approval/visibility | Actual Console PTY and scripted provider execute an approved recovery tool; target-context and policy tests; passive Overview inspection | Provider behavior is mocked; no live-model reliability or complete public-release certification |

The recovery goal is complete within these documented adapter boundaries.
Broader adapters, metadata support, arbitrary shell coverage, finer permission
classification and public-release packaging are future work, not implied
guarantees of these bounded implementations. Antigravity login was excluded.

## Environment baseline

The initial checkout includes unrelated dirty Antigravity, model-picker, documentation and safety-report changes. Preserve them. Docker Desktop is installed; the Docker daemon was not running at the initial check. No colima, limactl or qemu-system-aarch64 executable was on PATH. Starting the installed Docker runtime is authorized for disposable tests; host destructive tests and unrelated Docker cleanup are not.
