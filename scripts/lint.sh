#!/usr/bin/env sh

set -eu

repo_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$repo_root"

expected_version="${GOLANGCI_LINT_VERSION:-$(cat .golangci-version)}"
go_version="$(awk '/^go / { print $2; exit }' go.mod)"
gobin="$(go env GOBIN)"
gopath="$(go env GOPATH)"
cache_root="${TMPDIR:-/tmp}"
lint_cache="${GOLANGCI_LINT_CACHE:-${cache_root%/}/primitivebox-golangci-lint-cache}"
go_build_cache="${GOCACHE:-${cache_root%/}/primitivebox-go-build-cache}"
lint_timeout="${GOLANGCI_LINT_TIMEOUT:-5m}"

if [ -n "${gobin}" ]; then
  PATH="${gobin}:${PATH}"
elif [ -n "${gopath}" ]; then
  PATH="${gopath}/bin:${PATH}"
fi

export PATH
mkdir -p "${lint_cache}"
mkdir -p "${go_build_cache}"
export GOLANGCI_LINT_CACHE="${lint_cache}"
export GOCACHE="${go_build_cache}"

if ! command -v golangci-lint >/dev/null 2>&1; then
  echo "golangci-lint is not installed." >&2
  echo "Expected version: ${expected_version}" >&2
  echo "Install it with:" >&2
  echo "  go install github.com/golangci/golangci-lint/cmd/golangci-lint@${expected_version}" >&2
  exit 127
fi

echo "Using golangci-lint: $(golangci-lint version | head -n 1)"
echo "Lint target Go version: ${go_version}"
echo "golangci-lint cache: ${GOLANGCI_LINT_CACHE}"
echo "Go build cache: ${GOCACHE}"
echo "Lint command: golangci-lint run --timeout=${lint_timeout} --go=${go_version} ./..."

exec golangci-lint run --timeout="${lint_timeout}" --go="${go_version}" ./...
