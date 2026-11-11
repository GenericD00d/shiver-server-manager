#!/bin/sh
# Builds the manager (and, with TESTS=1, the PAM and end-to-end test
# binaries) inside AlmaLinux 8, whose glibc (2.28) is older than every
# supported system's, so one build runs on all of them. PAM needs cgo.
#
#   docker run --rm -v "$PWD:/src" -v "$(go env GOROOT):/goroot:ro" \
#     -v "$(go env GOMODCACHE):/gomod" almalinux:8 sh /src/packaging/build.sh <version>
set -eu
version=$1
dnf -y -q install gcc pam-devel >/dev/null
export PATH=/goroot/bin:$PATH GOROOT=/goroot GOMODCACHE=/gomod GOCACHE=/tmp/gocache GOTOOLCHAIN=local GOFLAGS=-buildvcs=false CGO_ENABLED=1
cd /src
go build -trimpath -ldflags "-s -w -X main.version=$version" -o shiver-server-manager ./cmd/shiver-server-manager
if [ "${TESTS:-}" = 1 ]; then
  go test -c -o auth.test ./internal/auth
  go test -c -o cli.test ./internal/cli
fi
# The newest glibc symbol each binary needs.
for b in shiver-server-manager auth.test cli.test; do
  [ -f "$b" ] && echo "$b: $(objdump -T "$b" | grep -o 'GLIBC_[0-9.]*' | sort -uV | tail -1)"
done
chown "$(stat -c %u:%g /src)" shiver-server-manager auth.test cli.test 2>/dev/null || true
