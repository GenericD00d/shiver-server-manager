# Shiver Server Manager: agent guide

## What this repo is

A Go program that sets up, runs, backs up, imports, exports and deletes several
[Sharkord](https://github.com/sharkord/sharkord) servers on one Linux machine. Each server gets its
own domain, its own system user (`sharkord-<id>`) and a hardened systemd unit, with Caddy handling
HTTPS. Admins drive it from a terminal menu, from the same actions as commands, or from a web page.

**It runs as root.** The sudoers rule lets any member of `shiver-admins` start it as root with any
arguments and no sudo password, so the manager's own PAM login is the real gate. Read SECURITY.md
before changing anything in [Rules that must not break](#rules-that-must-not-break).

## The Shiver family

The four Shiver repos, and the contracts between them, are in
[docs/standards.md](docs/standards.md#the-shiver-family). This repo's side of them:

- `plugins.json` names each Shiver plugin's repo, release asset pattern and checksum file. The
  plugins' release names have to match it.
- `internal/plugins/shiver.go` installs the companion plugin from Shiver's releases
  (`shiver-plugin-<version>.zip` and `SHA256SUMS.txt`).
- `internal/sharkord/tested.go` lists the tested Sharkord versions and their plugin SDK. It moves
  with Shiver's `SHARKORD_REF`.

## Commands

```bash
scripts/check.sh --quick    # docs and gofmt: seconds
scripts/check.sh            # + vet, tests, module checks, vulnerability scan, the web page build
```

The pieces:

```bash
cd internal/web/ui && npm ci && npm run build && cd -   # first: the binary embeds internal/web/dist
CGO_ENABLED=1 go build ./cmd/shiver-server-manager      # PAM needs cgo, and libpam0g-dev or pam-devel
go vet ./...
go test ./...                                           # unit tests; Linux-only code has stand-ins
python3 scripts/check-docs.py                           # the docs follow docs/standards.md
SSM_WEB_DEMO=1 go test ./internal/web -run TestDemo -timeout 0   # the web page with sample data
```

The demo serves http://127.0.0.1:8443 (log in as admin, password demo). The end-to-end tests need
root, PAM accounts and real systems. CI runs them on every push: the `e2e-ubuntu` and `e2e-vm` jobs
in `.github/workflows/ci.yml`. `test/vm/run.sh` runs them in a VM locally. Releases are in
[RELEASING.md](RELEASING.md).

## Layout

```
cmd/shiver-server-manager/  the entry point: the menu with no arguments, otherwise a command
internal/                   one package per job; ARCHITECTURE.md lists them
internal/web/ui/            the web page's front end (React); internal/web/dist is its committed build
packaging/                  nfpm config, install scripts, systemd units, sudoers and PAM files
test/                       browser tests, the package install check, and the VM test runner
docs/                       the admin, member and troubleshooting guides; decisions; the original design
plugins.json                the Shiver plugins on offer
```

## Rules that must not break

SECURITY.md explains why each one matters.

| Rule | Enforced in | Tested by |
|---|---|---|
| Every command except `version`, `help` and `completion` logs in when started through sudo | `PersistentPreRunE` in `internal/cli/cli.go`, `login` in `internal/cli/session.go` | `TestNonLinuxOrNonRootRefused`, the e2e login tests |
| Hidden `internal-*` commands refuse root, because the sudoers rule allows any arguments | `refuseRoot` in `internal/cli/cli.go` | `TestInternalCommandsRefuseRoot` |
| The environment is dropped and `PATH` fixed before anything runs, and the manager refuses to start if its own files are writable by anyone but root | `internal/sysenv/` | Review |
| External programs run only through `internal/run`: an argument list, never a shell; a timeout; secrets redacted | `internal/run/run.go` | `internal/run/run_test.go` |
| Secrets never go in a command's arguments, where other users can read them from `/proc`. They go on stdin | `internal/luks/`, `internal/run/` | `internal/luks/luks_test.go` |
| Input is validated before it goes into any name or path | `internal/validate/` | Its tests |
| Root reads and writes a server's data folder only through `os.Root`, and unpacks into staging folders the server's user can't write | `internal/backup/archive.go`, `internal/plugins/archive.go`, `stagingDir` in `internal/ops/backup.go`, `swapPlugin` in `internal/ops/shiver.go` | `TestReadDoesNotFollowSymlinksInDest`, `TestUnpackDoesNotFollowSymlinksInDest`, `TestStagingFoldersStayRootOwned` |
| Archives may hold only plain files and folders, with relative paths, under size caps | `internal/backup/archive.go`, `internal/plugins/archive.go` | `internal/backup/backup_test.go`, `internal/plugins/archive_test.go` |
| The web page refuses unknown Host headers, demands same-origin and its own header for changes, and never says why a login failed | `internal/web/server.go` | `TestRequestGuards`, `TestLogin` |
| The browser never sends a command line: each web action maps to a fixed command with validated inputs | `internal/web/actions.go` | `TestActionsBuildFixedCommands`, `TestEveryWebActionIsARealCommand` |
| Downloads are checked against the checksums their release publishes | `internal/sharkord/releases.go`, `internal/selfupdate/`, `internal/plugins/catalog.go` | Their tests |

## Conventions

- **The rules in the design guide** apply to every part: running commands, writing files, secrets,
  locking, checking input and running as root safely. See
  [Rules every part follows](docs/design/implementation-guide.md#rules-every-part-follows).
- **Names and fixed paths** live in `internal/paths/paths.go`, so a rename touches one file.
- **Linux-only code** sits behind build tags (`_linux.go`), with stand-ins (`_other.go`) so the rest
  builds and tests on Windows.
- **Operations are resumable steps** (`internal/ops/engine.go`). The state file says what should
  exist, and repair changes the system to match it, never the reverse.
- **Every generated file** starts with "Managed by shiver-server-manager" where its format allows.
- **The menu and the web page** run the same commands an admin could type. Nothing is implemented
  twice.
- **Docs** use British spelling. **Commits** have a plain-English subject and a body saying why.

## When you change something

- **A new command**: it logs in, through `withOps` or `login`. A new hidden `internal-*` command
  calls `refuseRoot` first. Document it in `docs/admin-guide.md`.
- **A new web action**: map it in `internal/web/actions.go` with every input validated.
  `TestEveryWebActionIsARealCommand` checks it is a real command.
- **A new file or setting on the system**: add it to "What the manager changes on the system" in
  `docs/admin-guide.md`.
- **A new tested Sharkord version**: `internal/sharkord/tested.go` and `docs/tested-versions.md`.
- **Root touching a server's data folder**: follow the data-folder rule above, and add a test that
  plants a link.
- **`plugins.json`, or a plugin's release names**: change both sides in the same release.
- **Anything users will notice**: add a line under `## Unreleased` in CHANGELOG.md.
- **A decision or a bug worth remembering**: `docs/decisions.md`.
- **docs/standards.md** changes only as its last section describes.

## Further reading

- [ARCHITECTURE.md](ARCHITECTURE.md): the packages, the main flows and the contracts
- [SECURITY.md](SECURITY.md): trust boundaries, scope and review history
- [RELEASING.md](RELEASING.md): building, publishing and verifying a release
- [docs/admin-guide.md](docs/admin-guide.md): every menu option, and everything the manager changes
- [docs/decisions.md](docs/decisions.md): decisions, open items and the bug log
- [docs/design/implementation-guide.md](docs/design/implementation-guide.md): the original design
- [docs/standards.md](docs/standards.md): the standard all four Shiver repos follow
