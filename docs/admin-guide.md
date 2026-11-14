<img src="../logo.png" alt="Shiver Server Manager" width="72" align="right">

# Admin guide

Shiver Server Manager runs several [Sharkord](https://github.com/sharkord/sharkord) servers on one machine, each on its own domain, with Caddy handling HTTPS. Everything is done from a terminal menu (`sudo shiver-server-manager`) or the same actions as commands (`sudo shiver-server-manager --help`).

It isn't an official Sharkord project.

## Requirements

- One of these, on amd64 (x86_64) or arm64 (aarch64), with systemd:
  - Ubuntu 24.04 or 26.04, or a system built on them such as Linux Mint 22
  - Debian 12 or 13
  - Fedora 43 or newer
  - RHEL, AlmaLinux or Rocky Linux 9 or 10
  - openSUSE Tumbleweed or Leap 16
- A public IPv4 address that reaches the machine. Sharkord's voice server only listens on IPv4.
- Ports 80 and 443 free (no other web server), plus the voice ports (40000-40999 by default).
- For Let's Encrypt certificates, a domain name per server pointing at the machine.

## Installing

Download the package for your system and processor from the releases page (the `.deb` for Ubuntu, Mint and Debian, the `.rpm` for Fedora, RHEL and its rebuilds, and openSUSE), check it against `SHA256SUMS`, and install it:

    sha256sum -c --ignore-missing SHA256SUMS
    sudo apt install ./shiver-server-manager_<version>_<arch>.deb                          # Ubuntu, Mint, Debian
    sudo dnf install ./shiver-server-manager-<version>-1.<arch>.rpm                        # Fedora, RHEL, AlmaLinux, Rocky
    sudo zypper install --allow-unsigned-rpm ./shiver-server-manager-<version>-1.<arch>.rpm # openSUSE
    sudo shiver-server-manager

The package creates the `shiver-admins` group, the manager's folders and two timers (health check every 5 minutes, and backups, which run each server on its own schedule), and a sudoers rule so admins can start the manager without a sudo password. The manager's own login is the password check.

## First-time setup

The first run asks to run setup (System > First-time setup, or `setup`). It's safe to run again; each step leaves alone what's already in place. In order:

1. Checks the OS and that ports 80 and 443 are free.
2. Creates its folders and records, the `shiver-admins` group, and makes you the first admin.
3. Installs Caddy and writes its configuration. Ubuntu's, Debian's, Fedora's and RHEL's own Caddy is too old, so there it comes from the Caddy project's own repository (apt on Ubuntu and Debian, COPR on Fedora and RHEL); openSUSE's is used as it is. On openSUSE, a drop-in for `caddy.service` makes Caddy start from its Caddyfile (openSUSE's unit starts with `--resume`). If Caddy already serves other sites, it only adds two `import` lines to your Caddyfile, after showing you the change.
4. Moves Caddy's admin interface to a Unix socket only Caddy and root can use.
5. Finds the firewall (ufw or firewalld). With none, it offers to switch one on, allowing SSH first: ufw on Ubuntu and Debian, firewalld elsewhere. Opens TCP 80, 443 and UDP 443 for Caddy.
6. Installs `cryptsetup` for encrypted data folders.
7. Checks the clock is synchronised, finds the public IPv4 address, tells a VPS from a home server behind a router, and spots carrier-grade NAT.
8. Checks it can reach GitHub, Let's Encrypt and Caddy's repository, and that there's disk space.
9. On Ubuntu and Debian, adds Caddy to automatic security updates (unattended-upgrades only covers the distribution's own archive). dnf-automatic and openSUSE's updates already cover every repository.
10. On a desktop, offers to keep the computer awake.
11. Sets the vault passphrase and shows the **recovery key once**. Write it down or keep it in a password manager, away from the server: it's the only way back into the vault and every backup if the passphrase is lost.

Useful options: `--email` (Let's Encrypt expiry mails), `--public-ip` (set the address by hand, or `auto`), `--data-location` (the default folder for new and imported servers' data, instead of `/var/lib/sharkord`), `--container-size` (GiB for each new encrypted container, default 10), `--no-firewall`.

## Logging in

Admins are OS accounts in `shiver-admins`. Start the manager with `sudo shiver-server-manager`; it asks for your OS password through PAM, so it follows the server's own login rules, including two-factor modules. After 5 wrong passwords in 15 minutes, that username is paused for 5 minutes. The menu locks after 10 idle minutes. Showing a secret token, making a new one, deleting a server, restoring a backup and removing everything ask for the password again.

Scripts can pass `--password-stdin` and `--passphrase-stdin` (answers one per line, password first), `--yes` to skip confirmations and `--json` for machine-readable output. Exit codes: 0 done, 1 error, 2 bad usage, 3 login or vault needed, 4 another run holds the lock, 5 stopped partway (run `repair`).

## The web page

Everything the menu does is also on a web page, for servers without a desktop. It's off until you switch it on (System > Switch on the web page, or):

    sudo shiver-server-manager web enable

It only listens on the server itself (`127.0.0.1:8443`), so nothing new is exposed. Open it from your own computer through an SSH tunnel:

    ssh -L 8443:localhost:8443 you@your-server

then browse to http://localhost:8443 and log in with your OS username and password, the same as the menu (two-factor codes included).

On the Servers page, click a server to see its details: ports, data folder, limits, health and plugins. A tick next to a server means it's healthy, a warning sign that it needs attention, and an (i) that one of its plugins has an update (install it on the Shiver plugins page).

To reach it from anywhere without a tunnel, give it a domain pointing at the server; Caddy then serves it over HTTPS:

    sudo shiver-server-manager web enable --domain admin.example.com

That puts a root-level control panel on the internet behind your OS password, so prefer the tunnel unless you need it. Either way:

- Only `shiver-admins` members can log in, with the same pause after failed logins. Sessions end after 10 idle minutes.
- Showing a secret token, making a new one, deleting a server, restoring a backup, changing the vault passphrase and removing everything ask for your password again.
- Each action runs the same command the terminal runs, logged under your name with "(web)"; its progress shows live.
- The page refuses requests from other sites and unknown host names.

`web status` shows whether it's on; `web disable` switches it off and removes its domain.

## The menu

### 1. Servers

| Action | What it does |
| --- | --- |
| Create a server | Asks for an ID, a domain and a certificate type; everything else has a default. If the domain points at a proxy in front of the server (so its A record holds the proxy's address), say so and the DNS check is skipped (`--skip-dns-check`); self-signed servers never check DNS. Downloads Sharkord (checked against its release's checksums), creates the server's system user, encrypted container, files and service, saves the secret token in the vault, opens its voice port, writes its Caddy site and waits up to two minutes for its certificate. |
| Import a server | From an export, a backup, a Sharkord data folder (`~/.config/sharkord` of a plain install) or `docker:<container>`. A running source is copied while it runs, then you're asked to stop it for a short final copy. Settings in its `config.ini` can be carried over. You're asked for its secret token. |
| Export a server | An encrypted file (its own passphrase) to move the server elsewhere; the server keeps running. The secret token goes inside only if you ask. |
| Start, Stop, Restart | One server or all. A server you stop stays stopped: repair and the health check leave it alone. |
| Change settings | Sharkord's environment settings (bitrate, origins, proxies, single sign-on...). The server restarts; if it doesn't come back, the old settings are put back. Community settings (name, roles, channels, invites) stay in Sharkord's own admin screens. |
| Show logs | The service's journal and Sharkord's `error.log`. |
| Update Sharkord | Backs up, switches to the Sharkord release you pick (the newest by default) and restarts; if it doesn't answer within 90 seconds, goes back with the database from that backup. |
| Back up now / Restore a backup | See "Backups" below. |
| Change domain | Sets up the new domain and certificate first, then retires the old one. Members need the new address. |
| Limit memory and CPU | Caps one server so it can't slow the others. |
| Delete a server | Asks for your password and the server's ID, then removes its service, site, certificate, firewall rules, data (wiping an encrypted container's keys), secrets and user. Export it first if you might want it back. |
| Shiver plugins | The plugins on offer, install or update one, what each server has. See "Shiver plugins". |

### 2. Owner tokens

Sharkord prints a server's secret (owner) token once, at its first start; the manager captures it into the vault. **Show** asks for your password again and hides it on the next key. **Save** stores a token you have (for imported servers), after checking it against the server's database. **Make a new one** (`token new <id>`) replaces a lost or leaked token: it asks for your password again, backs the server up, stops it, puts the new token in its database, saves it in the vault and starts it again. The old token stops working. Sharkord also signs logins with the token, so everyone on the server has to log in again. Existing owners stay owners. Restoring a backup made before the change brings back the old token.

### 3. Certificates

Status and expiry; switch between Let's Encrypt and self-signed; **Retry now** (rechecks DNS, then has Caddy try again at once); **Test mode** (Let's Encrypt's staging server, for testing without hitting its limit of 5 certificates a week per domain); **Root certificate** (for self-signed servers: its fingerprint, the download link for members, and saving it to a file). Send members the fingerprint another way so they can check the download. The manager never falls back to self-signed by itself.

### 4. Firewall

Show the manager's rules, repair them, and **router port mapping** (UPnP, opt-in, for home servers). "Ports to open elsewhere" shows what to open in a cloud provider's firewall or a home router.

### 5. Security

Admins (add existing OS accounts, remove them; the last admin can't be removed), unlock the vault, change its passphrase, recover it with the recovery key, grow an encrypted server's storage, health alerts.

### 6. Storage

New and imported servers get their data folder in the **default location** (`/var/lib/sharkord` unless you change it). To use another folder, add it and make it the default in one step: `storage add /srv/data/sharkord --default` (or Storage > Add a folder with "Make it the default" on the web page). The create and import forms also let you pick a location for one server.

Storage locations (the default is `/var/lib/sharkord`), adding a folder on another drive, mounting a newly formatted drive permanently, moving a server between locations, the backup location and the default location. Only ext4, XFS and Btrfs drives listed in `/etc/fstab` are accepted, never `noexec` mounts.

### 7. System

First-time setup, health check, repair, help (the steps outside the manager), updating the manager, and **Remove everything**.

## Encrypted data folders

New servers keep their data in a LUKS container (`<location>/<id>.luks`) with its key in the vault. This protects a stolen disk or disk image, not a running server. After a reboot, encrypted servers stay stopped until an admin logs in and unlocks the vault (Security > Unlock the vault); the health check sends an alert after each reboot. Create a server with `--no-encrypt` to skip encryption.

A full container stops Sharkord saving messages; the health check warns at 80%. Grow it with Security > Grow storage.

## Backups

By default, backups run at 00:00 and 12:00 and keep the last 14 per server (one week). Each server can have its own schedule and retention, or none: Backups > Schedule on the web page, or `backup schedule <id>` (for example `--times 03:30 --days mon,thu --keep 8`, `--keep-days 30`, or `--off`). The newest backup is never dropped for its age, so a server whose backups are off keeps its last one. Delete single backups with Backups > Delete, or `backup delete <id> <file>`. They're encrypted to the vault's public key, so making them needs no passphrase; restoring needs the vault passphrase or the recovery key. They're in `/var/backups/sharkord/<id>/` unless you choose another backup location. Being encrypted, they're safe to copy off the server (see Help).

Restoring unpacks beside the current data and swaps it in while the server is stopped; if the server doesn't start, the previous data goes back.

## Shiver plugins

The Shiver plugins page (Shiver plugins in the terminal menu, or `plugin catalog`) lists the plugins on offer: [Shiver](https://github.com/GenericD00d/Shiver) itself (the server half of the Shiver apps), [Shiver Temp Share](https://github.com/GenericD00d/shiver-temp-share) and [Shiver Media Player](https://github.com/GenericD00d/shiver-media-player), with each one's latest version and what every server has. The list is `plugins.json` in the manager's own repository, read from GitHub (and kept for an hour), so new plugins appear without updating the manager. While the repository is private, reading it needs the GitHub token from System > Updates; without the list, the page says plugins are unavailable.

Install or Update fetches the plugin's latest release, checks it against the checksum published with it and checks its manifest (the right plugin, the plugin SDK of the server's Sharkord), and installs it with plugins switched on. The server is backed up and restarted, and if Sharkord can't load the plugin the previous copy goes back. To install several at once, tick them and choose Install selected: the server is backed up and restarted once for the lot, and if Sharkord can't load one, every previous copy goes back. From the terminal: `plugin add <id> <plugin>...`. The Servers page marks a server with an update waiting with an (i). Remove a plugin in Sharkord's own Settings > Plugins, which also deletes the data it keeps.

The older `shiver install`, `shiver update` and `shiver status` commands still work for Shiver itself.


## Other plugins

Servers > (a server's menu) > Install a plugin from a file uploads a Sharkord plugin as a .zip or .tar (.tar.gz, .tar.zst) and installs it the same way as Shiver: the archive must hold manifest.json, server/index.js and client/index.js (at the top or in one folder) and speak the plugin SDK of the server's Sharkord; the server is backed up, the plugin is swapped in while it's stopped and switched on, and if Sharkord can't load it the previous copy goes back. From the terminal: `plugin install <id> <file>`; `plugin list` shows what each server has. Only install plugins you trust: they run inside Sharkord with access to its data. Remove one in Sharkord's own Settings > Plugins.
## Health check and repair

The health check runs every 5 minutes and from System > Health check: servers running and answering, encrypted servers waiting to be unlocked, container use, certificates (including renewals failing) and the self-signed root's expiry, the public address (when it changes, every server is restarted to announce the new one), firewall rules, site files, Caddy, free space and missing drives, and new Sharkord and Shiver versions. Set `health alerts --webhook <url>` or `--email-to ... --smtp host:port` to be told when problems appear and clear.

**Repair** makes the system match the manager's records: it finishes interrupted creates, deletes and other operations, redoes anything missing (files, service, firewall rules, site files), restarts servers that stopped unexpectedly, and offers to remove leftovers that belong to no server. It never recreates a missing data folder, and only removes one when you answer yes.

## What the manager changes on the system

| Path | What |
| --- | --- |
| `/usr/bin/shiver-server-manager` | The program |
| `/etc/sudoers.d/shiver-server-manager`, `/etc/pam.d/shiver-server-manager` | Admin access and login |
| `/var/lib/shiver-server-manager/` | Records (`state.json`), the vault, health report, version links (root only) |
| `/etc/shiver-server-manager/instances/<id>.env` | Each server's settings (root only) |
| `/etc/shiver-server-manager/alerts.json` | Alert settings (root only) |
| `/var/log/shiver-server-manager.log` | Every action, with time and admin (rotated weekly) |
| `/opt/sharkord/<version>/sharkord` | Sharkord binaries |
| `/var/lib/sharkord/<id>/` (or another location) | Each server's data; `<id>.luks` for encrypted ones |
| `/var/backups/sharkord/` | Backups |
| `/usr/lib/systemd/system/sharkord@.service` | The shared unit; per-server drop-ins in `/etc/systemd/system/sharkord@<id>.service.d/` |
| `/etc/caddy/Caddyfile`, `/etc/caddy/shiver-server-manager/`, `/etc/caddy/sites/<id>.caddy` | Caddy's configuration |
| `/etc/apt/sources.list.d/caddy-stable.list`, `/etc/apt/apt.conf.d/52shiver-server-manager` | Caddy's repository and its automatic updates |
| `/etc/yum.repos.d/_copr:copr.fedorainfracloud.org:group_caddy:caddy.repo` | Caddy's repository on Fedora and RHEL |
| `/etc/systemd/system/caddy.service.d/shiver-server-manager.conf` | openSUSE only: Caddy starts from its Caddyfile |
| `/etc/fstab` | Drives mounted with Storage > Add a drive (backed up first) |
| ufw rules labelled `sharkord:<id>`, or firewalld services `sharkord-<id>` | Firewall |
| `sharkord-<id>` system users | One per server |

## Uninstalling

Removing the package never stops or deletes a server, but it does remove the shared unit (`sharkord@.service`), so servers keep running only until the next reboot. To remove everything, use System > Remove everything first (it deletes every server), then remove the package (`sudo apt remove shiver-server-manager`, `sudo dnf remove shiver-server-manager` or `sudo zypper remove shiver-server-manager`). The vault, records and backups stay until you delete them; reinstalling the package brings the servers back as they were.
