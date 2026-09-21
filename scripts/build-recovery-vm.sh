#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# The context contains only reviewed lab scripts and the Dockerfile, never
# the repository, credentials or the user's home directory.
exec docker build --pull=false --progress=plain -t cvkeharness-recovery-vm:alpine323 "${script_dir}/../e2e/labvm"
