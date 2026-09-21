# Recoverable systems operations

CvkeHarness implements typed regular-file recovery, narrow Linux NGINX and
managed SSH transactions, native Btrfs checkpoints, checked resource arithmetic,
and bounded remote batches. Coverage is specific to these adapters. Arbitrary
shell commands, application-consistent database restoration and general machine
rollback are outside the guarantee. See [the acceptance ledger](recovery-implementation.md)
for validation scope and recorded test results.

For a first hands-on run, use the [HTML self-testing guide](recovery-testing-guide.html):
exact commands, expected results, disposable-file exercises, and Docker/VM lab steps.

## Platform support

The application builds on Windows, Linux and macOS. The recovery executor's
filesystem identity, metadata and locking implementation currently supports
Linux and macOS only. On native Windows, executor commands return an explicit
unsupported-platform error before opening recovery state, and the agent does
not advertise `recovery_manage` or `recovery_fleet`. Checked arithmetic through
`recovery calculate` and `safety_calculate` remains available.

Use a Linux build inside WSL2 for recovery workflows on a Windows machine, with
test files in the Linux filesystem and the required service/filesystem support.
NGINX, managed SSH and native Btrfs adapters retain their narrower Linux-specific
requirements; merely building the Windows executable does not provide rollback
coverage for native Windows files, services or disks.

## Configure the operator boundary

Add roots and optional budgets to `config.yaml`:

```yaml
recovery:
  roots:
    - /etc/nginx
    - /srv/example/config
  max_files: 100
  max_bytes: 33554432
  max_file_bytes: 8388608
  max_age_seconds: 900
  min_free_bytes: 16777216
  max_repair_attempts: 2
```

An empty root list disables file preparation/application. Roots must exist. Their
canonical paths are used; requested file paths must be absolute and cannot
traverse symlinks. On macOS, use `/private/tmp/...` when `/tmp` is a symlink.
The byte budget counts original plus proposed contents, not a model's estimate.
These settings are operator-authored and cannot be raised in a tool call.
The default free-space reserve is 16 MiB. Before every application, the executor
samples each actual target filesystem, rounds candidate/restoration staging to
allocation blocks, adds metadata headroom and checks the reserve. It records
those measurements separately from the immutable manifest; `inspect` includes
the evidence. A shortage leaves the operation `ready` and target files unchanged.
This check does not reserve blocks against other writers or predict quotas,
physical disk failure, compression or all filesystem metadata requirements.
Actual backup/write failures still go through the normal failure/recovery path.

## Prepare, apply, inspect, restore

Create a request containing exact regular-file changes. `replace` requires an
existing file; `create` requires a missing file and existing parent directory;
`delete` quarantines an existing regular file. Directory recursion and wildcards
are not accepted by this initial executor.

```json
{
  "changes": [
    {"action": "replace", "path": "/srv/example/config/settings.conf", "content": "enabled=true\n"}
  ]
}
```

```sh
cvkeharness recovery prepare --request change.json
cvkeharness recovery inspect OPERATION_ID
cvkeharness recovery apply OPERATION_ID --confirm REVIEWED_DIGEST
cvkeharness recovery recover OPERATION_ID --confirm REVIEWED_DIGEST
```

Replace the uppercase placeholders with the returned ID and exact digest.
`--confirm` is direct operator authorization for that manifest; application
still rejects policy-denied actions. The agent's `recovery_manage` tool routes
apply/restore through the existing one-time scoped-approval system. Its current
policy assessment conservatively requires create, overwrite and delete
permission for an apply/restore action. Finer per-entry classification could
reduce unnecessary prompts while retaining exact manifest review.

Model-triggered recovery consumes a durable attempt before dispatch. The default
is two attempts per operation, with an operator maximum of three. Failure,
lost acknowledgement and restart do not reset the count. Inspecting does not
consume an attempt; an already-recovered operation remains idempotent. Once
exhausted, the model must stop. The direct operator CLI remains available for
deliberate recovery after inspection and any necessary conflict resolution.

The CLI initializes no provider. If normal configuration cannot be loaded,
use `--state /absolute/path/to/state.db`. `--root` can explicitly specify an
operator-authorized preparation/application root. Restoration is tied to the
original manifest and machine/principal, so removing a configured root does
not strand an existing recovery operation.

`recovery list` and Console Overview expose recorded operations. `recovery
reconcile OPERATION_ID` marks interrupted execution as uncertain and changes no
target files. Inspect before restoring. An error after application starts is
not evidence that nothing changed.

## Checked resource arithmetic

The agent has a pure `safety_calculate` tool; the same implementation is available
without an app configuration or provider through `recovery calculate --request
math.json`. For example:

```json
{
  "operation": "multiply",
  "left": {"value": "75", "unit": "%"},
  "right": {"value": "8", "unit": "GiB"},
  "output_unit": "MiB",
  "rounding": "exact",
  "expected": "6144"
}
```

The result is exactly 6144 MiB. An `expected` value of 6000 returns the computed
result together with a mismatch and a failed command/tool result. Decimal
strings use rational arithmetic, so binary floating-point error cannot change
rounding. Units distinguish SI bytes (`kB`, `MB`, `GB`) from binary bytes (`KiB`,
`MiB`, `GiB`, `TiB`), and support `count`, `%`, `ms`, `s`, `min` and `h`.
Units are case-sensitive. Exponents and ambiguous spellings are refused.

Supported operations are addition/subtraction of matching dimensions, scalar
multiplication/division, and ratios between equal dimensions. Output is an
integer in the selected unit. Choose `exact` to reject fractional results,
or explicitly choose `floor`, `ceil`, or `nearest_even`. Division by zero,
unsupported compound units and results outside signed 64-bit bounds fail.
Calculation does not authorize a system change or measure a machine. File
executors derive sizes from actual bytes and independently check target
capacity; a model cannot substitute a calculator result for those checks.

## What is preserved

Original/candidate bytes are stored privately under the state database's
`recovery/` directory, separate from the SQLite manifest. Artifacts are mode
0600; directories are mode 0700. The manifest binds hashes, sizes, modes,
owner/group, modification time, parent directory identity and machine identity.
File restoration preserves contents, permission bits, owner/group and
modification time. It does not preserve inode number or access time.

Deletion moves the original into a mode-0700 quarantine directory on the same
filesystem; it does not reclaim disk space. Backups and quarantine are retained
after recovery. There is no automatic purge yet. Never delete them while an
operation may need recovery.

This backend rejects hard links, special files, special permission bits,
unsupported ACLs/xattrs, mount crossings, credential/repository paths and its
own recovery state. The running recovery executable and configured service/
transport executables are protected from supported file/snapshot replacements.
macOS-managed `com.apple.provenance` is explicitly excluded
from restoration; other extended attributes require a future metadata backend.
It is not a backup mechanism for running database data files.

## Concurrency and interruption

A process lock serializes CvkeHarness recovery mutations sharing one state
directory. SQLite journal updates use revision checks. Ancestor directory
descriptors are opened without symlink traversal, with parent identity checks.
The executor checks the original before application and checks the expected
output before restoration; detected conflicting files remain untouched.

External programs do not participate in this lock. Content checks followed by
replacement are not a filesystem compare-and-swap primitive; do not claim
isolation against arbitrary concurrent writers. Further concurrency hardening
and broader file metadata support remain in the acceptance ledger.

The journal records preparation, readiness, application, verification,
commit, unknown outcome, restoration and failure. A fresh process can recover
partially applied operations. No model-generated inverse command is executed.
Operations on a remote target must run the executor on that target: the local
agent tool refuses remote/ambiguous target contexts. Use the explicitly enrolled
`recovery_fleet` controller below for supported remote batches.

## Run the evidence

```sh
go test ./recovery ./tools ./state ./internal/tui
go test -tags=e2e ./e2e -run TestRecoveryConsole -v
docker pull nginx:stable-alpine@sha256:dc5069ad14f19660b141b21236140b91656bf89bbc3e2417c70ae650cd66104c
bash scripts/build-recovery-vm.sh
bash scripts/test-recovery-docker.sh
```

The Docker suite uses the actual compiled Linux CLI in an owned container with
no host mounts, published ports, external network or provider credentials. Its
instrumented build deliberately exits at durable stage boundaries, then starts
a fresh process and independently reads the target. It also exercises real
ENOSPC on a 64 KiB tmpfs and conflicting-writer recovery failure. Fault switches
only exist in binaries explicitly built with the `recoveryfault` build tag.

The NGINX suite runs actual master/worker processes and checks responses through
independent HTTP clients. It verifies a successful change, rollback after a bad
health response, interruptions during validation/reload, a conflicting writer,
and failure when the master exits. It also removes the entire app configuration
before an offline restore. Container tests do not establish VM boot recovery,
guest boot or native filesystem snapshot behavior. The separate fleet lab uses
real SSH connections between three containers on an owned internal network.

## NGINX service transaction (Linux)

This first adapter supports one already-running, standalone HTTP instance per
operation. Configure its binary, dedicated prefix, PID file and fixed health
check in the operator's configuration:

```yaml
recovery:
  roots: [/srv/example/nginx]
  services:
    - name: example
      binary: /usr/sbin/nginx
      config_path: /srv/example/nginx/nginx.conf
      prefix: /srv/example/nginx
      pid_file: /srv/example/nginx/nginx.pid
      health_url: http://127.0.0.1:8080/health
      health_status: 200
      # SHA256 of the exact response bytes "healthy", without a newline.
      health_sha256: 87695fdac81728b9d7f2d4a1335c2632bb5e6ba1bed21d2dff0254fba31c7d5b
      timeout_seconds: 3
```

The existing master must have been started with exactly `BINARY -p PREFIX -c
CONFIG_PATH`. Start/manage it separately as the service's operator; the adapter
does not install NGINX, start a missing service, restart a different master or
change its account. Linux procfs and pidfd signaling must be available. The
executable, master PID/start time/boot identity and runtime directories are
checked before execution. Changed identities cause a refusal.

The supported configuration is intentionally small: `events`, `http`, `server`
and ordinary/exact `location` blocks; bounded integer worker counts,
connections and keepalive seconds; `return`; `sendfile`; and `server_tokens`.
The original structure, listener, runtime paths and attestation header remain
fixed. Required settings include:

- Explicit `pid` matching the configured PID file and `error_log stderr notice`.
- `access_log off` in the HTTP block. Each of `client_body_temp_path`,
  `proxy_temp_path`, `fastcgi_temp_path`, `uwsgi_temp_path` and `scgi_temp_path`
  must point to its own existing directory of that name inside the prefix.
- A literal `127.0.0.1:PORT` listener matching the health URL, on port 1024–65535.
- An exact health location containing `add_header X-Cvke-Worker $pid always;`.

Includes, dynamic modules, upstreams/proxying, TLS, arbitrary file references,
variables other than the fixed worker header, and unsupported syntax are
refused. This is not yet an adapter for an arbitrary existing NGINX deployment.
The reproducible lab configuration is in `e2e/recovery_nginx_docker_test.go`.

Use `recovery services` to discover configured instances. A preparation request
selects `"service": "example"` and contains exactly one `replace` change for
the configured main file. Ordinary file actions cannot edit registered service
configs or their protected executable/PID files. Inspect and use the normal CLI
`apply`/`recover` commands with the exact digest. The agent tool must use
`apply_service`/`recover_service`; these require service-change and health-probe
permissions in addition to configuration staging/replacement permissions.
An ordinary file grant cannot authorize a service reload.

Under that approval, the executor records validation, checks baseline health,
runs `nginx -t` against the private candidate, installs the file, validates the
installed configuration and signals the bound master using a pidfd. The
predetermined status/body must verify on three fresh connections from a new
worker generation before the operation becomes `committed`. No proxy settings,
redirects, arbitrary URLs or model-generated validation commands are used.
Response bodies and NGINX diagnostic content are not persisted in the journal.

This follows NGINX's documented behavior: a reload can leave the old workers
serving when the proposed configuration cannot be applied. A successful signal
alone cannot establish that a new configuration is active. See the official
[reload documentation](https://nginx.org/en/docs/control.html) and
[configuration-test options](https://nginx.org/en/docs/switches.html).

The operator may supply `resource_limits` inside each service definition:

```yaml
resource_limits:
  max_workers: 4
  max_connections_per_worker: 4096
  max_total_connections: 8192
  max_resident_workers: 8
  descriptor_reserve: 64
```

These are the defaults when the whole block is absent. A supplied block must
specify all fields. The executor derives worker and connection counts from the
validated grammar, checks their product without using a model's estimate, and
captures that demand in the manifest. Immediately before validation/application,
it reads the bound master's CPU affinity, soft descriptor limit and live worker
children. New workers must fit the affinity count, existing plus proposed workers
must fit the reload-overlap budget, and connections per worker plus the descriptor
reserve must fit the inherited soft limit. Persisted `service_resources` evidence
shows successful and refused checks. No process limit or affinity is raised.

The relationship between connections and open files follows
[NGINX's worker documentation](https://nginx.org/en/docs/ngx_core_module.html#worker_connections).
Measurements use Linux [prlimit](https://man7.org/linux/man-pages/man2/getrlimit.2.html)
and [CPU affinity](https://man7.org/linux/man-pages/man2/sched_getaffinity.2.html).
These checks bound configuration and reload overlap; affinity is not a cgroup CPU
quota, and the checks do not predict workload memory/CPU consumption or reserve
resources against another process. Recovery is not stranded by a new-application
resource budget.

A validation failure before installation is recorded as `validation_failed`
with the target config unchanged. Failure after installation triggers **one**
bounded attempt to restore the original file, validate it, reload and verify
fresh worker health. Caller cancellation does not cancel that rollback;
process death still requires explicit CLI recovery. A dead/replaced master,
conflicting destination, missing backup or failed original health check is
recorded as `recovery_failed`. Restored file bytes alone are never reported as
a recovered service. The stored adapter remains usable with `--state` even
when the current app/provider configuration is unavailable.

This transaction does not restore interrupted client requests, other programs'
writes, account/package changes, runtime cache contents or external side
effects. Its rollback is in-process, with explicit offline recovery after process
death. Timed target-local/reboot recovery belongs to the managed SSH adapter below.

## Native Btrfs checkpoints (Linux amd64/arm64)

This adapter uses native Linux ioctls, not a shell wrapper. Configure a dedicated
Btrfs subvolume and a private sibling directory:

```yaml
recovery:
  snapshots:
    - name: work
      path: /srv/data/work
      store: /srv/data/.cvkeharness-snapshots
      max_entries: 1000
      max_logical_bytes: 268435456
```

The source must already be a writable, non-top-level subvolume, accessed through
its parent filesystem mount. A separately mounted subvolume cannot be exchanged
and is refused. The store must be an ordinary directory in the same parent
subvolume, owned by the executor and mode 0700. Its exact name is required.
State must live outside the protected tree. Unsupported kernels/filesystems,
missing permissions and invalid scope fail closed; there is no copy-based
fallback presented as a native snapshot.

```sh
cvkeharness recovery snapshot targets
cvkeharness recovery snapshot prepare work
cvkeharness recovery apply CHECKPOINT_ID --confirm CHECKPOINT_DIGEST
cvkeharness recovery snapshot restore-prepare CHECKPOINT_ID --checkpoint-digest CHECKPOINT_DIGEST
cvkeharness recovery apply RESTORE_ID --confirm RESTORE_DIGEST
```

Checkpoint preparation reviews the complete supported inventory. Application
rechecks it and creates a native read-only snapshot. Restoration gets a **new**
operation and approval, binding the current tree as well as the checkpoint.
It creates a writable clone and atomically exchanges that clone with the active
subvolume pathname. The entire displaced tree remains under
`STORE/RESTORE_ID.displaced`, including files added or edited after the checkpoint.
There is no automatic deletion of checkpoints or displaced trees.

The agent uses `snapshot_targets`, `snapshot_prepare`, `snapshot_restore_prepare`,
`apply_snapshot` and `recover_snapshot` actions of `recovery_manage`. Snapshot
mutations have distinct approval effects; a file-only action cannot execute a
whole-tree restore. Normal CLI `inspect`, `reconcile` and `recover` operate from
the persisted plan and work without provider access or current app configuration.

Coverage is deliberately bounded: at most 10000 entries, 1 GiB logical bytes,
64 MiB per file and depth 64. Regular files and ordinary directories preserve
contents, modes, ownership and modification times. Symlinks, hardlinks, special
files, unsupported ACLs/xattrs, nested subvolumes and directory/file bind mounts
are refused. Credential/repository/recovery paths and configured service files
cannot be included. The source's filesystem UUID, subvolume UUID, persistent
parent/store root IDs and inode identities bind the plan across reboot. A
read-only checkpoint's UUID and complete inventory are rechecked before restore.
Its generation counter is diagnostic: the kernel can change it when cloning a
read-only source, without changing the protected tree contents.

Apply records fresh filesystem capacity and requires conservative logical-copy
and metadata headroom plus the operator reserve. This does not reserve space,
guarantee Btrfs metadata allocation or turn shared extents into an independent
backup. [Btrfs documents snapshot scope and shared storage](https://btrfs.readthedocs.io/en/latest/btrfs-subvolume.html).

Stop external writers before checkpoint/restore. Inventory collection is not an
application-consistent freeze; a running database is outside this contract.
Open file descriptors continue to reference the displaced tree after exchange.
The adapter verifies both trees and reports unknown outcomes when they differ
from the plan, preserving artifacts for inspection. Native atomic exchange does
not make the surrounding checks atomic against unrelated writers or renames.
This adapter does not roll back a boot disk, other subvolumes, mounts, processes,
network configuration, or remote side effects. Losing the filesystem also loses
its local checkpoints.

## Genuine VM evidence

On an arm64 Docker host, build the owned lab image and run:

```sh
bash scripts/build-recovery-vm.sh
bash scripts/test-recovery-vm.sh
```

The image pins Alpine 3.23.4; its package repository resolves QEMU, the Linux
guest kernel and Btrfs utilities at image build time. Record the resulting image
ID/package versions for an exact repeat. The lab runs QEMU TCG with a separate
guest kernel, two disposable virtual disks, and generated SSH keys inside a
network-disabled container. No host directories, credentials, devices, ports or
Docker socket are exposed. SSH forwarding binds only the container loopback.
The build context contains only the reviewed lab files, and the compiled
instrumented CvkeHarness binary is copied into the owned container separately.

The VM tests prove a distinct kernel boot ID, actual SSH execution of the CLI,
file recovery after process death and guest reboot, and native Btrfs checkpoint
creation/restoration. They exercise snapshot-stage process death, restoration
after reboot without app config, later-writer retention/refusal, mounted-tree
refusal and checkpoint tampering. The explicit VM suite fails if its image is
missing; it does not silently skip native validation. The managed SSH suite
also exercises target-local timed rollback and recovery before listener startup
after guest reboot. The separate fleet controller uses enrolled SSH transports;
its multi-host evidence is container-based rather than a fleet reboot claim.

## Bounded remote batches

The controller connects to operator-enrolled literal IP addresses and invokes
the installed target-side CvkeHarness executable. It never interprets a target
path as a controller-host path. The first batch adapter supports one already-
prepared regular-file or NGINX operation per distinct machine/principal. SSH
listener changes and Btrfs operations retain their separate workflows.

Enrollment is an operator task: install the executable and configure recovery
roots/services on each target; obtain its host public key through a trusted
channel; run `cvkeharness recovery --state /STATE/state.db identity` there and
record the returned hash. Do not learn an unknown server's identity by accepting
its first SSH key. Create a dedicated client identity and a known-hosts file
inside the controller's protected state directory, each mode 0600 or stricter.
The known-hosts entry uses the expected executor hash as its alias:

```text
EXPECTED_TARGET_HASH ssh-ed25519 OPERATOR_VERIFIED_PUBLIC_KEY
```

Example controller settings (replace every placeholder with enrolled values):

```yaml
state_db_path: /var/lib/cvkeharness/state/state.db
security:
  version: 1
  profile: reasonable
  overrides:
    data.credential_access: ask
recovery:
  fleet:
    limits:
      max_hosts: 4
      max_files: 100
      max_bytes: 33554432
      max_services: 2
      max_repair_attempts: 2
      max_age_seconds: 900
    hosts:
      - name: server-one
        address: 192.0.2.10
        port: 22
        user: root
        expected_target: EXPECTED_TARGET_HASH
        ssh_binary: /usr/bin/ssh
        identity_file: /var/lib/cvkeharness/state/fleet/client-key
        known_hosts_file: /var/lib/cvkeharness/state/fleet/server-one-known
        executor: /usr/local/bin/cvkeharness
        state_path: /var/lib/cvkeharness/state/state.db
        timeout_seconds: 30
```

The default reasonable profile denies credential access. The example explicitly
allows asking to use this operator-configured identity; the feature does not
change that policy itself. Fleet mutation is additionally checked against remote,
file, service and network permissions. Model calls still require the exact scoped
approval. Non-root target accounts work only within their existing filesystem and
service permissions; the controller never adds sudo or changes privileges.

Prepare each operation with the target CLI, then create a controller request:

```json
[
  {"host":"server-one","id":"TARGET_OPERATION_ID","digest":"TARGET_OPERATION_DIGEST"}
]
```

```sh
cvkeharness recovery fleet prepare --request batch.json
cvkeharness recovery fleet inspect BATCH_ID
cvkeharness recovery fleet apply BATCH_ID --confirm REVIEWED_BATCH_DIGEST
cvkeharness recovery fleet reconcile BATCH_ID
cvkeharness recovery fleet recover BATCH_ID --confirm REVIEWED_BATCH_DIGEST
```

Preparation inspects each target's immutable manifest and calculates the total
files, original-plus-candidate bytes and service count. Host limits apply before
any connection. Budget failure changes no target files. The batch binds target
identity, operation/digest, transport configuration, SSH binary, key, known-hosts
fingerprints and controller policy. Every target is inspected before the first
dispatch and again immediately before its turn. The target executor independently
enforces live file, service, policy, age and capacity preconditions.

Dispatch is strictly serial and persisted before sending. A failed command,
timeout, lost connection or noncommitted result stops later hosts. A dispatched
batch can never be applied again, including after a controller crash. Reconcile
only reads remote journals; it does not resume the rollout. Seeing `ready` after
dispatch does not prove an earlier command cannot still execute, so it remains
unknown until an explicit recovery request resolves it. For a still-ready target
operation, recovery verifies the original files (and original service identity/
health where applicable), then records terminal cancellation under the target's
executor lock. A delayed apply of that same operation is subsequently refused.
No file is written by this cancellation. A changed original remains a conflict.
Direct inspection on the target remains available.

Recovery visits dispatched targets in reverse order. Each attempted repair is
persisted before transmission and consumes its slot even if the response is
lost. It stops on a failed/unknown repair. Claims prevent reusing the same target
operation in another batch in the same controller database, including an
undispatched entry of a stopped batch. A new rollout requires new target plans
and a new reviewed batch. These are per-operation/per-batch limits, not a
systemwide rate limit or an OS boundary against arbitrary root commands.

`recovery_fleet` exposes the same actions through agent approval/routing. Console
Overview shows unfinished batch outcomes, dispatch flags and repair counts;
opening it never acts. Saved transports keep inspection/restoration usable with
`--state` when app/provider configuration is absent. Changed/lost keys or a
changed SSH binary are refused; use the operator's direct target recovery path
if the original transport cannot be restored safely. Target file conflicts are
preserved, and a failed batch is not falsely described as globally atomic.

Connections ignore ambient SSH configuration and agents, disable forwarding and
multiplexing, require strict host-key verification and use one configured key.
The remote command is fixed typed CLI syntax with literal paths and validated
IDs, never model-provided shell text. This uses OpenSSH's documented
[remote command behavior](https://man.openbsd.org/ssh.1) and
[host-key/configuration controls](https://man.openbsd.org/ssh_config.5).

## Managed SSH with revert-unless-confirmed recovery

The initial Linux amd64/arm64 workflow changes only the port of a **dedicated,
root-owned OpenSSH instance** supervised by CvkeHarness. It requires OpenSSH's
separate `sshd-session` executable, kernel procfs/socket evidence, boot-relative
time and pidfd signaling. It does not adopt an arbitrary running daemon or edit
accounts, keys, authentication, addresses, firewall rules or routes.

Provision the instance's existing private host key, authorized public keys and
standalone configuration as the operator. No account or key provisioning is
performed by a model tool. Put a private root-owned JSON startup specification
inside the state directory, for example `/var/lib/cvkeharness/ssh-service.json`:

```json
{
  "name": "management",
  "binary": "/usr/sbin/sshd",
  "session_binary": "/usr/lib/ssh/sshd-session",
  "config_path": "/srv/managed-ssh/sshd_config",
  "pid_file": "/run/cvkeharness-sshd.pid",
  "host_key_path": "/srv/managed-ssh/host_key",
  "authorized_keys_path": "/srv/managed-ssh/authorized_keys",
  "timeout_seconds": 5,
  "confirmation_seconds": 30
}
```

The executable paths are installation-specific and must point to the actual
regular executables, without symlinks. Files must be root-owned and not writable
by group/others; the host key and startup specification must be private. The
standalone configuration must contain exactly the supported fixed directives
(blank lines and full-line comments are allowed):

```text
Port 2222
ListenAddress 0.0.0.0
HostKey /srv/managed-ssh/host_key
PidFile /run/cvkeharness-sshd.pid
AuthorizedKeysFile /srv/managed-ssh/authorized_keys
PermitRootLogin prohibit-password
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitEmptyPasswords no
AllowUsers root
AllowTcpForwarding no
X11Forwarding no
PermitTunnel no
UseDNS no
Subsystem sftp internal-sftp
```

Unknown/duplicate directives, includes, `Match`, command hooks and changed fixed
values are refused. The proposed port must differ and be between 1024 and 65535;
an existing privileged port can still be restored from its original manifest.
This is a bounded additional SSH service. It must have a free baseline port and
a service manager that owns its startup and restart.

Run the supervisor through that target's init manager:

```sh
/usr/local/bin/cvkeharness recovery --state /var/lib/cvkeharness/state.db ssh supervise --spec /var/lib/cvkeharness/ssh-service.json
```

Configure init to start this command after its persistent filesystems mount and
restart it after failure. It starts and owns the foreground `sshd`; **the SSH
listener must not be started independently before the supervisor's recovery
pass**. The supervisor restores a persisted unconfirmed operation before it
launches SSH on startup, including after reboot. The lab's actual `/init` loop
implements and tests that contract. No systemd unit, arbitrary distribution
startup integration or machine-wide boot recovery has been tested yet. If init
does not start/restart this command, automatic recovery cannot run while the
supervisor is down. After a recorded restoration failure, resolve the conflict
and run explicit recovery before restarting it.

Add the identical service fields to operator configuration under
`recovery.ssh_services`, and its configuration directory to `recovery.roots`.
`recovery ssh services` lists configured instances. A normal `prepare` request
selects `"ssh_service": "management"` and one exact `replace` change for the
standalone configuration. Only `Port` may change. Registered key/executable/PID
paths are protected; registered configs require this typed transaction.

```sh
cvkeharness recovery prepare --request ssh-port-change.json
cvkeharness recovery apply OPERATION_ID --confirm REVIEWED_DIGEST
# Establish a fresh SSH connection to the proposed port, then run there:
cvkeharness recovery ssh confirm OPERATION_ID --confirm REVIEWED_DIGEST
```

Application requires a matching live supervisor with a fresh heartbeat, fixed
executable/authentication identities, unchanged target contents, capacity and
syntax checks. The executor persists the exact pending operation and deadline
**before modifying the target**. Heartbeats update a different database field
and cannot erase that deadline. The state directory serializes mutations, and
other recoverable mutations/preparations are refused while SSH recovery is
armed. The confirmation window is operator-defined, 15–300 seconds from arming.

The watchdog uses Linux `CLOCK_BOOTTIME`, including suspend time, rather than
wall-clock arithmetic. A different boot or supervisor startup causes pending
recovery before listener startup. `inspect` includes `ssh_deadline` evidence,
its boot identity and an estimated UTC revert time for display; the estimate is
not the clock that controls decisions. See Linux's
[clock definitions](https://man7.org/linux/man-pages/man2/clock_gettime.2.html).

A successful reload becomes `awaiting_confirmation`, not `committed`. The
executor checks that the bound master owns the proposed listening socket and
answers with an SSH banner. Confirmation additionally walks the confirming
process's kernel ancestry to that master, checks newly created `sshd-session`
processes and an established socket on the proposed port, and rechecks the
deadline and file/identity preconditions. An old multiplexed transport or a
spoofed `SSH_CONNECTION` variable cannot satisfy this proof. No raw addresses,
keys, environment data or model-generated verification command are persisted.
OpenSSH documents its [listener/session split](https://www.openssh.org/txt/release-9.8)
and [configuration testing and reload behavior](https://man.openbsd.org/sshd.8).

The agent uses `apply_ssh`, `confirm_ssh` and `recover_ssh` with separate scoped
service/network/file effects. A file-only grant cannot authorize them. The
current agent process cannot confirm through its own old SSH transport; use the
new connection and the model-independent CLI. Console exposes the pending
state and confirmation instructions; opening its details performs no action.

If no confirmation arrives, the supervisor makes one deterministic restoration
attempt, validates the original file, reloads and checks the original listener.
It retains a conflicting writer's bytes and records `recovery_failed` instead
of repeatedly repairing. `recovery recover ID --confirm DIGEST` permits explicit
recovery after the operator resolves a conflict. Without a live listener, that
command can restore/validate the file but leaves `rolling_back` until supervisor
startup verifies the running original service. Provider access and current app
configuration are unnecessary when `--state` is supplied.

Coverage includes the managed configuration and original listener readiness.
It does not restore interrupted requests or compensate for external key,
account, filesystem, routing or firewall changes. An unresponsive kernel, lost
storage or a stopped init manager can prevent execution of the watchdog; keep
the target's ordinary out-of-band recovery route. General shell commands do not
gain this adapter's recovery coverage.

After provisioning the documented lab images, the complete verification entry
point is `bash scripts/test-recovery-all.sh`. It runs local tests, race checks,
vet, compiled CLI/PTY/provider-loop journeys, Docker fault tests and genuine VM
tests, and fails rather than silently skipping unavailable VM prerequisites.
