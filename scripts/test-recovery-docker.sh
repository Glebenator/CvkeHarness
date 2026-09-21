#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${script_dir}/.."
exec go test -count=1 -tags=e2e,recoverydocker ./e2e -run '^TestRecoveryDocker' -v "$@"
