# Changelog

## Unreleased

### Added

- Install several Shiver plugins at once: tick them on the Plugins page and choose Install selected, list several IDs in the terminal menu, or give `plugin add` more than one. The server is backed up and restarted once for the lot instead of once per plugin, and if Sharkord can't load one, the previous copies all go back.

## 0.3.2 (2026-11-14)

### Added

- Every published Sharkord release is offered, not only the ones on the manager's tested list, so a new Sharkord shows up without a manager update. Create, update and import use the newest by default, and the health check says when one is out. Sharkord 0.0.31 is checked and added to the tested list.
- Pick the Sharkord version when creating, importing or updating a server: a dropdown on the web page, a choice in the terminal menu, or `--version` (new on `import`). The newest is chosen unless you pick another. `instance versions` lists them.
- Known problem: Sharkord 0.0.31 doesn't exit when stopped, so each stop, restart, update or backup on it takes 90 seconds longer and ends with a kill. Pick 0.0.30 to avoid it until Sharkord fixes it.

### Security

- Restores, plugin installs and backups work in a server's data folder only through `os.Root`, which never follows a link out of it, and unpack into staging folders the server's own user can't write.
- The web page gives the same answer for a wrong password and for a right password on an account outside `shiver-admins`.
- Built with Go 1.27.4, which fixes a crash of the web page's HTTP server in Go 1.27.3 ([GO-2026-6859](https://pkg.go.dev/vuln/GO-2026-6859)).

### Changed

- The docs follow the Shiver standard (`docs/standards.md`): AGENTS.md, ARCHITECTURE.md, SECURITY.md and RELEASING.md are new, `NOTES.md` is now `docs/decisions.md`, and the original guide and checklist moved to `docs/design/`. `scripts/check-docs.py` checks the structure in CI, and `scripts/check.sh` runs every local check.
- The unit tests pass as root and on a nearly full disk, so only real failures show.
- `govulncheck` runs weekly as well as on every push, in its own workflow (`.github/workflows/audit.yml`), so a new Go advisory shows up even when nothing is pushed.

## 0.3.1 (2026-11-12)

- The Shiver plugins page now shows one server at a time: pick it from the list at the top to see its plugins, with install and update buttons. A server with an update has an (i) in the list, and the "available" links on the Servers page open the page on that server.
- Make a new secret (owner) token for a server, when the old one is lost or leaked: in the server's menu (…) on the web page, Owner tokens in the terminal menu, or `token new <id>`. It asks for your password again, backs the server up and restarts it. The old token stops working, and everyone on the server has to log in again, because Sharkord also signs logins with the token.

## 0.3.0 (2026-11-12)

From users' feedback:

- A Shiver plugins page lists the Shiver plugins on offer (Shiver itself, Shiver Temp Share and Shiver Media Player), with each one's latest version and what every server has, and installs or updates one on a server with a click. The list is `plugins.json` in the manager's repository, so new plugins show up without updating the manager; if it can't be read, the page says plugins are unavailable. Each install is checked against the checksum published with the release and backed up first, and the previous copy comes back if Sharkord can't load it. In the terminal: the Shiver plugins menu, `plugin catalog` and `plugin add <id> <plugin>`.
- The server menu no longer has "Install or update Shiver"; that's on the Shiver plugins page now.
- The Servers page shows the selected server's details (ports, data folder, limits, health, plugins, changed settings) instead of every server's plugins in one list. Click a server, or use the keyboard, to pick it.
- Each server has a tick when it's healthy or a warning sign when it needs attention, and an (i) when one of its plugins has an update.

## 0.2.0 (2026-11-12)

- Runs on more Linux distributions: Debian 12 and 13, Fedora 43 and newer, RHEL, AlmaLinux and Rocky Linux 9 and 10, and openSUSE Tumbleweed and Leap 16, as well as Ubuntu 24.04 and 26.04 and Linux Mint 22. Each release has an `.rpm` beside the `.deb`. Setup installs packages with apt, dnf or zypper, gets Caddy from the Caddy project's own repository where the distribution's is too old (COPR on Fedora and RHEL), and offers firewalld rather than ufw outside Debian and Ubuntu. Self-update installs the right package the right way.
- On openSUSE, a drop-in makes Caddy start from its Caddyfile (openSUSE's unit starts with `--resume`). Where SELinux confines Caddy (openSUSE Tumbleweed), its admin socket is in `/run/caddy` and setup lets Caddy connect to the servers (`httpd_can_network_connect`).
- Caddy 2.10 is now enough (openSUSE Leap 16's).
- Nothing changes on an existing Ubuntu or Mint server.
- `instance logs` with an unknown server ID says so instead of crashing.

## 0.1.6 (2026-11-10)

- Install any Sharkord plugin from a .zip or .tar (.tar.gz, .tar.zst) file: a server's menu > Install a plugin from a file on the web page (the file is uploaded), or `plugin install <id> <file>`. Like Shiver, the server is backed up first, the plugin is switched on, and the previous copy comes back if Sharkord can't load it. Files that can run (a bundled helper program) stay runnable. `plugin list` and Servers > Plugins show what each server has.

## 0.1.5 (2026-11-10)

- Each server can have its own backup schedule (times of day and days of the week), its own retention (how many to keep, and/or for how many days), or no scheduled backups at all: Backups > Schedules on the web page, or `backup schedule <id>`. A tighter limit deletes the extra backups at once, after showing which. The newest backup is never dropped for its age. The default stays 00:00 and 12:00, keeping 14.
- Delete single backups: the bin button on Backups, or `backup delete <id> <file>`.
- The backup timer now checks every 5 minutes and backs up the servers whose schedule owes one, catching up once after the machine was off.

## 0.1.4 (2026-11-10)

- One-click updates of the manager itself: System > Updates on the web page, or `self-update` in the terminal (`self-update --check` only looks). It downloads the latest release's package for this machine from GitHub, checks it against SHA256SUMS, and installs it with apt in a unit of its own, so the web page restarting doesn't cut the install short. Servers keep running. The health check announces new versions. While the repository is private, save a read-only GitHub token first: `self-update token` (or the GitHub token button).
- Security: the hidden `internal-vacuum` and `internal-enable-plugins` commands now refuse to run as root. The sudoers rule lets members of `shiver-admins` run the manager as root with any arguments, and these two skipped the login, so someone using an admin's account without its password could have had root create a file of their choosing at any new path. The manager only ever runs them as a server's own user, so nothing else changes.

## 0.1.3 (2026-11-10)

- Import, backup, restore and export no longer take away a file's permission to run. Plugins that keep a program in their data folder (a music bot's yt-dlp, say) failed with "EACCES: permission denied, posix_spawn" after an import or restore. For a server imported with an earlier version: `sudo find /var/lib/sharkord/<id>/plugin-data -path '*/bin/*' -type f -exec chmod 700 {} +`.
- Help: forward each server's voice port on its own, not the whole 40000-40999 range; some routers accept a large range and silently drop it.

## 0.1.2 (2026-11-10)

- `firewall upnp` found no router when the server's firewall was on: ufw dropped the router's answers to the multicast search. When nobody answers, it now asks the default gateway directly, whose answer the firewall lets in.
- `firewall upnp on` explains when the router refuses because it already forwards the ports with rules set by hand (UPnP errors 718 and 729), and leaves UPnP off since it isn't needed.
- Upgrading the package restarts the web page if it's running, so it shows and runs the new version (before, it kept the old one until restarted by hand).
- The DNS warning during create and import now says how to stop it: `cert dns-check <id> off` when the domain points at a proxy in front of the server, or `cert switch <id> --tls selfsigned`.
- `cert dns-check <id> on|off` (and Certificates > DNS check on the web page) turns the check on or off for an existing server.

## 0.1.1 (2026-11-10)

- Setup failed with "can't read Caddy's version" when Ubuntu's own Caddy (2.6.2) was installed. It now reads that version, and offers to upgrade Caddy in place from Caddy's own repository, keeping an existing Caddyfile.
- A server left in an unfinished import (or any other unfinished operation) can now be deleted, and so "Remove everything" works too. Deleting after an interrupted move also removes the copy in the other location.
- Repair explains that an unfinished import needs the same import run again, or the server deleted.
- Creating or importing a server is refused until first-time setup has finished.
- Create and import can skip the DNS check (`--skip-dns-check`, or "the domain points at a proxy" in the forms), for domains whose A record holds a proxy's address; the server remembers it for `cert retry` and `instance domain`. Self-signed servers never checked DNS.
- The web page and menu's import forms can choose the certificate type (self-signed included), and both create and import can choose the storage location.
- `storage add <path> --default` adds a folder and makes it where new and imported servers go, in one step; setup has `--data-location` for the same.

## 0.1.0 (2026-11-10)

First release. Everything in the checklist, built in the guide's order:

- One program, a terminal menu and commands, for Ubuntu 24.04 and 26.04 and Linux Mint 22, on amd64 and arm64.
- First-time setup: OS and port checks, Caddy from its own repository, the firewall (ufw or firewalld), clock, public address, carrier-grade NAT, outbound access, disk space, automatic updates, sleep on desktops, the admins group and the vault.
- Logins through PAM for members of `shiver-admins`, a pause after failed logins, and an idle lock.
- An age vault for secret tokens and container keys, with a recovery key.
- Sharkord servers under systemd, each with its own user, ports, hardened unit and domain; Let's Encrypt or self-signed certificates through Caddy.
- Encrypted data folders (LUKS) by default, storage locations on other drives, moving servers between them.
- Backups twice a day, restore, export, and import from exports, backups, plain installs and Docker volumes.
- The Shiver plugin in one step.
- A health check every five minutes with email or webhook alerts, public IP changes, UPnP.
- Resumable operations and `repair`.
- A web page with everything the menu does, for servers without a desktop: on the loopback address only, reached through an SSH tunnel or an admin domain through Caddy, with the same PAM login.
