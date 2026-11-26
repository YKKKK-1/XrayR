#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 1 ]]; then
  echo "Usage: GOOS=linux GOARCH=amd64 bash release/compat/build.sh OUTPUT" >&2
  exit 2
fi

output="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
root="$(cd "$(dirname "$0")/../.." && pwd)"
temp_parent="$(cd "${TMPDIR:-/tmp}" && pwd -P)"
stage="$(mktemp -d "$temp_parent/xrayr-compat.XXXXXXXX")"
[[ "$(cd "$stage/.." && pwd -P)" == "$temp_parent" && -d "$stage" && ! -L "$stage" ]]
trap 'rm -rf -- "$stage"' EXIT
export GOWORK=off CGO_ENABLED=0

cd "$root"
module=github.com/xtls/reality
expected=v0.0.0-20261108101329-4bb46b2afd8b
if [[ "$(go list -m -f '{{.Version}}' "$module")" != "$expected" ]]; then
  echo "Review the REALITY compatibility patch against the new dependency first." >&2
  exit 1
fi
go mod download "$module"
module_dir="$(go list -m -f '{{.Dir}}' "$module")"
if command -v cygpath >/dev/null; then
  module_dir="$(cygpath -u "$module_dir")"
fi

# Compile the application in a disposable module, with a relative replacement
# so the build does not embed the host's temporary path.
mkdir "$stage/src"
cp -R api app cmd common panel service main.go go.mod go.sum "$stage/src/"
cd "$stage/src"
cp -R "$module_dir" reality-compat
chmod -R u+w reality-compat
(cd reality-compat && git apply "$root/release/compat/reality-x25519.patch")
go mod edit "-replace=$module=./reality-compat"
GOOS="$(go env GOHOSTOS)" GOARCH="$(go env GOHOSTARCH)" \
  go test -mod=readonly "$module" -run '^TestLegacyX25519Hello$' -count=1
go build -mod=readonly -trimpath \
  -ldflags="-s -w -buildid= -X github.com/XrayR-project/XrayR/cmd.codename=XrayR-REALITY-X25519-compat" \
  -o "$output" .
