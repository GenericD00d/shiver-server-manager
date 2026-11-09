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
- **State file:** it also stores `fixedPublicIPv4`, for when the admin sets the address by hand.
- **Who may run setup:** while `shiver-admins` has no members, anyone with root through sudo can run setup (they're setting the server up) and becomes the first admin. Once it has members, setup needs an admin login like everything else.
- **Admins:** removing the last admin is refused, since nobody could then log in except root without sudo.
- **Passphrases:** vault (and later export) passphrases need at least 12 characters.
- **Scripted answers:** with `--password-stdin` and `--passphrase-stdin`, answers are read one per line in the order asked: the login password first, then passphrases. On stdin a new passphrase is read once, not twice. Setup's recovery-key confirmation is the next stdin line.
- **Secrets in memory:** age takes passphrases and keys as Go strings, which can't be wiped. Byte slices are cleared where the code owns them; `Key.Forget` drops the vault key. Everything typed in secret is added to the runner's redaction list.
- **Exit code 3** also covers a wrong vault passphrase or recovery key, a paused login, and no terminal to ask on.

## Manual checks still to do

- **Interactive login on a real terminal:** CI checks logins through stdin only.
- **Two-factor login** (guide's "Prototype first"): try `libpam-google-authenticator`; the code forwards every PAM prompt, but it hasn't been tried.
- **sudo-rs on Ubuntu 26.04:** run the sudoers check there; CI only covers 24.04.

## Verified against Sharkord v0.0.30 source

Env variable names; first-start token output (chalk-coloured, printed after the "access token" lines); `randomUUIDv7()` tokens hashed with `js-sha256` (lowercase hex) into `settings.secret_token`; `/healthz` returns `{"status":"ok",...}`; `PLUGIN_SDK_VERSION = 2`; the log lines `Plugin loaded: <id>@v<version> by <author>` and `Failed to load plugin <id>: <error>`; `settings.enable_plugins` and the `plugin_data` table; release assets `sharkord-linux-x64`, `sharkord-linux-arm64`, `release.json`. Shiver v0.2.7 ships `shiver-plugin-0.3.2.zip` and `SHA256SUMS.txt`.

## Bug log

(none yet)
