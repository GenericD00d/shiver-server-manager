# Build notes

Decisions made with the owner, places this build differs from `Implementation guide.md`, and bugs found along the way. The guide and checklist stay as written; this file records what changed.

## Decisions (2026-11-09)

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
- **Backups:** twice a day, at 00:00 and 12:00 (timer `OnCalendar=*-*-* 00,12:00:00`, `Persistent=true`, no random delay), keeping 14, which is one week.
- **Other defaults:** 10 GiB containers, health check every 5 minutes (unchanged from the guide).
- **Releases:** GitHub repository `GenericD00d/shiver-server-manager`.
- **Sharkord versions:** only v0.0.30 on the tested list to start (plugin SDK 2).
- **Development:** Go on Windows. Linux-only code sits behind build tags, with stand-ins so it builds and tests on Windows; CI runs the real thing on Ubuntu 24.04, amd64 and arm64.

## Differences from the guide

- **Port check:** the guide's test bind is used, not the checklist's `ss -lntu`.
- **UPnP:** the `goupnp` library is used, not `upnpc`, so the package doesn't need to suggest `miniupnpc`.
- **Operation record:** the instance's `operation` field is an object (`name`, `lastStep`, `failedStep`, `error`, `startedAt`) rather than just a name, so repair knows where to resume.
- **State file:** it also stores `fixedPublicIPv4`, for when the admin sets the address by hand, `upnp`, the test CA, and each operation's `params` (a move's source and target).
- **systemctl instead of D-Bus:** units are controlled through `systemctl` via the runner (testable with the fake), not go-systemd's D-Bus client.
- **Secret settings:** `SHARKORD_OIDC_CLIENT_SECRET` stays out of the records; it lives only in the env file and is carried over when the file is rewritten.
- **Alert settings:** email and webhook settings (including an SMTP password) are in `/etc/shiver-server-manager/alerts.json` (root, 600), not the vault, because the health timer runs without unlocking it.
- **Self-signed root name:** "Shiver Server Manager Root" rather than "Sharkord Manager Root".
- **Prototype answered:** a digits-only `SHARKORD_OIDC_CLIENT_ID` written as `'"12345"'` passes through systemd and Sharkord reads it as text (CI checks this). The unit's hardening, as written, runs Sharkord and voice fine.
- **Who may run setup:** while `shiver-admins` has no members, anyone with root through sudo can run setup (they're setting the server up) and becomes the first admin. Once it has members, setup needs an admin login like everything else.
- **Admins:** removing the last admin is refused, since nobody could then log in except root without sudo.
- **Passphrases:** vault (and later export) passphrases need at least 12 characters.
- **Scripted answers:** with `--password-stdin` and `--passphrase-stdin`, answers are read one per line in the order asked: the login password first, then passphrases. On stdin a new passphrase is read once, not twice. Setup's recovery-key confirmation is the next stdin line.
- **Secrets in memory:** age takes passphrases and keys as Go strings, which can't be wiped. Byte slices are cleared where the code owns them; `Key.Forget` drops the vault key. Everything typed in secret is added to the runner's redaction list.
- **Exit code 3** also covers a wrong vault passphrase or recovery key, a paused login, and no terminal to ask on.

## Manual checks still to do

- **A real reboot with encrypted servers:** CI simulates it (service stopped, container closed); try a real reboot on a VM.
- **A second VM for the voice test:** CI runs the browsers on the same machine, so traffic doesn't cross a real firewall.
- **Import from a running Docker container:** CI covers a plain data folder and exports; try `import docker:<name>` with the official image.
- **Sharkord update and rollback:** only one Sharkord version is tested, so `instance update` and its rollback haven't run against two real versions.
- **UPnP on a real home router, and firewalld:** covered by unit tests only.
- **Shiver's statuses and role colours in a browser:** CI checks the plugin loads and its client bundle is served.

- **Interactive login on a real terminal:** CI checks logins through stdin only.
- **Two-factor login** (guide's "Prototype first"): try `libpam-google-authenticator`; the code forwards every PAM prompt, but it hasn't been tried.
- **sudo-rs on Ubuntu 26.04:** run the sudoers check there; CI only covers 24.04.

## Verified against Sharkord v0.0.30 source

Env variable names; first-start token output (chalk-coloured, printed after the "access token" lines); `randomUUIDv7()` tokens hashed with `js-sha256` (lowercase hex) into `settings.secret_token`; `/healthz` returns `{"status":"ok",...}`; `PLUGIN_SDK_VERSION = 2`; the log lines `Plugin loaded: <id>@v<version> by <author>` and `Failed to load plugin <id>: <error>`; `settings.enable_plugins` and the `plugin_data` table; release assets `sharkord-linux-x64`, `sharkord-linux-arm64`, `release.json`. Shiver v0.2.7 ships `shiver-plugin-0.3.2.zip` and `SHA256SUMS.txt`.

## Bug log

Problems found in the guide's design while building, and what was done.

- **ExecStart can't reach the version link (fixed).** The unit runs `ExecStart=/var/lib/<manager>/bin/%i` as the instance's user, but the guide makes that folder root-only (700), so exec fails with permission denied. The state folder is now 711 (others can pass through, not list) and `bin/` 755; every file inside stays 600 or 700.
- **ext4 labels are 16 bytes (fixed).** `mkfs.ext4 -L sharkord-<id>` is too long for IDs over 7 characters; the label is cut to 16.
- **The 077 umask hid Caddy's folders (fixed).** Folders the manager creates under `/etc/caddy` came out 0700, so Caddy's own user couldn't read its config. Folders are now created with explicit modes.
- **Opening a stopped WAL database as root (design change).** Opening `db.sqlite` read-only as root when Sharkord is stopped makes SQLite create root-owned `-shm`/`-wal` files Sharkord then can't open. Database reads for token checks use a private copy; `VACUUM INTO` and the plugin switches run as the instance's own user, through hidden `internal-*` commands of the same binary.
- **`findmnt --verify` and existing fstab problems (fixed).** A server's fstab can already have errors (GitHub's runners do), so the check compares error counts before and after adding the line.
- **Restore and encrypted data (design change).** Restoring "beside the current folder" would put an encrypted server's data on the bare disk. Restores and imports unpack into a staging folder inside the data folder, then swap with renames.
- **Voice channel rows are aria-disabled (test only).** Sharkord marks channel rows aria-disabled when a member can't drag them; the voice test clicks with force.
