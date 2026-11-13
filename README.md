<p align="center"><img src="logo.png" alt="Shiver Server Manager" width="160"></p>

# Shiver Server Manager

Sets up, runs, backs up, imports, exports and deletes several [Sharkord](https://github.com/sharkord/sharkord) servers on one machine, each on its own domain, from a terminal menu, with Caddy handling HTTPS. It also installs and updates the [Shiver](https://github.com/GenericD00d/Shiver) plugins (Shiver, Temp Share, Media Player), listed in `plugins.json`.

Not an official Sharkord project.

## A disclaimer worth reading

**This project is vibecoded.** Almost all of it was written by an LLM working from one person's direction, review and bug reports.

- **No independent security audit has been done.** It runs as root, and holds each server's owner token, its data-folder key and its backups. Read the code before you trust a machine to it. Its internal reviews are listed in [SECURITY.md](SECURITY.md#review-history); they are not an outside audit.
- **The packages are unsigned.** Self-update checks a package against the `SHA256SUMS` published in the same release, which catches a damaged download but not a compromised GitHub account. Signing is planned ([RELEASING.md](RELEASING.md#keys)).
- It is early software. Expect rough edges, and expect things to change.

It is offered as-is.

## What it does

- **Servers**: each on its own domain and system user, with HTTPS from Let's Encrypt or a self-signed root, its own voice ports, resource limits, and updates with rollback.
- **Encrypted data folders** (LUKS), unlocked after a reboot from the vault.
- **Backups** on each server's own schedule, restores, exports, and imports from another install or a Docker container.
- **Storage locations** on other drives, and moving a server between them.
- **The firewall** (ufw or firewalld) and, on a home network, port mapping by UPnP.
- **Owner tokens** kept in an encrypted vault, shown only after the password is entered again.
- **A health check** every 5 minutes, with email or webhook alerts, and **repair**, which makes the system match the manager's records.
- **The Shiver plugins**, installed and updated per server.
- **A web page** with everything the menu does, for servers without a desktop.

## Install

Supported, on amd64 and arm64: Ubuntu 24.04 and 26.04 (and Linux Mint 22), Debian 12 and 13, Fedora 43 and newer, RHEL, AlmaLinux and Rocky Linux 9 and 10, and openSUSE Tumbleweed and Leap 16.

    sudo apt install ./shiver-server-manager_<version>_<arch>.deb       # Ubuntu, Mint, Debian
    sudo dnf install ./shiver-server-manager-<version>-1.<arch>.rpm     # Fedora, RHEL, AlmaLinux, Rocky
    sudo shiver-server-manager

(openSUSE: `sudo zypper install --allow-unsigned-rpm ./shiver-server-manager-<version>-1.<arch>.rpm`.)

## Usage

`sudo shiver-server-manager` opens the menu, after logging you in with your OS password; the first run walks through setup. The same actions are commands: `sudo shiver-server-manager --help`.

There's also a web page for servers without a desktop (`web enable`, then an SSH tunnel to port 8443, or an admin domain through Caddy).

The [admin guide](docs/admin-guide.md) covers every menu option.

## Settings

Everything is set from the menu or its command. Each server's settings live in `/etc/shiver-server-manager/instances/<id>.env`, and the manager's own records in `/var/lib/shiver-server-manager/state.json`; edit them through the manager, not by hand, since repair treats its records as the truth. The admin guide lists every setting, and [everything the manager changes on the system](docs/admin-guide.md#what-the-manager-changes-on-the-system).

## Known limitations

- Every published Sharkord release is offered, newest by default. The ones checked by hand are in [tested versions](docs/tested-versions.md); a newer one is assumed to work like the newest of those, and an update that doesn't start rolls back.
- Removing the package removes the shared unit, so servers don't start after the next reboot until it is reinstalled.
- Arch Linux isn't supported: it no longer packages Caddy.
- The manual checks still to do are listed under [Open items](docs/decisions.md#open-items).

## Docs

- [Admin guide](docs/admin-guide.md): installing, first-time setup, every menu option, and everything the manager changes on the system
- [Member guide](docs/member-guide.md): installing a self-signed server's root certificate
- [Troubleshooting](docs/troubleshooting.md)
- [Tested versions](docs/tested-versions.md)
- [Changelog](CHANGELOG.md)

For whoever works on it:

- [AGENTS.md](AGENTS.md): the checks to run, the rules not to break, and the conventions
- [ARCHITECTURE.md](ARCHITECTURE.md): the packages, the main flows and the contracts
- [SECURITY.md](SECURITY.md) and [RELEASING.md](RELEASING.md)
- [docs/decisions.md](docs/decisions.md): decisions, differences from the guide, the bug log, and manual checks still to do
- [docs/design/checklist.md](docs/design/checklist.md): what the manager does, and [docs/design/implementation-guide.md](docs/design/implementation-guide.md): how each part is built

## Building

The web page's front end (React, with Sharkord's own shadcn/ui components and theme) is in `internal/web/ui`; build it first, since the binary embeds `internal/web/dist`:

    cd internal/web/ui && npm ci && npm run build

To look at it during development with sample data: `SSM_WEB_DEMO=1 go test ./internal/web -run TestDemo -timeout 0`, then http://127.0.0.1:8443 (admin / demo).

PAM needs cgo, so build on Linux. Releases build inside AlmaLinux 8, whose glibc (2.28) is older than every supported system's, so one binary runs on all of them (see `packaging/build.sh`):

    docker run --rm -v "$PWD:/src" -v "$(go env GOROOT):/goroot:ro" -v "$(go env GOMODCACHE):/gomod" almalinux:8 sh /src/packaging/build.sh $VERSION
    ARCH=amd64 VERSION=$VERSION nfpm package -f packaging/nfpm.yaml -p deb -t dist/
    ARCH=amd64 VERSION=$VERSION nfpm package -f packaging/nfpm.yaml -p rpm -t dist/

For development, `CGO_ENABLED=1 go build ./cmd/shiver-server-manager` and `go test ./...` with `libpam0g-dev` (or `pam-devel`) work on any of them.

Everything else builds and tests on any OS; Linux-only parts have stand-ins. `scripts/check.sh` runs every local check.

CI runs the unit tests, then installs the package on every supported system in a container (install scripts, sudoers and PAM files, the binary runs), runs the end-to-end tests inside Debian, Fedora, AlmaLinux 9 and 10, and openSUSE Tumbleweed and Leap cloud images under KVM, and installs the .deb on a fresh Ubuntu 24.04 runner (amd64 and arm64) to drive the real binary through every milestone as root: PAM logins, real Sharkord servers, Caddy with Pebble (Let's Encrypt's test server), ufw, LUKS, a second disk, backups and imports, the real Shiver plugin, the health check, two browsers on a voice call, and the web page with a real PAM login. Pushing a `v*` tag publishes the packages ([RELEASING.md](RELEASING.md)).

## The Shiver family

- [Shiver](https://github.com/GenericD00d/Shiver): the desktop and Android client, and the companion plugin
- **shiver-server-manager**, this repo
- [shiver-media-player](https://github.com/GenericD00d/shiver-media-player): a plugin for watching videos together in a voice channel
- [shiver-temp-share](https://github.com/GenericD00d/shiver-temp-share): a plugin for sharing large files with links that expire

## Licence

MIT. See [LICENSE](LICENSE).
