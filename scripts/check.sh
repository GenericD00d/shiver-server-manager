#!/usr/bin/env bash
# The manager's local checks, the same ones CI's build job runs. Run before pushing:
#
#   scripts/check.sh           everything (needs Go with cgo, libpam0g-dev or pam-devel, Node and npm)
#   scripts/check.sh --quick   the docs and gofmt
#
# The end-to-end tests need root, PAM accounts and real systems: CI runs them, and test/vm/run.sh
# runs them in a VM.
set -euo pipefail
cd "$(dirname "$0")/.."

quick=false
[ "${1:-}" = "--quick" ] && quick=true

step() { printf '\n== %s\n' "$*"; }

step "Docs follow the standard"
python3 scripts/check-docs.py

step "gofmt"
unformatted=$(gofmt -l cmd internal)
if [ -n "$unformatted" ]; then
  echo "$unformatted"
  exit 1
fi

if $quick; then
  printf '\nQuick checks passed.\n'
  exit 0
fi

step "The web page (the binary embeds its build)"
(cd internal/web/ui && npm ci --no-audit --no-fund && npm run build)

step "go vet"
go vet ./...

step "Unit tests"
go test ./...

step "Modules match go.sum"
go mod verify

step "Known vulnerabilities"
if command -v govulncheck >/dev/null 2>&1; then
  govulncheck ./...
else
  echo "govulncheck isn't installed (go install golang.org/x/vuln/cmd/govulncheck@latest); CI runs it"
fi

step "The sudoers file"
if command -v visudo >/dev/null 2>&1; then
  visudo -cf packaging/sudoers
else
  echo "visudo isn't installed; CI checks the file"
fi

printf '\nAll checks passed.\n'
