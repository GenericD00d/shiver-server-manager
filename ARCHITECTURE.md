# Architecture

The map of the manager for agents and contributors. Update it in the same commit as any change that
adds, removes, renames or moves a package, command or file it lists.

## Overview

One Go binary, run three ways:

- **By an admin**, as `sudo shiver-server-manager`. With no arguments it opens the terminal menu;
  with arguments it runs a command. Either way it logs the admin in through PAM first.
- **By the timers**, as root with no sudo user: the health check every 5 minutes and the backup
  check every 5 minutes. They skip the login.
- **As the web page**, a root service on `127.0.0.1:8443` (`shiver-server-manager-web`), off until
  `web enable`. Each action it offers runs the same command as a child process.

The menu, the commands and the web page all call `internal/ops`, which does everything to the
system as resumable steps. Each Sharkord server runs as its own system user, under the shared unit
`sharkord@.service` plus per-server drop-ins.

```
  admin ── sudo ──► shiver-server-manager ── PAM login ──► menu / commands
                                                                │
  browser ── SSH tunnel or Caddy ──► web page (root) ── runs ───┤
                                                                │
  timers (root, no login): health, backups ─────────────────────┤
                                                                ▼
                                                     internal/ops: resumable steps
                                                                │
        ┌──────────────┬───────────────┬──────────────┬─────────┴────┬────────────┐
     systemd        Caddy          firewall        storage, LUKS   backups     vault
  sharkord@<id>   /etc/caddy      ufw, firewalld    data folders    age, zstd   age
     │
     └─ each server: user sharkord-<id>, its data folder, its ports
```

What each boundary in that picture guards against is in
[SECURITY.md](SECURITY.md#trust-boundaries).

## Layout

```
cmd/shiver-server-manager/   main: the menu with no arguments, otherwise a command
internal/                    one package per job (below)
internal/web/ui/             the web page's front end: React 19, Vite, Tailwind, Sharkord's UI components
internal/web/dist/           its build, committed, embedded in the binary
internal/help/               the "Steps outside the manager" text, embedded
packaging/                   nfpm.yaml, build.sh (builds inside AlmaLinux 8), install scripts,
                             sharkord@.service, the timers, the web service, sudoers and PAM files
test/browser/                Playwright: a voice call between two browsers, and the web page
test/vm/                     the end-to-end tests in a cloud image under KVM
test/install-check.sh        installs, checks and removes a package
docs/                        admin, member and troubleshooting guides, tested versions, decisions,
                             standards.md; docs/design/ holds the original guide and checklist
plugins.json                 the Shiver plugins on offer: repo, asset pattern, checksum file
scripts/                     check.sh, check-docs.py
```

## Modules

| Package | Job |
|---|---|
| `cmd/shiver-server-manager` | The entry point. Starts the menu with no arguments, otherwise runs a command |
| `internal/cli` | The command tree, flags, `--json` output and exit codes. Commands parse flags and call `internal/ops`; the menu calls the same functions. `session.go` is the login, `opsrun.go` the shared `withOps` |
| `internal/tui` | The terminal menu: every action collects its inputs with a form, shows what will change, and runs the same command an admin could type |
| `internal/web` | The web page: a small HTTP server on the loopback address. `actions.go` maps named actions to fixed commands; `sessions.go`, `jobs.go`, `upload.go`, `static.go` |
| `internal/auth` | The login: an OS username and password checked through PAM, membership of the admins group, a pause after repeated failures, and the idle lock |
| `internal/vault` | Secrets (owner tokens, data-folder keys) encrypted with age. The vault's own X25519 identity is stored encrypted with the vault passphrase; shown once at setup, it is the recovery key |
| `internal/state` | The records: what should exist on the system. Repair changes the system to match them, never the reverse. Also the lock file |
| `internal/ops` | Everything the manager does to the system, as resumable steps (`engine.go`): create, delete, update, switch type, move, unlock, repair, backups, restore, import, plugins, health |
| `internal/sharkord` | Sharkord itself: its releases (checked against their `release.json`), env settings, first start, owner token, health endpoint, and the tested versions (`tested.go`) |
| `internal/systemd` | The per-server drop-ins for `sharkord@.service`, and controlling units through `systemctl` |
| `internal/caddy` | Caddy's configuration as files under `/etc/caddy`, applied with validate and reload. Never Caddy's admin API |
| `internal/firewall` | The manager's rules, through ufw or firewalld |
| `internal/netinfo` | The public address, DNS checks, carrier-grade NAT, and UPnP port mapping on home routers |
| `internal/storage` | Storage locations: folders on drives that come back after a reboot, with a file system that allows running programs |
| `internal/luks` | Each encrypted server's container: a LUKS2 file holding ext4, mounted on its data folder. Keys are 64 random bytes and reach cryptsetup on stdin |
| `internal/backup` | The one archive format, for backups and exports alike: tar, compressed with zstd, encrypted with age. Backups go to the vault's public key, exports to a passphrase |
| `internal/plugins` | Fetching, checking and unpacking plugins: the companion plugin from Shiver's releases (`shiver.go`), the others from `plugins.json` (`catalog.go`), and archives (`archive.go`) |
| `internal/selfupdate` | The manager's own latest release, and its package checked against the release's `SHA256SUMS` |
| `internal/osuser` | OS accounts: the admins group and each server's system user |
| `internal/run` | The one way to run external programs: an argument list, a timeout, a fixed environment, a log line per command, secrets redacted. `fake.go` stands in for tests |
| `internal/sysenv` | Process setup before anything else runs: keeps the few variables needed, drops the rest, sets umask 077, and checks the manager is running safely as root |
| `internal/fsutil` | The one way the manager writes files: atomic, with explicit owner and mode |
| `internal/validate` | Checks input before it goes into any name or path |
| `internal/paths` | Every name and fixed location the manager uses |
| `internal/actionlog` | One log line per action and per external command, with the time and the logged-in user |
| `internal/distro` | The supported distributions: how each installs packages, which firewall it usually uses, which package the manager comes in |
| `internal/httpx` | Retries HTTP requests that fail on the way |
| `internal/help` | The "Steps outside the manager" text |
| `internal/testutil` | Helpers shared by tests |

## Flows

### Logging in

1. `sysenv.Setup` keeps `SUDO_USER`, `TERM` and `NO_COLOR`, drops the rest of the environment,
   fixes `PATH`, and sets umask 077. `CheckOwnership` refuses to go on if the binary or the
   manager's folders can be changed by anyone but root.
2. `PersistentPreRunE` lets only `version`, `help`, `completion` and the hidden `internal-*`
   commands through without root. The `internal-*` commands refuse root themselves (`refuseRoot`).
3. A command that does anything calls `login`, usually through `withOps`. Started through sudo, it
   runs PAM for `SUDO_USER`, then checks membership of `shiver-admins` (`auth.Login.Run`). Started
   by root with no sudo user, as the timers are, it skips the login.

### Creating a server

The steps are in `internal/ops/create.go`, as the design guide's
[Create, step by step](docs/design/implementation-guide.md#create-step-by-step) describes. They add the
record, create the system user, download and check Sharkord, create the data folder or its LUKS
container, write the env file and drop-ins, capture the owner token into the vault, start the
service, add the firewall rules and the Caddy site, and mark the record active. Each step can be
resumed.

### A web action

1. The browser posts a named action, with `X-Requested-By` and a same-origin `Origin`.
2. `internal/web/actions.go` turns it into a fixed argument list, validating every input. Nothing
   from the browser becomes a command line.
3. `Jobs` runs the manager itself as a child process: as root, with no sudo user, with
   `--yes --passphrase-stdin --acting-user <admin>`. Secrets go on stdin and are blanked out of the
   job's output.

### Backups and restores

- **A backup** copies the database first, with `VACUUM INTO` run as the server's own user
  (`internal-vacuum`). It then streams tar into zstd into age, to the backup location, and records
  the archive in the index.
- **A restore** stops the server, then unpacks the archive into a staging folder inside the data
  folder, writing only through an `os.Root` on that folder. It gives the files to the server's user,
  swaps the folders with renames, starts the server, and swaps back if it doesn't answer.

### Installing a plugin

1. The release is found from `plugins.json`, or from Shiver's latest release for the companion
   plugin. The archive is downloaded and checked against its release's checksums.
2. The server is backed up and stopped. The archive is then unpacked into a staging folder through
   an `os.Root`, and its manifest checked against the tested plugin SDK.
3. The plugin folder is swapped in and given to the server's user. On a first install, plugins are
   switched on in Sharkord's database, as the server's own user (`internal-enable-plugins`).
4. The server is started. If Sharkord doesn't log the plugin as loaded, the old copy goes back.

## Contracts

- **Commands**: the menu, the web page and scripts all run the same commands. `docs/admin-guide.md`
  documents them, and `TestEveryGuideCommandExists` checks the design's list exists.
- **The state file** (`state.json`, `internal/state`) has a schema version. Repair treats it as the
  truth.
- **Files on the system**: "What the manager changes on the system" in `docs/admin-guide.md`.
- **Hidden commands** run as a server's own user: `internal-vacuum <src> <dst>`,
  `internal-enable-plugins <db> [<plugin-id>]` and `internal-set-token <db>`. They refuse root.
- **Sharkord releases**: the assets `sharkord-linux-x64` and `sharkord-linux-arm64`, checked against
  the release's `release.json`.
- **The Shiver plugins**: `plugins.json`, and Shiver's `shiver-plugin-<version>.zip` with
  `SHA256SUMS.txt`. See
  [docs/standards.md](docs/standards.md#contracts-between-the-repos).
- **The manager's own releases**: packages named as `selfupdate.PackageName` expects, and
  `SHA256SUMS`.
