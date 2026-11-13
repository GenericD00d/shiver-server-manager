# Sharkord Manager: implementation guide

Nov 9, 2026 · @Smith

This tab tells the agent building the manager how to do each item in Checklist: the architecture, libraries, file formats, exact commands, and the order to build in. Where the two tabs disagree, this one is newer.

## Brief for the agent

**Goal:** build `sharkord-manager`, one program for Ubuntu 24.04 and 26.04 and Linux Mint 22 that sets up, runs, backs up, imports, exports and deletes several Sharkord servers on one machine, each on its own domain, from a terminal menu, with Caddy handling HTTPS.

**Scope:** every item in Checklist. This tab says how to do each one.

**Fixed decisions** (ask the user before changing any):

- Go, built as one binary with cgo on (PAM needs it)
- Sharkord runs as its native Linux binary under systemd, never Docker
- Caddy comes from Caddy's apt repository and is configured only through files under `/etc/caddy`
- Let's Encrypt by default; self-signed through Caddy's built-in certificate authority as an option
- Logins are OS accounts in the `sharkord-admins` group, checked through PAM
- Secrets live in an age-encrypted vault with its own passphrase and a recovery key
- Instance data lives in LUKS containers by default
- MIT licence

**Done means:**

- Every checklist item is built and covered by a test, or by a written manual check where a test isn't possible
- The tests under "Testing" pass on Ubuntu 24.04, Ubuntu 26.04 and Mint 22, on amd64 and arm64
- A fresh server gets from `apt install` to two working instances, one per certificate type, with voice working, using only the menu and the "Steps outside the manager"

**Ask the user before starting:**

- The program's final name (this guide uses `sharkord-manager`)
- Defaults to confirm: container size 10 GiB, 14 daily backups kept, a health check every 5 minutes
- Where releases will be published (a GitHub repository for the .deb files, later an apt repository)
- Which Sharkord versions to support first (this guide was checked against v0.0.30)

**How to work:**

- Follow "Build order" and build every checklist item, logging bugs as you go and fixing them once everything is in, unless one blocks the next milestone
- Never touch a real server's firewall or Caddy while developing; use the VMs under "Testing"
- When Sharkord's behaviour matters, read its source at the pinned version instead of guessing
- If something in this guide proves wrong, note it for the user rather than working around it silently

## Build order

Each milestone ends with something you can run and test. Note anything that doesn't work yet and carry on; fix the list once everything is built, unless a bug blocks the next milestone.

1. **Skeleton:** Go module, the command tree (no behaviour yet), the command runner, the state file with locking, and the action log. Done when `sharkord-manager version` runs and the state tests pass.
2. **Login and vault:** PAM login, the `sharkord-admins` check, vault creation, unlock and recovery key. Done when a wrong password, a user outside the group and a wrong passphrase are each refused.
3. **One plain instance:** download and checksum, system user, ports, env file, the unit template, first start with token capture into the vault. Done when `/healthz` answers on 127.0.0.1 and the saved token matches the database's hash.
4. **HTTPS and firewall:** Caddy site files, validation and reload, Let's Encrypt (Pebble in tests), ufw rules. Done when the instance loads over HTTPS in a browser and two browsers can talk in a voice channel.
5. **Delete and repair:** the full delete, steps that resume, and `repair`. Done when killing the manager mid-create or mid-delete and running `repair` leaves nothing behind.
6. **Self-signed mode:** the `pki` block, internal certificates, the root download and switching types. Done when both kinds of instance run side by side.
7. **Encrypted data and storage:** LUKS containers, unlock after reboot, grow and erase, plus storage locations on other drives and moving instances between them. Done when a rebooted VM keeps encrypted instances stopped until unlocked, and an instance moves to a second disk intact.
8. **Backups, export and import:** scheduled encrypted backups, restore, export, and the three import sources. Done when an instance moves between two VMs with its messages and uploads intact.
9. **Shiver plugin:** install, update and version notices. Done when one click installs Shiver on a fresh instance and its statuses and role colours show up in a browser.
10. **Health and network:** the health-check timer, public IP changes, UPnP, firewalld. Done when changing the VM's public address is picked up and the instances restart with it.
11. **Menu:** the terminal screens on top of the commands. Done when every checklist action can be done from the menu.
12. **Packaging:** .deb packages for amd64 and arm64, install scripts, sudoers file. Done when the full VM test matrix passes from a clean install.
13. **Docs:** the admin guide, the member guide and troubleshooting, with the "Steps outside the manager" text shown in the menu.

## Architecture

&#91;embedded content: how the manager fits together · 4 parts inside, 5 system pieces\]

Admins and the timers reach the same resumable steps, which drive every system piece. Members reach each instance through Caddy for the web app, and straight to its own port for voice.

## Packages and libraries

One Go module. The menu and the commands call the same `internal/ops` functions, so nothing is implemented twice.

| Package | Job | Libraries and tools |
| --- | --- | --- |
| `cmd/sharkord-manager` | Entry point: starts the menu with no arguments, otherwise runs a command |  |
| `internal/cli` | Command tree, flags, `--json` output, exit codes | `github.com/spf13/cobra` |
| `internal/tui` | Menu screens and forms | `charm.land/bubbletea/v2`, `charm.land/huh/v2`, `charm.land/lipgloss/v2` |
| `internal/auth` | PAM login, group check, idle lock, pause after failed logins | `github.com/msteinert/pam/v2`, `os/user` |
| `internal/vault` | Create, unlock, store and read secrets, recovery key, change passphrase | `filippo.io/age` |
| `internal/state` | Records file, lock file, atomic writes, schema version | `golang.org/x/sys/unix` |
| `internal/ops` | Step engine: create, delete, update, switch type, move, unlock, repair |  |
| `internal/sharkord` | Releases, download and checksum, env files, first start, token check, version detection | `net/http`, `modernc.org/sqlite`, `gopkg.in/ini.v1` |
| `internal/systemd` | Unit file, drop-ins, start, stop, status, logs | `github.com/coreos/go-systemd/v22/dbus`, `journalctl` |
| `internal/caddy` | Main Caddyfile, site files, validate, reload, certificate status | `text/template`, `crypto/x509`, `caddy`, `systemctl` |
| `internal/firewall` | ufw and firewalld behind one interface | `ufw`, `firewall-cmd` |
| `internal/netinfo` | Public IP, DNS checks, carrier-grade NAT, UPnP | `net`, `github.com/huin/goupnp` |
| `internal/storage` | Storage locations: drive checks, fstab entries, free space, moving instances | `findmnt`, `lsblk`, `rsync`, `golang.org/x/sys/unix` |
| `internal/luks` | Containers: create, open, mount, grow, close, erase | `cryptsetup`, `mkfs.ext4`, `resize2fs` |
| `internal/backup` | Backups, restore, export, import | `modernc.org/sqlite`, `archive/tar`, `github.com/klauspost/compress/zstd`, `filippo.io/age` |
| `internal/plugins` | Shiver plugin: release lookup, checksum, install, switching plugins on, version checks | `net/http`, `archive/zip`, `modernc.org/sqlite` |
| `internal/health` | Health checks, notices, email and webhook alerts | `net/http`, `net/smtp` |
| `internal/osuser` | System users and the admins group | `useradd`, `userdel`, `groupadd`, `gpasswd` |
| `internal/run` | The single way to run external commands, plus a fake for tests | `os/exec` |

Latest versions on 2026-11-09: bubbletea 2.1.1, huh 2.0.3, lipgloss 2.0.6, cobra 1.10.2, pam 2.1.0, age 1.3.2, go-systemd 22.7.0, goupnp 1.3.1, modernc sqlite 1.62.0. Charm's v2 libraries use the `charm.land/...` import paths, not `github.com/charmbracelet/...`. The pure-Go SQLite driver keeps PAM as the only cgo dependency.

## Rules every part follows

### Running commands

- Run every external program through `internal/run`: an argument list (never a shell string), a timeout, and a log line with the command and its exit code
- At start, set `PATH=/usr/sbin:/usr/bin:/sbin:/bin`, `LC_ALL=C` and `LANG=C.UTF-8`, drop the caller's other variables, and resolve each tool once
- Never put a secret in a command's arguments, where other users can read it from `/proc`. Pass it on stdin (`cryptsetup --key-file=-`) or in a mode-600 file
- Redact known secrets from logs and error messages inside the runner
- Prefer machine-readable output: `systemctl show -p`, `journalctl -o json`, `ss -H`, `firewall-cmd --list-ports`

### Writing files

- Write to a temporary file in the same folder, set owner and mode, fsync it, rename it over the target, then fsync the folder
- Set the umask to 077 at start, so secret files are never readable by others, even briefly
- Begin every generated file with a "Managed by sharkord-manager" comment where the format allows, and list it in the instance's record
- In folders an instance or an import source controls, never follow symlinks: open with `O_NOFOLLOW`, walk with `Lstat`, and refuse symlinks and device files when copying

### Secrets

- Keep secrets in memory only while needed, and overwrite byte slices after use
- Keep them out of logs, `--json` output, error messages and the state file
- Show an owner token only after the password is entered again, hide it on the next keypress, and never put it on the clipboard

### Locking

- Commands that change anything take an exclusive `flock` on `/run/sharkord-manager.lock`; read-only commands take a shared one
- Wait up to 10 seconds, then fail with a message naming who holds the lock (write the holder's PID and user into the file)
- The state file says what should exist. Repair changes the system to match it, never the reverse

### Checking input

- Instance ID: `^[a-z][a-z0-9-]{0,19}$`, unique
- Domain: lowercased and converted with `golang.org/x/net/idna`, at least two labels, each 1–63 characters of `a-z`, `0-9` and inner hyphens, 253 characters at most, unique across instances
- Ports: inside the configured ranges, unique, and free when assigned
- Import and restore paths: absolute, cleaned, existing, with no symlinks along the way

### Running as root safely

- Never start an editor, pager or shell; show long text inside the menu
- Ignore `EDITOR`, `PAGER`, `SHELL` and `LD_*` even if present
- Refuse to start if the binary, `/etc/sharkord-manager` or the state folder can be written by anyone other than root

## Records and file formats

### State file

`/var/lib/sharkord-manager/state.json`, root-only, written atomically under the lock. `status` is one of `creating`, `active`, `locked`, `deleting`, `failed`. `operation` is set while a create, delete, update or switch is running, so repair can resume it.

```json
{
  "schema": 1,
  "settings": {
    "httpPorts": [5000, 5999],
    "webrtcPorts": [40000, 40999],
    "publicIPv4": "203.0.113.10",
    "firewall": "ufw",
    "locations": [
      { "path": "/var/lib/sharkord", "default": true },
      { "path": "/srv/sharkord-disk2/sharkord", "fsUUID": "6f1c2a9e-3b7d-4c55-9e0a-2d4f8b1c7e63" }
    ],
    "backupLocation": "/var/backups/sharkord",
    "backupKeep": 14,
    "containerSizeGiB": 10
  },
  "instances": {
    "chat-a": {
      "status": "active",
      "domain": "chat-a.example.com",
      "tls": "letsencrypt",
      "testMode": false,
      "sharkordVersion": "0.0.30",
      "location": "/srv/sharkord-disk2/sharkord",
      "dataPath": "/srv/sharkord-disk2/sharkord/chat-a",
      "httpPort": 5000,
      "webrtcPort": 40000,
      "encrypted": true,
      "settings": { "SHARKORD_WEBRTC_MAX_BITRATE": "30000000" },
      "createdAt": "2026-11-09T12:00:00Z",
      "operation": null
    }
  }
}
```

### Env file

`/etc/sharkord-manager/instances/<id>.env`, owned by root, mode 600. systemd reads it before switching to the instance's user. The six variables after the comment are always set by the manager; settings from the menu follow them.

```
# Managed by sharkord-manager (instance chat-a). Do not edit.
SHARKORD_DATA_PATH=/srv/sharkord-disk2/sharkord/chat-a
SHARKORD_HOST=127.0.0.1
SHARKORD_PORT=5000
SHARKORD_WEBRTC_PORT=40000
SHARKORD_WEBRTC_ANNOUNCED_ADDRESS=203.0.113.10
SHARKORD_AUTOUPDATE=false
SHARKORD_WEBRTC_MAX_BITRATE=30000000
```

### Instance unit

`/usr/lib/systemd/system/sharkord@.service`, shipped in the package and shared by all instances. `/var/lib/sharkord-manager/bin/<id>` is a link to `/opt/sharkord/<version>/sharkord`. The hardening below should not affect Bun or mediasoup, but run the voice test after adding anything.

```ini
# Managed by sharkord-manager. Do not edit.
[Unit]
Description=Sharkord instance %i
After=network-online.target
Wants=network-online.target

[Service]
User=sharkord-%i
Group=sharkord-%i
EnvironmentFile=/etc/sharkord-manager/instances/%i.env
ExecStart=/var/lib/sharkord-manager/bin/%i
Restart=on-failure
RestartSec=5s
UMask=0077
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
RestrictNamespaces=yes
LockPersonality=yes
CapabilityBoundingSet=
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK

[Install]
WantedBy=multi-user.target
```

Leave out `MemoryDenyWriteExecute=` (it breaks Bun's JIT) and anything that stops programs running from the data folder (Sharkord runs its mediasoup worker from there). `AF_NETLINK` is there because Sharkord lists the network interfaces at startup.

### Drop-in for the data folder

Every instance gets `/etc/systemd/system/sharkord@<id>.service.d/data.conf`, because its data can live on any storage location. Without this file Sharkord can't write anywhere and fails to start, which is the safe outcome. `RequiresMountsFor=` names the storage location rather than the data folder, so an encrypted instance doesn't depend on its own container's mount. For a location under `/home`, also add `ProtectHome=read-only`, so `ReadWritePaths=` can open just that folder.

```ini
# Managed by sharkord-manager (instance chat-a). Do not edit.
[Unit]
RequiresMountsFor=/srv/sharkord-disk2/sharkord

[Service]
Environment=HOME=/srv/sharkord-disk2/sharkord/chat-a
ReadWritePaths=/srv/sharkord-disk2/sharkord/chat-a
```

### Drop-in for resource limits

When the admin sets limits, write `/etc/systemd/system/sharkord@<id>.service.d/limits.conf` and run `systemctl daemon-reload`. `MemoryHigh=` slows the instance down first, and at `MemoryMax=` it's killed and restarted, so set the first at about 90% of the second. The values below are examples.

```ini
[Service]
MemoryHigh=1800M
MemoryMax=2G
CPUQuota=200%
```

### Drop-in for encrypted instances

`/etc/systemd/system/sharkord@<id>.service.d/encrypted.conf`. A failed condition skips the start instead of failing it, so a locked instance doesn't loop.

```ini
# Managed by sharkord-manager (instance chat-a). Do not edit.
[Unit]
ConditionPathIsMountPoint=/srv/sharkord-disk2/sharkord/chat-a
```

### Timers

`sharkord-manager-health.timer` runs `sharkord-manager health --unattended` 2 minutes after boot and every 5 minutes after that. `sharkord-manager-backup.timer` runs `sharkord-manager backup --all --unattended` daily. Both services are `Type=oneshot`.

```ini
[Timer]
OnBootSec=2min
OnUnitActiveSec=5min

[Install]
WantedBy=timers.target
```

```ini
[Timer]
OnCalendar=daily
RandomizedDelaySec=1h
Persistent=true

[Install]
WantedBy=timers.target
```

## Operations, health check and repair

### Steps that can resume

Every operation (create, delete, update, switch type, unlock, restore, import) is an ordered list of steps. Each step checks whether its result already exists before acting, so running it twice is harmless.

```go
type Step interface {
    Name() string                          // stable; stored in the record
    Done(ctx context.Context) (bool, error) // is the result already in place?
    Do(ctx context.Context) error           // make it so; safe to repeat
}
```

- For each step: skip it if `Done` is true; otherwise run `Do`, then check `Done` again to confirm
- After each step, save its name in the instance's `operation` field
- On an error, stop, keep the operation's name and failed step in the record, and offer "Retry", plus "Undo" for a create
- If the manager is killed, the next start and `repair` resume from the saved step

### Create, step by step

1. Add the record as `creating`, with its ports reserved
2. Create the system user
3. Download and check the Sharkord version, unless it's already in `/opt/sharkord`
4. Create the data folder in the chosen storage location, or the LUKS container and its mount
5. Write the env file, the version link, the data drop-in and, for encrypted instances, the encrypted drop-in
6. First start outside systemd: capture the owner token into the vault
7. Enable and start the service, then wait for `/healthz`
8. Add the firewall rules
9. Write the Caddy site file, validate, reload
10. Wait up to 2 minutes for the certificate; if it isn't there yet, finish anyway and show it as pending, since Caddy keeps trying
11. Set the record to `active`

Delete uses the 11 steps on the checklist tab, in that order.

### Change domain, step by step

1. Check the new domain: valid, not used by another instance, and its DNS pointing here
2. Write the new domain's site file as `/etc/caddy/sites/<id>.next.caddy`, validate, and reload, so both domains work for a moment
3. Wait for the new domain's certificate
4. Move `<id>.next.caddy` over `<id>.caddy`, validate, and reload
5. Delete the old domain's certificate folder, and save the new domain in the record

Ports, firewall rules and the instance itself stay as they are. Members need the new address.

### Health check

The timer and the menu run the same checks and save the results to `/var/lib/sharkord-manager/health.json`:

- Each instance: unit active (`systemctl show -p ActiveState`), `/healthz` answering within 3 seconds, container mounted if encrypted, disk use, and a warning once a container is over 80% full
- Certificates: present, and days left. Warn when less than a sixth of a certificate's lifetime is left: Caddy renews with a third left, so this means renewals are failing
- The root certificate: warn once it has under 2 years and 90 days left, because Caddy refuses to start at 2 years with this config
- Public IPv4 address: if it changed, rewrite every env file, restart the instances and renew the UPnP mappings
- Firewall rules and site files present for each instance; Caddy active
- Free disk space under 10%
- Encrypted instances waiting to be unlocked, shown as a notice rather than an error; on the first run after a boot, also send an alert
- New Sharkord versions: compare each instance's version with the cached release list, and notify once per new tested version

Send an email or webhook only when a problem appears or clears, by comparing with the previous `health.json`.

### Repair

- Resume any instance with an `operation` set
- For `active` instances, rerun the create steps' `Done` checks and redo anything missing; restart instances that stopped unexpectedly
- List leftovers carrying the manager's marker but missing from the records (site files, env files, `sharkord-*` users, firewall rules labelled `sharkord:`, UPnP mappings, containers) and ask before removing each. Never remove a data folder or container without asking
- `repair --dry-run` prints what it would change

## Login, vault and owner tokens

### Who has to log in

- Started by root itself, with no `SUDO_USER` (the timers): no login
- Started through sudo: the menu opens on a login screen, with the username filled in from `SUDO_USER`. Commands prompt on the terminal, or read the password from `--password-stdin` for scripts
- After logging in, check that the user is in `sharkord-admins`: `user.Lookup(name)`, then `GroupIds()`, compared with `user.LookupGroup("sharkord-admins")`. With cgo on, `os/user` goes through NSS, so directory accounts work too
- Log every action as the logged-in user, plus the SSH user if they differ

### PAM

Ship `/etc/pam.d/sharkord-manager`, so the login follows the same rules as the server's own logins:

```
# Managed by sharkord-manager
@include common-auth
@include common-account
```

```go
t, err := pam.StartFunc("sharkord-manager", username, func(s pam.Style, msg string) (string, error) {
    switch s {
    case pam.PromptEchoOff:
        return askHidden(msg) // the password, or a two-factor code
    case pam.PromptEchoOn:
        return askVisible(msg)
    default: // pam.ErrorMsg, pam.TextInfo
        showMessage(msg)
        return "", nil
    }
})
// then t.Authenticate(0), t.AcctMgmt(0), and t.End() when finished
```

Forward every prompt to the screen rather than assuming one password prompt, so two-factor modules work. After 5 failed logins for a username within 15 minutes, refuse that username for 5 minutes (counts kept in `/var/lib/sharkord-manager/logins.json`). After 10 idle minutes, return to the login screen and drop the unlocked vault from memory. Run PAM again before showing an owner token, deleting an instance, restoring a backup or exporting with a token.

### sudoers

`/etc/sudoers.d/sharkord-manager`, owned by root, mode 440. The file name must not contain a dot, or sudo skips it.

```
# Managed by sharkord-manager
%sharkord-admins ALL=(root) NOPASSWD: /usr/bin/sharkord-manager
```

### Vault

`/var/lib/sharkord-manager/vault/`, owned by root, mode 700:

- `identity.age`: the vault's age X25519 identity, encrypted with the vault passphrase (`age.NewScryptRecipient`)
- `recipient.txt`: the identity's public key (`age1...`), so scheduled backups can encrypt without unlocking anything
- `canary.age`: a known text encrypted to the recipient, to check that a key or passphrase is right
- `secrets/<id>/owner-token.age` and `secrets/<id>/luks-key.age`, encrypted to the recipient

The vault operations:

- **Setup:** `age.GenerateX25519Identity()`; save `identity.age` and `recipient.txt`. Show `identity.String()` (`AGE-SECRET-KEY-1...`) once as the recovery key, and have the admin type its last 6 characters back before continuing
- **Unlock:** decrypt `identity.age` with `age.NewScryptIdentity(passphrase)`, parse it with `age.ParseX25519Identity`, check it against `canary.age`, and keep it in memory for the session only
- **Recover:** parse the pasted recovery key, check it against `canary.age`, then set a new passphrase
- **Change passphrase:** unlock, then write a new `identity.age` atomically

### Owner tokens

- **Capture:** in the first start's output, strip ANSI colour codes, then take the first match of `[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}` after the words "access token". Sharkord makes the token with Bun's `randomUUIDv7()`
- **Check:** open the instance's `db.sqlite` read-only and compare `SELECT secret_token FROM settings` with the lowercase hex SHA-256 of the token. Do this both for captured tokens and for tokens an admin pastes in
- **Save:** encrypt it to `secrets/<id>/owner-token.age`
- **Show:** run PAM again, unlock if needed, decrypt, display, and clear the screen on the next keypress

## Instances

### Releases and download

- List releases with `GET https://api.github.com/repos/sharkord/sharkord/releases?per_page=30`, sending `Accept: application/vnd.github+json` and `User-Agent: sharkord-manager/<version>`. Unauthenticated calls are limited to 60 an hour, so cache the answer for an hour. Skip drafts and pre-releases
- Pick the asset by `runtime.GOARCH`: `sharkord-linux-x64` for amd64, `sharkord-linux-arm64` for arm64
- Download it with `release.json` from the same release. That file holds `{version, releaseDate, artifacts: [{name, target, size, checksum}]}`, where `checksum` is the SHA-256 in hex. Check size and checksum for the matching `name`
- Install to `/opt/sharkord/<version>/sharkord` (root, mode 755) through a temporary file and a rename
- The checksum catches a broken download, not a tampered release, since both files come from the same place

### System user and ports

- `useradd --system --user-group --no-create-home --home-dir <data path> --shell /usr/sbin/nologin sharkord-<id>`
- Take the lowest port in each range that isn't in the records, then confirm it's free: for HTTP, listen on `127.0.0.1:<port>` over TCP and close; for WebRTC, bind `0.0.0.0:<port>` over both UDP and TCP and close
- Save the ports in the record before anything uses them

### First start

- Run `/opt/sharkord/<version>/sharkord` directly as the instance's user (`SysProcAttr.Credential`), with the env file's variables, the data folder as working directory, and stdout and stderr piped
- Read the output until the token appears and `/healthz` answers, polling every 500 ms for up to 90 seconds; the first start unpacks the web interface and sets up the database
- Send SIGTERM, wait up to 10 seconds, then SIGKILL
- Check the token against the database, save it to the vault, then `systemctl enable --now sharkord@<id>`
- If no token appears but the database already has settings (a retry after a crash), the token was shown once and lost. Say so, and let the admin carry on and use "Save owner token" later

### Settings

Sharkord runs every override through `JSON.parse` and keeps the raw text only when that fails. So write each value so it parses to the right type:

- Numbers and booleans bare, and booleans only as lowercase `true` or `false`; `False` would parse as text and switch the setting on
- Strings as a JSON string inside single quotes: `SHARKORD_OIDC_CLIENT_ID='"12345"'`. Written bare, a digits-only value becomes a number, fails Sharkord's checks and stops it starting
- Lists as a JSON array inside single quotes: `SHARKORD_ALLOWED_ORIGINS='["https://chat-a.example.com"]'`
- To go back to the default, remove the line; Sharkord ignores empty values

Test this with a digits-only OIDC client ID before relying on it.

### Updates and rollback

1. Back up the instance
2. Download the new version if it isn't in `/opt/sharkord` yet
3. Stop the service, point the version link at the new binary, and start it again
4. Wait up to 90 seconds for `/healthz`
5. If it doesn't answer: stop, point the link back, restore the database from step 1, start, and report what happened

Remove a version from `/opt/sharkord` once no instance links to it and it isn't the newest.

## Storage locations

A location is a folder on a mounted drive. An instance's data lives at `<location>/<id>`. An encrypted instance's container is `<location>/<id>.luks`, mounted on `<location>/<id>`.

### Adding a location

1. Find the drive holding the path: `findmnt -J -o TARGET,SOURCE,FSTYPE,OPTIONS,UUID --target <path>`
2. Refuse it if the file system isn't `ext4`, `xfs` or `btrfs`, if its options include `noexec`, or if the path is under `/tmp`, `/var/tmp`, `/run`, `/media`, `/boot`, `/proc`, `/sys` or `/dev`
3. Refuse it unless the drive is the root file system or has a line in `/etc/fstab` (matched by UUID, label or mount point), so it comes back after a reboot
4. Create the folder owned by root with mode 711, so instance users can pass through it but not list it
5. Check that every parent folder lets others pass through (the `x` bit for others); if one doesn't, name it and offer `chmod o+x`
6. Save the location with the drive's UUID, so the health check notices if a different drive appears at that path

### Mounting a formatted drive

1. List candidates with `lsblk -J -o PATH,SIZE,TYPE,FSTYPE,UUID,LABEL,MOUNTPOINTS`: partitions formatted ext4, XFS or Btrfs with no mount point
2. Create the mount point, `/srv/sharkord-<label>`
3. Copy `/etc/fstab` to `/etc/fstab.sharkord-manager.bak`, then append the line below. Use `0 0` at the end for XFS and Btrfs, which skip the boot-time check
4. Run `findmnt --verify`; if it reports errors, put the backup back and stop
5. Run `systemctl daemon-reload` and `mount /srv/sharkord-<label>`, then confirm with `findmnt`
6. Carry on with "Adding a location" for a folder on the drive

```
# Added by sharkord-manager
UUID=6f1c2a9e-3b7d-4c55-9e0a-2d4f8b1c7e63 /srv/sharkord-disk2 ext4 defaults,nofail 0 2
```

### Space

- Read free space with `unix.Statfs(path)` as `Bavail × Bsize`
- Require at least 2 GiB free for a plain instance, and the container size plus 1 GiB for an encrypted one
- On Btrfs, create the container file empty, run `chattr +C` on it, then `fallocate` it. Copy-on-write fragments a large file that's rewritten in place, and the flag only takes effect on an empty file

### Moving an instance

These are resumable steps, like any other operation:

1. Check free space at the new location
2. Stop the service; for an encrypted instance, also unmount and close its container
3. Copy with `rsync -aHAX --numeric-ids <old>/ <new>/` for a plain folder, or copy the container file for an encrypted one
4. Check the copy: open the copied `db.sqlite` read-only, run `PRAGMA integrity_check`, and compare file counts and sizes. For a container, open it from its new place and do the same inside
5. Write the new `SHARKORD_DATA_PATH`, `data.conf` and, if encrypted, `encrypted.conf`, then `systemctl daemon-reload`
6. Start the service and wait for `/healthz`
7. Show the old copy's path and size, and delete it once the admin confirms

This makes `rsync` a package dependency.

### Backup location

- The same picker, but any local file system will do: backups are encrypted files, so they need neither Linux permissions nor the `noexec` check
- Scheduled backups still need the drive listed in `/etc/fstab`; warn if it isn't

### Health

- For each location, check that `findmnt --target` still shows the saved UUID, and report free space
- An instance whose drive is missing fails to start because of `RequiresMountsFor=`; report it as "drive missing", not as a crash

## Encrypted data folders

The key is 64 random bytes from `crypto/rand`, saved in the vault as `luks-key.age`. It always reaches `cryptsetup` on stdin with `--key-file=- --keyfile-size=64`; from stdin, cryptsetup reads the whole key rather than stopping at a newline. `<c>` below is `<location>/<id>.luks`, and `<d>` is `<location>/<id>`.

### Create

```
fallocate -l 10G <c>                    # on Btrfs: create empty, chattr +C, then fallocate
cryptsetup luksFormat --type luks2 --batch-mode --key-file=- --keyfile-size=64 <c>
cryptsetup open --type luks2 --key-file=- --keyfile-size=64 <c> sharkord-<id>
mkfs.ext4 -q -L sharkord-<id> /dev/mapper/sharkord-<id>
mkdir -p <d>
mount -o nodev,nosuid /dev/mapper/sharkord-<id> <d>
chown sharkord-<id>:sharkord-<id> <d> && chmod 700 <d>
cryptsetup luksHeaderBackup <c> --header-backup-file <temp file>
```

Never mount with `noexec`. Encrypt the header backup into the vault folder and delete the temporary file: a damaged header makes the data unreadable even with the key.

### Unlock after a reboot

For each encrypted instance, once an admin has unlocked the vault:

1. Decrypt the key and `cryptsetup open` the container
2. Run `e2fsck -p /dev/mapper/sharkord-<id>`; exit codes 0 and 1 are fine, 4 or higher means stop and report
3. `mount -o nodev,nosuid` it on `<d>`, then `systemctl start sharkord@<id>`

### Lock

`systemctl stop sharkord@<id>`, `umount <d>`, then `cryptsetup close sharkord-<id>`.

### Grow

Grow with the container closed, so the loop device cryptsetup creates for it picks up the new size:

1. Lock the instance (above)
2. `fallocate -l <new size> <c>`
3. Open it, then run `cryptsetup resize sharkord-<id>` with the key on stdin
4. Mount it, run `resize2fs /dev/mapper/sharkord-<id>`, and start the service

### Erase

Lock the instance, run `cryptsetup erase --batch-mode <c>` to wipe every key slot, then delete the file and the vault's `luks-key.age` and header backup. Once the key slots are gone, nothing on the disk can be decrypted.

## Backups, export and import

### One archive format

Backups and exports are the same archive: tar, compressed with zstd, then encrypted with age. Backups are encrypted to the vault's recipient. Exports use a passphrase chosen for that export (`age.NewScryptRecipient`).

```
manifest.json
db.sqlite          # from VACUUM INTO
data/config.ini
data/uploads/...
data/public/...
data/plugins/...
data/plugin-data/...
```

```json
{
  "format": 1,
  "kind": "export",
  "createdAt": "2026-11-09T12:00:00Z",
  "managerVersion": "1.0.0",
  "sharkordVersion": "0.0.30",
  "instance": {
    "id": "chat-a",
    "domain": "chat-a.example.com",
    "tls": "letsencrypt",
    "encrypted": true,
    "settings": { "SHARKORD_WEBRTC_MAX_BITRATE": "30000000" }
  },
  "ownerToken": null
}
```

### Making a backup or export

The instance can keep running:

1. Open `<d>/db.sqlite` read-only (`file:<path>?mode=ro`) and run `VACUUM INTO '<staging>/db.sqlite'`. On a live WAL database this gives a consistent copy that includes changes still in the WAL file (tested with SQLite)
2. Then add the files, skipping the database and its `-wal` and `-shm` files, `interface/`, `drizzle/`, `mediasoup/`, `tmp/`, `logs/` and Sharkord's own `backups/`. Copying the database first means every upload it refers to is already on disk
3. Stream tar into zstd into age, to a temporary file in `<backupLocation>/<id>/`, then rename it to `<UTC time>.tar.zst.age` with mode 600
4. Record its size and SHA-256 in `<backupLocation>/<id>/index.json`, then delete the oldest backups beyond the number kept

### Restore

1. Ask for the password again and unlock the vault
2. Decrypt and unpack into a new folder (or a new container) beside the current one. Accept only plain files and folders with relative paths, no `..`, no links and no device files
3. Give the files to the instance's user (folders 700, files 600)
4. Stop the service, swap the new folder in, start it and wait for `/healthz`; if it doesn't answer, swap back
5. Delete the old folder once the admin confirms

### Import

Read the start of the file to choose what to ask for: an age header with an `scrypt` stanza is an export (ask for its passphrase), one with an `X25519` stanza is a backup (unlock the vault). A folder containing `db.sqlite` is a Sharkord data folder:

- **Plain install:** `~/.config/sharkord` of the user who ran it, or the `SHARKORD_DATA_PATH` in its systemd unit
- **Docker:** run `docker inspect <container> --format '{{json .Mounts}}'` and take the source of the mount at `/home/bun/.config/sharkord`. Offer `docker stop` for the final copy

For a folder:

1. Read the version from the highest-numbered folder in `interface/`, and refuse if it's newer than any version the manager offers
2. Walk the source with `Lstat`, and refuse links, device files and pipes
3. Find out whether something still has the database open: compare the device and inode of `db.sqlite` with those of every `/proc/*/fd/*` target. This also works for processes inside containers, whose paths differ
4. If it's in use, copy with `rsync -aHAX --numeric-ids` (skipping `interface`, `drizzle`, `mediasoup` and `tmp`) while it runs, then ask for it to be stopped and copy again with `--delete`
5. Give the files to the new instance's user
6. Carry over settings from `config.ini`, except the host, ports, announced address and auto-update, which the manager sets. Sharkord writes lists as repeated `name[]=value` lines, so load the file with `ini.LoadOptions{AllowShadows: true}` and read `ValueWithShadows()`
7. Ask for the owner token and check it against the database
8. Continue with the create steps from "Write the env file"

## Shiver plugin

[Shiver](https://github.com/GenericD00d/Shiver) attaches its server plugin to each release as `shiver-plugin-<version>.zip`, with checksums in `SHA256SUMS.txt`. The plugin has its own version number: release v0.2.7 carries plugin 0.3.2.

### Install or update, step by step

1. Ask `GET https://api.github.com/repos/GenericD00d/Shiver/releases/latest` (cached for an hour), and pick the asset matching `shiver-plugin-*.zip`, plus `SHA256SUMS.txt`
2. Download both, and check the zip's SHA-256 against its line in `SHA256SUMS.txt`
3. Unpack it into a temporary folder by the import rules: relative paths only, no links or device files. Accept the plugin files at the top level or inside one folder, as Sharkord's own installer does
4. Check `manifest.json`: `id` is `shiver`, and `sdkVersion` equals the instance's plugin SDK version. Sharkord refuses any other SDK version, and 0.0.30 uses SDK 2, so keep the SDK version beside each entry in the tested Sharkord list
5. Back up the instance and stop it
6. Rename the current `<data>/plugins/shiver` aside, move the new folder in, and give it to the instance's user (folders 700, files 600)
7. First install only: switch plugins on (below)
8. Start the instance, wait for `/healthz`, then read its journal since the start for `Plugin loaded: shiver@v<version>`. On `Failed to load plugin shiver`, put the old folder back, restart, and show Sharkord's error
9. Delete the old folder

### Switching plugins on

Sharkord keeps both switches in its database, and both are off by default: `settings.enable_plugins` for the server, and a `plugin_data` row for each plugin. This is the one place the manager writes to Sharkord's database. Do it only for Sharkord versions on the tested list, with the instance stopped, after the backup, and after checking with `PRAGMA table_info` that the columns exist:

```sql
UPDATE settings SET enable_plugins = 1;
INSERT INTO plugin_data (plugin_id, enabled, settings) VALUES ('shiver', 1, '{}')
  ON CONFLICT(plugin_id) DO UPDATE SET enabled = 1;
```

On other versions, show the admin where to switch them on in Sharkord: plugins in the server settings, then Shiver under Settings → Plugins. Once both are on, `GET http://127.0.0.1:<port>/plugin-bundle/shiver/client/index.js` answers 200; it answers 403 while plugins are off and 404 while Shiver is.

### Updates and removal

- The health check compares each instance's installed `manifest.json` version with the latest release's plugin, and notifies once per new version
- "Update Shiver everywhere" runs the steps above one instance at a time
- Removal stays in Sharkord's Settings → Plugins, which also deletes Shiver's per-user rows, its capabilities and its data folder. The manager notices the folder is gone and updates its record

### Trust

`SHA256SUMS.txt` comes from the same release as the zip, so it catches a broken download, not a tampered release. The plugin's server half runs inside every instance that installs it. Shiver already signs desktop updates with minisign; signing `SHA256SUMS.txt` the same way would let the manager check its origin against a public key built into the manager (`github.com/jedisct1/go-minisign`). Listing Shiver in Sharkord's plugin marketplace (`plugins.json` in the `Sharkord/plugins` repository, pointing at the same zip and checksum) would also let admins install it from Sharkord itself.

## Caddy and certificates

Everything below was run on Caddy 2.11.9.

### Main Caddyfile

The manager's global settings live in their own file, pulled into the options block with one `import` line. If `/etc/caddy/Caddyfile` is still the package's default welcome site, replace it with the layout below. Otherwise add only the two `import` lines, showing the admin the change first; Caddy allows only one options block, so put the first line inside theirs.

```caddyfile
{
    import /etc/caddy/sharkord-manager/global.caddy
}

import /etc/caddy/sites/*.caddy
```

`/etc/caddy/sharkord-manager/global.caddy` (add `email <address>` here if the admin gives one; Caddy then also uses ZeroSSL as a backup):

```caddyfile
skip_install_trust
pki {
    ca local {
        name "Sharkord Manager"
        root_cn "Sharkord Manager Root"
        intermediate_lifetime 730d
        renewal_window_ratio 0.5
    }
}
```

To keep other local users away from Caddy's admin interface, add the line below to `global.caddy`. Caddy creates the socket writable by its owner only, and the packaged service runs its reload command as the `caddy` user, so reloads keep working (tested on Caddy 2.11.9). Make the switch with `systemctl restart caddy`, not a reload: a reload would look for the new socket before Caddy has opened it.

```caddyfile
admin unix//var/lib/caddy/admin.sock
```

### Site files

`/etc/caddy/sites/<id>.caddy`. Let's Encrypt:

```caddyfile
# Managed by sharkord-manager (instance chat-a). Do not edit.
chat-a.example.com {
    reverse_proxy 127.0.0.1:5000
}
```

Test mode adds `tls { ca https://acme-staging-v02.api.letsencrypt.org/directory }` inside the site. Self-signed, with the root certificate served over plain HTTP and every other HTTP request redirected:

```caddyfile
# Managed by sharkord-manager (instance chat-b). Do not edit.
chat-b.example.com {
    tls {
        issuer internal {
            lifetime 365d
        }
    }
    reverse_proxy 127.0.0.1:5001
}

http://chat-b.example.com {
    handle /sharkord-root.crt {
        root * /var/lib/caddy/.local/share/caddy/pki/authorities/local
        rewrite * /root.crt
        header Content-Type application/x-x509-ca-cert
        file_server
    }
    handle {
        redir https://{host}{uri} permanent
    }
}
```

Inside every site block, also add an error page for when the instance is down. Caddy answers 502 when it can't reach the instance, so catch that and reply 503 with a short message (tested on Caddy 2.11.9):

```caddyfile
handle_errors 502 {
    respond "This server is offline for maintenance. Try again in a few minutes." 503
}
```

### Validate and reload

1. Keep the previous site file as a backup until the change works
2. Validate as Caddy's own user, with `HOME=/var/lib/caddy`: `caddy validate --config /etc/caddy/Caddyfile`. Run as root, it creates a second certificate authority in root's home
3. On failure, put the backup back and show Caddy's error
4. Run `systemctl reload caddy`. The packaged service reloads with `--force`, so a reload retries certificates even when nothing changed, which is what "Retry now" needs

### Certificate status

- Read `/var/lib/caddy/.local/share/caddy/certificates/<issuer>/<domain>/<domain>.crt`, where `<issuer>` is `acme-v02.api.letsencrypt.org-directory`, `acme-staging-v02.api.letsencrypt.org-directory` or `local`
- Parse it with `crypto/x509` for the expiry date; a missing file means "pending"
- For the last error, read `journalctl -u caddy -o json` for entries whose message is "could not get certificate from issuer" and whose `identifier` is the domain

### Root certificate

- Members download it from `http://<domain>/sharkord-root.crt`
- Show its SHA-256 fingerprint in the menu, and ask the admin to send it to members another way (chat, email) so they can compare it before trusting the file, since the download isn't encrypted
- Offer "Save root certificate to a file" as well

### Switching type

Write the new site file, validate, reload, wait for the new certificate, then delete the domain's folder under the old issuer.

### Keeping Caddy updated

Ubuntu's automatic updates only cover its own archive. Check that `/etc/apt/apt.conf.d/20auto-upgrades` turns them on (`APT::Periodic::Unattended-Upgrade "1";`), then add Caddy's repository in `/etc/apt/apt.conf.d/52sharkord-manager`, using the `o=` value that `apt-cache policy caddy` shows for it. Apt adds this to Ubuntu's own list, so Ubuntu's security updates carry on as before.

```
Unattended-Upgrade::Origins-Pattern {
    "origin=<the o= value>";
};
```

## Firewall and network

### One interface, two backends

```go
type Rule struct {
    Port     int
    Proto    string // "tcp" or "udp"
    Instance string // "" for Caddy's 80 and 443
}

type Firewall interface {
    Active(ctx context.Context) (bool, error)
    Allow(ctx context.Context, r Rule) error  // no-op if already there
    Remove(ctx context.Context, r Rule) error // no-op if already gone
    List(ctx context.Context) ([]Rule, error) // only rules the manager added
}
```

Pick the backend at setup: firewalld if `systemctl is-active firewalld` says active, else ufw if `ufw status` says active. If neither is active, offer to turn on ufw. Don't manage raw nftables.

**ufw:**

- Allow: `ufw allow 40001/udp comment 'sharkord:chat-a'`, then the same for tcp. Caddy gets `80/tcp`, `443/tcp` and `443/udp`, labelled `sharkord:caddy`
- List and remove: read `ufw status numbered` and pick the lines ending `# sharkord:<id>`, including their `(v6)` twins. Delete them by number, highest first, with `ufw --force delete <n>`, so the numbering doesn't shift under you
- Turning it on: allow the SSH port first, then `ufw --force enable`

**firewalld:** one service per instance, so the label is the service name:

```
firewall-cmd --permanent --new-service=sharkord-chat-a
firewall-cmd --permanent --service=sharkord-chat-a --add-port=40001/udp
firewall-cmd --permanent --service=sharkord-chat-a --add-port=40001/tcp
firewall-cmd --permanent --add-service=sharkord-chat-a
firewall-cmd --reload
```

Remove with `--remove-service`, then `--delete-service`, then `--reload`. For Caddy, add the built-in `http` and `https` services plus port `443/udp`. Use the default zone.

### Public IPv4 address

- Ask `https://ipv4.icanhazip.com`, `https://api.ipify.org` and `https://ifconfig.me/ip` over IPv4 only (a `tcp4` dialer), 3 seconds each, and accept an answer when two agree
- Let the admin set a fixed address instead
- Writing `SHARKORD_WEBRTC_ANNOUNCED_ADDRESS` also stops Sharkord making its own lookup at each start

### DNS check

- Resolve the domain's A records through 1.1.1.1 and 8.8.8.8 (`net.Resolver` with `PreferGo` and a custom `Dial`), falling back to the system resolver if port 53 is blocked
- Pass when every A record is the public IPv4 address
- If the domain also has an AAAA record, it must point at this server too, or be removed: Let's Encrypt tries IPv6 first when one exists

### Router (UPnP)

- Find the router with `internetgateway2.NewWANIPConnection1Clients()`, then the `2` and `NewWANPPPConnection1Clients()` variants
- `GetExternalIPAddress()`: an address in 100.64.0.0/10, or one different from the public IPv4, means carrier-grade or double NAT
- `AddPortMapping("", port, "UDP", port, <LAN IP>, true, "sharkord-manager chat-a", 0)`, and again for TCP. If the router refuses a lease of 0, use 86400 and renew it in the health check
- Find the LAN IP by opening a UDP socket towards the router and reading its local address
- Remove with `DeletePortMapping("", port, "UDP")` and the TCP equivalent

### When the public IP changes

Once two lookups agree on a new address: save it, rewrite every env file, restart the instances one at a time (waiting for `/healthz` each time), renew the UPnP mappings, and notify the admin. Without a dynamic DNS service, warn that the domains still point at the old address.

## Menu and commands

The menu and the commands both call `internal/ops`, passing a callback that reports each step as it runs. The commands print the steps as lines; the menu draws them as a progress list.

### Commands

```
sharkord-manager                                   # opens the menu
sharkord-manager setup
sharkord-manager instance list | show <id>
sharkord-manager instance create --id <id> --domain <domain>
    [--tls letsencrypt|selfsigned] [--test-mode] [--location <path>]
    [--no-encrypt] [--version <v>]
sharkord-manager instance start | stop | restart | logs [--follow] <id>
sharkord-manager instance set <id> KEY=VALUE...
sharkord-manager instance update <id> [--version <v>]
sharkord-manager instance move <id> --to <location>
sharkord-manager instance delete <id>
sharkord-manager token show | save <id>
sharkord-manager shiver status | install <id> | update <id> | update --all
sharkord-manager cert status [<id>] | switch <id> --tls <type> | retry <id> | root [--out <file>]
sharkord-manager backup <id> | --all [--unattended]
sharkord-manager restore <id> <file>
sharkord-manager export <id> --out <file> [--include-token]
sharkord-manager import <file|folder> --id <id> --domain <domain> [--location <path>]
sharkord-manager storage list | add <path> | mount <device> | remove <path> | default <path>
sharkord-manager firewall status | repair
sharkord-manager vault unlock | change-passphrase | recover
sharkord-manager admins list | add <user> | remove <user>
sharkord-manager health [--unattended]
sharkord-manager repair [--dry-run]
sharkord-manager version
```

- **Global flags:** `--json` (machine-readable output, no colour), `--yes` (skip confirmations, but never the login or the password check before showing a token), `--password-stdin`, `--passphrase-stdin`
- **Exit codes:** 0 done; 1 error; 2 bad usage; 3 login or vault unlock needed; 4 another run holds the lock; 5 stopped partway, with the operation saved for `repair`

### Menu screens

- **Login:** username filled in from `SUDO_USER`, hidden password, and any extra PAM prompts shown as further fields
- **Main:** one row per instance with its status, domain, days left on its certificate and storage location; health notices across the top; key hints at the bottom
- **Actions:** each is a form, then a confirmation screen listing what will change, then the progress list. On a failure it offers "Retry" and, for a create, "Undo"
- **Help:** the "Steps outside the manager" text, embedded in the binary with `embed`, shown inside the menu
- Keep every screen usable at 80×24 and without colour (`NO_COLOR`)

### Look and feel

Keep the menu in line with Sharkord's own design:

- **Colours:** Sharkord's client uses a neutral grey theme (`apps/client/src/index.css`), with red (`--destructive`) only for destructive actions. In the terminal, use the default text colour, grey for secondary text, and red only for confirming deletes, restores and other destructive actions. Check every screen on both light and dark terminals
- **Words:** take names from Sharkord's English strings (`apps/client/src/i18n/locales/en/`): a "server" for each instance, the "secret token" for the owner token, and Settings → Plugins when pointing into Sharkord
- **A web interface,** if one is ever added, should use Sharkord's own stack (React with Radix UI and shadcn/ui) and the same theme variables

## Packaging

### Building

- PAM needs cgo, so build each architecture on its own runner instead of cross-compiling: GitHub Actions' `ubuntu-24.04` for amd64 and `ubuntu-24.04-arm` for arm64, each with `libpam0g-dev` installed
- Build on Ubuntu 24.04, the oldest supported base. A binary built against a newer glibc won't start on older systems; build on 22.04 if Ubuntu 22.04 and Mint 21 are ever added
- `CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" ./cmd/sharkord-manager`
- Check the sudoers file in CI with `visudo -cf packaging/sudoers`

### The .deb (GoReleaser with nfpm)

```yaml
nfpms:
  - package_name: sharkord-manager
    formats: [deb]
    license: MIT
    depends: [systemd, libpam0g, rsync]
    recommends: [ufw, cryptsetup]
    suggests: [miniupnpc, sqlite3, libnss3-tools]
    contents:
      - src: packaging/sudoers
        dst: /etc/sudoers.d/sharkord-manager
        type: config|noreplace
        file_info: { mode: 0440 }
      - src: packaging/pam
        dst: /etc/pam.d/sharkord-manager
        type: config|noreplace
      - src: packaging/sharkord@.service
        dst: /usr/lib/systemd/system/sharkord@.service
      - src: packaging/sharkord-manager-health.service
        dst: /usr/lib/systemd/system/sharkord-manager-health.service
      - src: packaging/sharkord-manager-health.timer
        dst: /usr/lib/systemd/system/sharkord-manager-health.timer
      - src: packaging/sharkord-manager-backup.service
        dst: /usr/lib/systemd/system/sharkord-manager-backup.service
      - src: packaging/sharkord-manager-backup.timer
        dst: /usr/lib/systemd/system/sharkord-manager-backup.timer
      - src: packaging/logrotate
        dst: /etc/logrotate.d/sharkord-manager
        type: config|noreplace
      - src: LICENSE
        dst: /usr/share/doc/sharkord-manager/copyright
    scripts:
      postinstall: packaging/postinst.sh
      preremove: packaging/prerm.sh
```

### Install scripts

`postinst.sh`, on `configure`:

```sh
#!/bin/sh
set -e
if [ "$1" = configure ]; then
  getent group sharkord-admins >/dev/null || groupadd --system sharkord-admins
  install -d -m 700 /var/lib/sharkord-manager /var/lib/sharkord-manager/vault
  install -d -m 700 /etc/sharkord-manager /etc/sharkord-manager/instances
  install -d -m 711 /var/lib/sharkord
  install -d -m 700 /var/backups/sharkord
  systemctl daemon-reload
  systemctl enable --now sharkord-manager-health.timer sharkord-manager-backup.timer
  echo "Run 'sudo sharkord-manager' to finish setting up."
fi
```

- `prerm.sh`, on `remove`: disable and stop the two timers only. Never stop, change or delete an instance
- On purge, leave `/var/lib/sharkord-manager` (the vault) and every data folder in place, and say where they are
- Logs: rotate `/var/log/sharkord-manager.log` weekly, keep 8, compressed, created as mode 600

### Releases

Publish the two .deb files with a `SHA256SUMS` file. An apt repository with a signing key can come later.

## Testing

### Unit tests

- Give `internal/run` a fake that records each command and returns canned output. Tests then assert the exact commands, including that no secret ever appears in an argument list
- Keep known-good copies of every generated file (env files, drop-ins, site files, the Caddyfile) under `testdata/`, compare against them, and regenerate them with a `-update` flag so changes show up in review
- Step engine: stop an operation after each step in turn, resume it, and check the result matches an uninterrupted run
- Feed the parsers real output saved from the target systems: `ufw status numbered`, `findmnt -J`, `lsblk -J`, `systemctl show`, Caddy's JSON journal, a Sharkord 0.0.30 first start, and a `config.ini` with `[]` lists
- Fuzz the ID and domain checks, archive unpacking (paths, links) and env value encoding with `go test -fuzz`

### Certificates without the internet

- Run Pebble (`github.com/letsencrypt/pebble/v2`), Let's Encrypt's test server, with `PEBBLE_VA_ALWAYS_VALID=1` so it skips the domain checks. It listens on port 14000 by default
- Point Caddy at it with the global options `acme_ca https://localhost:14000/dir` and `acme_ca_root <Pebble's test root>`, through a hidden `--acme-ca` setting in the manager

### VMs

- Ubuntu 24.04 and 26.04 through LXD (`lxc launch ubuntu:24.04 t1 --vm`) or Multipass; Mint 22 installed once from its ISO, then cloned from a snapshot
- On Ubuntu 26.04, `sudo` is sudo-rs and the core command-line tools are the Rust versions by default, so run the sudoers and install-script tests there as well
- Attach a second virtual disk (an LXD block volume, or a qcow2 file under libvirt) for the storage tests, and detach it before a reboot for the missing-drive test
- Run each firewall setup: ufw on, ufw off, firewalld
- arm64: an arm64 cloud server, or a Raspberry Pi 5 running Ubuntu Server 24.04

### Voice, end to end

- Run the browsers on a different VM from the server, so the traffic goes through the real firewall and ports
- Playwright with Chromium's `--use-fake-ui-for-media-stream` and `--use-fake-device-for-media-stream`; two browser contexts sign up, then join the same voice channel. Sharkord's own Playwright tests (`packages/e2e`) show the sign-up and navigation steps
- Keep a handle on the page's connections with `page.addInitScript`, wrapping `RTCPeerConnection` so each new one is pushed onto `window.__pcs`
- Pass when an `inbound-rtp` audio report from `getStats()` shows `bytesReceived` rising
- For self-signed instances, use `ignoreHTTPSErrors: true` here, and check certificate trust separately by hand

### Manual checks, written down each release

- The Sharkord desktop app on Windows, macOS and Linux, against both certificate types
- Installing the root certificate on an iPhone and an Android phone
- UPnP on a real home router

## Prototype first

These weren't tested while writing this guide. Try each in a VM before building on it.

- **Automatic unlock with a TPM:** check that `systemd-cryptenroll` can enroll a TPM on a container file (through its loop device), and how it would unlock at boot. If it can't, offer automatic unlock only for whole-drive locations, or drop the option
- **Env value quoting:** confirm systemd passes `'"12345"'` through as `"12345"`, and that Sharkord then reads it as text
- **Unit hardening:** run the voice test with the unit as written, then try `SystemCallFilter=@system-service` and `ProtectProc=invisible` one at a time
- **Growing a container:** walk through the grow steps and confirm the file system ends up the new size
- **ufw deletion:** check deleting by number, `(v6)` twins included, on the ufw versions in Ubuntu 24.04 and 26.04
- **Two-factor login:** try the PAM screen with a module that asks extra questions, such as `libpam-google-authenticator`
- **Desktop app on Linux:** confirm the Sharkord desktop app trusts a root added to `~/.pki/nssdb`. Also check whether `SHARKORD_ALLOWED_ORIGINS` affects the desktop app before ever narrowing it
- **Resetting a lost owner token:** writing a new token's SHA-256 into `settings.secret_token`, with the instance stopped, looks possible from Sharkord's code. But the README says that value also signs every session and file link, so everyone would be logged out. Ask Sharkord's developers before offering it
- **Each new Sharkord version:** recheck the facts this guide relies on: the env names, the data folder layout, the first-start output, `/healthz` and the token hash. Sharkord is still at 0.0.x
- **Blocking sleep on desktops:** while any instance runs, hold a sleep inhibitor from a small service (`systemd-inhibit --what=sleep:idle --mode=block --who=sharkord-manager --why="Sharkord instances are running" sleep infinity`), and check that Mint's power settings respect it

## Sources

- [Sharkord source code](https://github.com/sharkord/sharkord), v0.0.30: settings and env overrides, data paths, the voice server, first-start output, database setup (WAL), embedded files, release files
- [Caddy source code](https://github.com/caddyserver/caddy), v2.11.9: the certificate authority, the internal issuer, default issuers and Caddyfile options. The templates in this tab were run on a 2.11.9 build
- [Caddy's packaging](https://github.com/caddyserver/dist): the systemd service file (`User=caddy`, reload with `--force`) and the install script (home `/var/lib/caddy`)
- [CertMagic source code](https://github.com/caddyserver/certmagic): retries through the staging server, the one-third renewal window, certificate storage paths
- [Pebble source code](https://github.com/letsencrypt/pebble), v2.10.1: `PEBBLE_VA_ALWAYS_VALID` and the default port
- [Go module proxy](https://proxy.golang.org): current paths and versions of the Go libraries named here
- Tested while writing: `VACUUM INTO` on a live WAL database (Python's sqlite3), and the list format of the `ini` package version Sharkord uses (npm)
