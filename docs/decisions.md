# Decisions

Decisions made with the owner, places this build differs from the
[implementation guide](design/implementation-guide.md), and bugs found along the way, oldest first.
The guide and [checklist](design/checklist.md) stay as written; this file records what changed.

## 2026-11-09: Names, defaults and where releases come from

- **Name:** "Shiver Server Manager". The binary, package, log and manager folders use `shiver-server-manager` everywhere the guide says `sharkord-manager`. All names live in `internal/paths/paths.go`.

  | Guide | This build |
  | --- | --- |
  | `sharkord-manager` (binary, package, PAM service, sudoers file) | `shiver-server-manager` |
  | `/etc/sharkord-manager/`, `/var/lib/sharkord-manager/` | `/etc/shiver-server-manager/`, `/var/lib/shiver-server-manager/` |
  | `/run/sharkord-manager.lock`, `/var/log/sharkord-manager.log` | `/run/shiver-server-manager.lock`, `/var/log/shiver-server-manager.log` |
  | `/etc/caddy/sharkord-manager/global.caddy` | `/etc/caddy/shiver-server-manager/global.caddy` |
  | `sharkord-admins` group | `shiver-admins` |
  | "Managed by sharkord-manager" | "Managed by shiver-server-manager" |

  Names that describe Sharkord itself keep Sharkord's name: `sharkord@.service`, `sharkord-<id>` users, `/opt/sharkord`, `/var/lib/sharkord`, `/var/backups/sharkord`, `sharkord:<id>` firewall labels.
- **Backups:** by default twice a day, at 00:00 and 12:00, keeping 14 (one week). Since 0.1.5 each server has its own schedule (times and days, local time), retention (count and/or age) or none, in `state.Instance.Backups`. The timer runs `backup --scheduled` every 5 minutes; it backs up a server when its latest scheduled time has passed since its newest backup, so a missed slot (machine off) is caught up once, not once per slot. It looks without the lock first so it only waits when a backup is owed. Age retention never drops the newest backup.
- **Other defaults:** 10 GiB containers, health check every 5 minutes (unchanged from the guide).
- **Releases:** GitHub repository `GenericD00d/shiver-server-manager`.
- **Sharkord versions:** only v0.0.30 on the tested list to start (plugin SDK 2).
- **Development:** Go on Windows. Linux-only code sits behind build tags, with stand-ins so it builds and tests on Windows; CI runs the real thing on Ubuntu 24.04, amd64 and arm64.

## 2026-11-09: Where this build differs from the guide

- **Port check:** the guide's test bind is used, not the checklist's `ss -lntu`.
- **UPnP:** the `goupnp` library is used, not `upnpc`, so the package doesn't need to suggest `miniupnpc`.
- **Operation record:** the instance's `operation` field is an object (`name`, `lastStep`, `failedStep`, `error`, `startedAt`) rather than just a name, so repair knows where to resume.
- **State file:** it also stores `fixedPublicIPv4`, for when the admin sets the address by hand, `upnp`, the test CA, and each operation's `params` (a move's source and target).
- **systemctl instead of D-Bus:** units are controlled through `systemctl` via the runner (testable with the fake), not go-systemd's D-Bus client.
- **Secret settings:** `SHARKORD_OIDC_CLIENT_SECRET` stays out of the records; it lives only in the env file and is carried over when the file is rewritten.
- **Alert settings:** email and webhook settings (including an SMTP password) are in `/etc/shiver-server-manager/alerts.json` (root, 600), not the vault, because the health timer runs without unlocking it.
- **Self-signed root name:** "Shiver Server Manager Root" rather than "Sharkord Manager Root".
- **Packaging with nfpm directly:** the guide names GoReleaser (with nfpm). Since cgo means building each architecture on its own runner anyway, CI and the release workflow call nfpm directly with `packaging/nfpm.yaml`. `miniupnpc` isn't suggested (the manager uses goupnp); `util-linux`, `iproute2` and `e2fsprogs` are listed as dependencies.
- **Commands added for the menu:** `instance domain`, `instance limits`, `instance grow`, `remove-everything`, `backup list`, `storage backup-location`, `firewall upnp`, `health alerts`.
- **Prototype answered:** a digits-only `SHARKORD_OIDC_CLIENT_ID` written as `'"12345"'` passes through systemd and Sharkord reads it as text (CI checks this). The unit's hardening, as written, runs Sharkord and voice fine.
- **Who may run setup:** while `shiver-admins` has no members, anyone with root through sudo can run setup (they're setting the server up) and becomes the first admin. Once it has members, setup needs an admin login like everything else.
- **Admins:** removing the last admin is refused, since nobody could then log in except root without sudo.
- **Passphrases:** vault (and later export) passphrases need at least 12 characters.
- **Scripted answers:** with `--password-stdin` and `--passphrase-stdin`, answers are read one per line in the order asked: the login password first, then passphrases. On stdin a new passphrase is read once, not twice. Setup's recovery-key confirmation is the next stdin line.
- **Secrets in memory:** age takes passphrases and keys as Go strings, which can't be wiped. Byte slices are cleared where the code owns them; `Key.Forget` drops the vault key. Everything typed in secret is added to the runner's redaction list.
- **Exit code 3** also covers a wrong vault passphrase or recovery key, a paused login, and no terminal to ask on.

## 2026-11-09: A web page, at the owner's request

- **Reach:** a root service (`shiver-server-manager-web`, off until `web enable`) listening on `127.0.0.1:8443` only. Admins use an SSH tunnel, or opt in to an admin domain that Caddy proxies (`/etc/caddy/sites/_admin.caddy`).
- **One code path:** each web action runs the same CLI command as a child process (root, no sudo user, `--yes --passphrase-stdin --acting-user <admin>`), so the web page reuses everything the e2e tests cover. The browser never sends a command line: `internal/web/actions.go` maps named actions to fixed commands, validating every input (IDs, domains, absolute paths, no leading `-`). A test checks every action builds a command the CLI accepts.
- **Security:** PAM login (a second hidden prompt becomes a code field), the same throttle and group check; in-memory sessions (HttpOnly, SameSite=Strict, Secure behind Caddy), 10-minute idle and 24-hour limits; password again for tokens, deletes, restores, vault changes and remove-everything; changes need an app header and same origin; unknown Host headers are refused (DNS rebinding); strict CSP. Secrets passed on stdin are blanked out of job output; the secret token is returned once and kept nowhere.
- **Front end:** React 19, Vite, Tailwind 4, with components copied from Sharkord's `packages/ui` (MIT) and its theme tokens, following the guide's "a web interface should use Sharkord's own stack". The built files in `internal/web/dist` are committed (the binary embeds them) and rebuilt in CI and releases.

## 2026-11-09: Verified against Sharkord v0.0.30 source

Env variable names; first-start token output (chalk-coloured, printed after the "access token" lines); `randomUUIDv7()` tokens hashed with `js-sha256` (lowercase hex) into `settings.secret_token`; `/healthz` returns `{"status":"ok",...}`; `PLUGIN_SDK_VERSION = 2`; the log lines `Plugin loaded: <id>@v<version> by <author>` and `Failed to load plugin <id>: <error>`; `settings.enable_plugins` and the `plugin_data` table; release assets `sharkord-linux-x64`, `sharkord-linux-arm64`, `release.json`. Shiver v0.2.7 ships `shiver-plugin-0.3.2.zip` and `SHA256SUMS.txt`.

## 2026-11-10: Verified on the owner's server

- **Self-update from the web page, 0.1.4 to 0.1.5**, with a GitHub token for the private repository: installed, the web page restarted on the new version.
- **Voice through a home router (Telia F1):** works with each voice port forwarded on its own; the router accepted a 40000-40999 range rule and silently ignored it.

## 2026-11-12: Other distributions, at the owner's request

The guide targets Ubuntu. The manager now also runs on Debian 12 and 13, Fedora 43+, RHEL/AlmaLinux/Rocky 9 and 10, and openSUSE Tumbleweed and Leap 16 (`internal/distro`). Arch is left out: it no longer packages Caddy.

- **One build:** binaries are built inside AlmaLinux 8 (glibc 2.28, older than any supported system's; `packaging/build.sh`), with `-D_GNU_SOURCE` for the PAM library's `RTLD_NEXT`. One `.deb` and one `.rpm` per architecture.
- **Caddy:** Debian's, Ubuntu's, Fedora's and EPEL's own Caddy is too old; it comes from the Caddy project's apt repository or COPR (a `.repo` file written directly, no dnf plugin). openSUSE's own is used. The minimum is now 2.10.0 (Leap 16's, which CI runs); `renewal_window_ratio` is left out below 2.11, which doesn't know it.
- **openSUSE's Caddy unit** starts with `--resume` and reloads without `--force`; a drop-in (`caddy.service.d/shiver-server-manager.conf`) makes it behave like Caddy's own.
- **SELinux:** on Fedora and RHEL, Caddy from COPR and Sharkord run unconfined; nothing extra is needed (CI runs them enforcing). openSUSE Tumbleweed labels `/usr/bin/caddy` `httpd_exec_t`, so Caddy runs as `httpd_t`: it may not create its admin socket in `/var/lib/caddy` (`httpd_var_lib_t`) nor connect out. Where Caddy is labelled that way, the socket moves to `/run/caddy/admin.sock` (a `RuntimeDirectory` in the drop-in; httpd_t's sockets in /run get their own type) and setup switches on `httpd_can_network_connect`.
- **PAM file:** the `.deb` ships it (`common-auth`); the `.rpm`'s postinst writes it for the system's stack (`password-auth` on Fedora/RHEL, `common-auth`/`common-account` on openSUSE) and removes it on erase.
- **Firewall:** setup offers ufw on Debian and Ubuntu, firewalld elsewhere (SSH ports opened with `firewall-offline-cmd` before it starts).
- **Self-update** picks the `.deb` or `.rpm` and runs apt, dnf or zypper (`--allow-unsigned-rpm`: the packages aren't signed; SHA256SUMS over HTTPS vouches for them).
- **CI:** package installs on 12 distribution containers; the full end-to-end tests inside Debian 13, Fedora 44, AlmaLinux 9 and 10, openSUSE Tumbleweed and Leap 16 cloud images under KVM (`test/vm`), amd64 only. The browser tests (voice, web page) still run on Ubuntu only.

## 2026-11-13: Follow the Shiver standard

**Why:** the four Shiver repos share one maintainer, and agents do most of the writing, so they keep the same files, checks and security baseline ([standards.md](standards.md)).

**Consequences:** AGENTS.md, ARCHITECTURE.md, SECURITY.md and RELEASING.md are new. This file was `NOTES.md`, and the guide and checklist moved under `docs/design/`. `scripts/check-docs.py` checks the structure in CI.

## 2026-11-13: Pin CI's actions and build image; leave the test images current

**Why:** a moved tag must not change what CI runs or what a release is built in. The distribution images in the `install-scripts` job are different: they are there to catch each distribution's current image breaking the install scripts, and that job holds no secrets.

**Consequences:** every action is pinned to a commit SHA, `almalinux:8` (the release build image) by digest, and the browser tests install from a lockfile. Dependabot proposes action updates monthly. The test images stay unpinned, with a comment in `ci.yml` saying why.

## 2026-11-13: Root works in a server's data folder only through os.Root

**Why:** the data folder belongs to the server's own user, so a path root builds into it can't be trusted to stay inside it. Stopping the server first narrows the window but doesn't close it; `os.Root`, which never follows a link out of its folder, does.

**Consequences:** `backup.Read`, `backup.Write` and the plugin unpacker all go through a root on the folder they work in. Unpack staging folders stay root-owned (`stagingDir`); only the database copy's folder is the server user's (`vacuumDir`). `swapPlugin` walks and changes modes through a root, and refuses any link. Each has a test that plants a link and checks nothing outside is touched.

## Open items

- **Signed releases.** Packages, plugins and `plugins.json` are checked only against checksums from the same release. Signing them needs a minisign key that only the owner holds; see [RELEASING.md](../RELEASING.md#keys).

Manual checks still to do:

- **A real reboot with encrypted servers:** CI simulates it (service stopped, container closed); try a real reboot on a VM.
- **A second VM for the voice test:** CI runs the browsers on the same machine, so traffic doesn't cross a real firewall.
- **Import from a running Docker container:** CI covers a plain data folder and exports; try `import docker:<name>` with the official image.
- **Sharkord update and rollback:** only one Sharkord version is tested, so `instance update` and its rollback haven't run against two real versions.
- **UPnP on a real home router:** covered by unit tests only. (firewalld is now covered by the Fedora, AlmaLinux and openSUSE VMs in CI.)
- **Shiver's statuses and role colours in a browser:** CI checks the plugin loads and its client bundle is served.
- **Interactive login on a real terminal:** CI checks logins through stdin only.
- **Two-factor login** (guide's "Prototype first"): try `libpam-google-authenticator`; the code forwards every PAM prompt, but it hasn't been tried.
- **sudo-rs on Ubuntu 26.04:** run the sudoers check there; CI only covers 24.04.

## Bug log

Problems found in the guide's design while building, and what was done.

### ExecStart can't reach the version link (fixed)

The unit runs `ExecStart=/var/lib/<manager>/bin/%i` as the instance's user, but the guide makes that folder root-only (700), so exec fails with permission denied. The state folder is now 711 (others can pass through, not list) and `bin/` 755; every file inside stays 600 or 700.

### ext4 labels are 16 bytes (fixed)

`mkfs.ext4 -L sharkord-<id>` is too long for IDs over 7 characters; the label is cut to 16.

### The 077 umask hid Caddy's folders (fixed)

Folders the manager creates under `/etc/caddy` came out 0700, so Caddy's own user couldn't read its config. Folders are now created with explicit modes.

### Opening a stopped WAL database as root (fixed)

A design change. Opening `db.sqlite` read-only as root when Sharkord is stopped makes SQLite create root-owned `-shm`/`-wal` files Sharkord then can't open. Database reads for token checks use a private copy; `VACUUM INTO` and the plugin switches run as the instance's own user, through hidden `internal-*` commands of the same binary.

### `findmnt --verify` and existing fstab problems (fixed)

A server's fstab can already have errors (GitHub's runners do), so the check compares error counts before and after adding the line.

### Restore and encrypted data (fixed)

A design change. Restoring "beside the current folder" would put an encrypted server's data on the bare disk. Restores and imports unpack into a staging folder inside the data folder, then swap with renames.

### Removing the package removes the unit template (documented)

`sharkord@.service` lives in `/usr/lib`, so `apt remove` takes it away: running servers carry on, but don't start after a reboot until the package is reinstalled. The admin guide says so; changing it would mean the manager writing the unit into `/etc/systemd/system` itself.

### udev may not record a freshly formatted drive (fixed)

`lsblk` can show no file system for a drive formatted moments before (always, for loop devices on some kernels). The manager waits for udev, and reads a named drive directly with `blkid -p` if it's still missing.

### New members get a welcome dialog (fixed)

In the tests only: the voice test presses Skip before joining.

### Voice channel rows are aria-disabled (fixed)

In the tests only: Sharkord marks channel rows aria-disabled when a member can't drag them; the voice test clicks with force.

### Hidden commands reachable through sudo (fixed)

`internal-vacuum` and `internal-enable-plugins` skipped the login and took any paths, and the sudoers rule lets admins run the binary as root with any arguments, so they were a way past the login. They now refuse root (`refuseRoot` in `internal/cli/cli.go`); the manager only starts them as a server's own user. Any future hidden command needs the same check.

### Root could be led outside a server's data folder (fixed)

Restores and plugin installs unpacked as root into a staging folder the server's own user could change, and backups read that folder by path. All three now go through `os.Root`, and the staging folders stay root's. Found by the 2026-11-13 review.

### The web login confirmed passwords for accounts outside the admins group (fixed)

PAM checks the password before the group, and the page named the group only when the password was right. It now gives one message for both, and logs the real reason. Found by the 2026-11-13 review.

### Releases were built with a Go whose HTTP server could crash (fixed)

Go 1.27.3's `net/http` has a race in `MaxBytesReader` that can crash an HTTP/1 server (GO-2026-6859), and the web page is one. The `govulncheck` step added on 2026-11-13 found it on its first run. `go.mod` now asks for Go 1.27.4, which every CI and release build reads.

### Two unit tests depended on where they ran (fixed)

`TestNonLinuxOrNonRootRefused` expected a refusal that only happens when the tests aren't root, and `TestHealth` read the real free space, so both failed in a root container or on a nearly full disk. The first now skips as root; the health check reads space through `Sys.DiskSpace`, which the test harness sets to a roomy disk, and `TestHealth` checks the low-space warning with numbers of its own.
