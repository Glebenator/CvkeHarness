#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${script_dir}/.."
# Requires the documented Docker images and an arm64 Docker host. The explicit
# VM suite fails when unavailable; it must not become a silently skipped gate.
go test -count=1 ./...
go test -race ./...
go vet ./...
exec go test -count=1 -tags=e2e,recoverydocker,recoveryvm ./e2e -v "$@"
