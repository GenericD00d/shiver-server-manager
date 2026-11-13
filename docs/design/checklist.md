# Sharkord Manager Checklist

Nov 9, 2026 · @Smith

This is everything needed to build a menu-driven tool that sets up, runs and removes Sharkord instances on Ubuntu and Linux Mint, with Caddy providing Let's Encrypt certificates by default and self-signed ones as an option.

How to build each item, for the agent doing the work: Implementation guide

## Decisions to make first

- [ ] Support Ubuntu 24.04 LTS and 26.04 LTS, plus Linux Mint 22.x (built on Ubuntu 24.04)

  Mint 23, built on Ubuntu 26.04, is due around December 2026. Ubuntu 22.04 and Mint 21.x are optional extras.
- [ ] Support both Linux builds Sharkord publishes: `sharkord-linux-x64` and `sharkord-linux-arm64`
- [ ] Pick the language: Go is the easiest fit

  It builds a single file with nothing else to install, has good terminal-menu libraries, and packages easily as a .deb.
- [ ] Fix the file layout
  - `/opt/sharkord/<version>/sharkord`: Sharkord binaries, one folder per version
  - `/etc/sharkord-manager/`: manager settings, plus one env file per instance
  - `/var/lib/sharkord/<id>/`: the default place for instance data folders (an encrypted instance's container is mounted there); admins can pick other drives under "Storage locations"
  - `/var/lib/sharkord-manager/`: the manager's records and its encrypted vault, readable by root only
  - `/etc/caddy/sites/<id>.caddy`: one Caddy site file per instance
- [ ] Fix the port ranges: HTTP on 127.0.0.1 from 5000–5999, WebRTC on UDP and TCP from 40000–40999
- [ ] Give each instance its own system user (`sharkord-<id>`), so one community can't read another's database
- [ ] Set the rules for instance IDs: lowercase letters, digits and hyphens, starting with a letter, at most 20 characters

  The ID goes into every name (folder, service, user, Caddy file, firewall label), and these rules keep it valid in all of them.
- [ ] Keep a list of tested Sharkord versions, and only offer those

  Sharkord is still in alpha (0.0.x), so its settings can be renamed between releases.
- [ ] Release the manager under the MIT licence, the same as Sharkord

  Sharkord and Caddy are downloaded during setup rather than shipped inside the package, so the manager doesn't redistribute either.
- [ ] Ask Sharkord's maintainers how they feel about the name, so the manager isn't mistaken for an official Sharkord tool

## First-time setup

The "First-time setup" menu option checks and installs everything below, in this order.

- [ ] Refuse to run without root, and tell the admin to use `sudo`
- [ ] Read the OS and processor type from `/etc/os-release` and `uname -m`, and stop on anything unsupported

  Mint names its Ubuntu base in `UBUNTU_CODENAME` (`noble` for Mint 22), so check that rather than Mint's own version number.
- [ ] Check that ports 80 and 443 are free (`ss -ltnp`); if Apache or nginx holds them, stop and explain
- [ ] Install Caddy from Caddy's own apt repository, not Ubuntu's

  Ubuntu 24.04 and 26.04 both ship Caddy 2.6.2, but the self-signed settings need Caddy 2.11 or newer. The repository steps are on [Caddy's install page](https://caddyserver.com/docs/install#debian-ubuntu-raspbian).
- [ ] If Caddy already serves other sites, only add the manager's `import` line; never overwrite the admin's Caddyfile
- [ ] Detect the firewall: ufw (usually installed on Ubuntu and Mint, but switched off), firewalld, or none
- [ ] Check that the clock is synced (`timedatectl`), since certificates fail when the time is wrong
- [ ] Find the public IP, and compare it with the server's own addresses to tell a VPS from a home server behind a router
- [ ] Detect carrier-grade NAT on home servers

  If the router reports a WAN address in 100.64.0.0/10, or one that differs from the public IP, nothing outside can reach the server. Say so and suggest a VPS.
- [ ] Check outbound access to GitHub (Sharkord downloads), Let's Encrypt and Caddy's repository
- [ ] Check free disk space for binaries, uploads and backups
- [ ] Create the manager's folders, and the systemd timer that runs its health check
- [ ] Create the `sharkord-admins` group, and add the OS user running setup as the first admin
- [ ] Set the vault passphrase, then show the recovery key once and ask the admin to store it offline
- [ ] Install `cryptsetup`, which encrypted data folders need
- [ ] On desktop installs such as Mint, check that the computer won't go to sleep by itself, and offer to block sleep while instances run; a sleeping computer takes every community offline
- [ ] Check that automatic security updates (`unattended-upgrades`) are on, and add Caddy's repository to them using the origin `apt-cache policy caddy` shows; by default they only cover Ubuntu's own packages

## Access and security

### Logging in

- [ ] Let only members of the `sharkord-admins` group use the manager, with an "Admins" option to add or remove existing OS users
- [ ] Open with a login screen that takes an OS username and password and checks them through PAM

  PAM makes the login follow the server's own accounts, including any two-factor or directory login set up there.
- [ ] Let the group start it without a separate sudo password, via `/etc/sudoers.d/sharkord-manager`: `%sharkord-admins ALL=(root) NOPASSWD: /usr/bin/sharkord-manager`

  The manager needs root for services, the firewall and Caddy, so it always runs as root, and its own login is the password check.
- [ ] Ask for the password again before showing an owner token, deleting an instance or restoring a backup
- [ ] Lock the menu after 10 idle minutes, and pause logins for a few minutes after 5 wrong passwords

### Vault for secrets

- [ ] Keep owner tokens, data-folder keys and the backup key in an encrypted vault, unlocked with a vault passphrase chosen at setup

  It's separate from OS passwords because each admin's password changes on its own schedule. The `filippo.io/age` library can do the encryption in Go.
- [ ] Make the recovery key the vault's own private key, so it opens the vault and every backup if the passphrase is lost
- [ ] Add "Change vault passphrase" to the menu

### Owner tokens

- [ ] Save each new instance's token in the vault automatically when it first starts
- [ ] Add "Save owner token" for imported instances, or any whose token the admin already has

  The manager checks a pasted token before saving it: its SHA-256, in lowercase hex, must match `secret_token` in the `settings` table of the instance's database.
- [ ] Have "Show owner token" ask for the password again, and hide the token once a key is pressed
- [ ] Remove an instance's token from the vault when the instance is deleted

### Data folders

- [ ] Give every data folder to its instance's user with mode 700, so only that instance and root can open it
- [ ] Encrypt data folders by default: each instance's data lives in its own LUKS container (`cryptsetup`), with its key in the vault

  This protects against a stolen disk or a copied disk image, but not against root on the running server. After a reboot, encrypted instances stay stopped until an admin logs in and unlocks the vault.
- [ ] After a reboot, send an alert when encrypted instances are waiting to be unlocked, so an admin knows to log in
- [ ] Offer automatic unlock at boot for servers that must restart unattended, for example by tying the key to the server's TPM chip

  It still protects a disk taken out of the server, but no longer a server that is simply switched on.
- [ ] Give each encrypted instance a unit drop-in with `ConditionPathIsMountPoint=`, so Sharkord never starts while its container is locked

  Otherwise it would set up a fresh, empty community in the bare folder on the unencrypted disk.
- [ ] Warn when a container is 80% full and offer "Grow storage"; a full container stops Sharkord saving messages

## Instances

### Creating one

- [ ] Download the Sharkord build for the server's processor type, plus `release.json` from the same release, and check the file's SHA-256 against the checksum listed there
- [ ] Write one shared systemd template unit, `sharkord@.service`

  It runs as `sharkord-%i`, loads `/etc/sharkord-manager/instances/%i.env`, and starts a per-instance link to the chosen Sharkord version, so an update is a link swap plus a restart.
- [ ] Harden the unit, testing each option: `NoNewPrivileges=yes`, `ProtectSystem=strict` with `ReadWritePaths=` the data folder, `ProtectHome=yes`, `PrivateTmp=yes`

  Leave out `MemoryDenyWriteExecute=yes`, which blocks JIT compilers like Bun's. Don't block running programs from the data folder either: Sharkord unpacks its voice server (mediasoup) there and runs it.
- [ ] Pick free ports from the two ranges, confirm with `ss -lntu` that nothing is listening, and record them
- [ ] Write the instance's env file
  - `SHARKORD_DATA_PATH=/var/lib/sharkord/<id>`
  - `SHARKORD_HOST=127.0.0.1` and `SHARKORD_PORT=<HTTP port>`
  - `SHARKORD_WEBRTC_PORT=<WebRTC port>`
  - `SHARKORD_WEBRTC_ANNOUNCED_ADDRESS=<public IP>`
  - `SHARKORD_AUTOUPDATE=false`
- [ ] Do the first start outside systemd, capture the owner token from its output, stop it, then start the service

  Sharkord prints the token once, and there's no way to get it back. Under systemd it would stay in the system journal.
- [ ] Save the token in the vault (see Access and security)
- [ ] Confirm the instance is up: `http://127.0.0.1:<port>/healthz` answers with `"status":"ok"`
- [ ] Let the admin cap each instance's memory and CPU from the menu, so one busy community can't slow the others down

### Settings

- [ ] Offer Sharkord's environment settings in the menu
  - `SHARKORD_WEBRTC_MAX_BITRATE`, `SHARKORD_MAX_REQUEST_BODY_BYTES`, `SHARKORD_RATE_LIMITERS_ENABLED`
  - `SHARKORD_ALLOWED_ORIGINS`, `SHARKORD_TRUSTED_PROXIES`, `SHARKORD_DEBUG`, `SHARKORD_BACKUP_DATABASE`
  - Single sign-on: `SHARKORD_OIDC_ENABLED`, `_ISSUER`, `_CLIENT_ID`, `_CLIENT_SECRET`, `_REDIRECT_URI`, `_DISABLE_LOCAL_LOGIN`
- [ ] Never edit `config.ini`

  Sharkord rewrites it at every start and resets it to defaults if it's invalid. Environment variables override it and are never written back.
- [ ] Restart the instance after a settings change, since Sharkord reads them only at startup
- [ ] Leave community settings (name, roles, channels, invites, upload limits) to Sharkord's own admin screens; they live in its database

### Running and updating

- [ ] Start, stop, restart and status for one instance or all of them
- [ ] Logs: `journalctl -u sharkord@<id>`, plus Sharkord's own `combined.log` and `error.log` in the instance's `logs` folder
- [ ] Check GitHub for new Sharkord releases, and offer only versions on the tested list
- [ ] Update in three steps: back up, switch the instance's version link, restart
- [ ] If `/healthz` doesn't answer after an update, switch the link back and restore the backup taken just before

  The old version can't open a database that the new one has already upgraded.
- [ ] Tell the admin when a newer tested Sharkord version is out, since instances never update themselves
- [ ] Add "Change domain": set up the new domain and its certificate first, then remove the old site file and certificate; members will need the new address

### Backups

- [ ] Schedule backups yourself: Sharkord only snapshots its database (into the instance's `backups` folder) just before an upgrade changes it
- [ ] Copy the database with SQLite's `VACUUM INTO` while the instance runs, as Sharkord itself does, or archive the whole data folder with the instance stopped

  The database runs in WAL mode, so a plain file copy of a running instance can miss recent writes. Skip `interface`, `drizzle` and `mediasoup`, which Sharkord unpacks again at every start.
- [ ] Encrypt every backup to the vault's public key, so scheduled backups need no passphrase on the server; restoring needs the vault passphrase or the recovery key
- [ ] Offer copies to another machine or to cloud storage (for example with rclone); being encrypted, they're safe to keep off the server
- [ ] Keep a set number of backups per instance, and offer "Restore" in the menu

## Storage locations

- [ ] Add a "Storage" menu that lists each location with its drive, file system and free space, and can add, remove and set the default
- [ ] Let the admin pick a location when creating, importing or restoring an instance; `/var/lib/sharkord` is the default
- [ ] Add "Move data" for existing instances: stop, copy with owners and permissions, check the copied database, switch the path, start, then delete the old copy once the admin confirms
- [ ] Let the admin choose where backups and exports go the same way, so they can sit on a different drive from the data
- [ ] Only accept drives that come back after a reboot, meaning they're listed in `/etc/fstab`

  Desktop auto-mounts under `/media` disappear when nobody is logged in. For a formatted drive that isn't mounted yet, offer to create the mount point and the `/etc/fstab` entry, with `nofail` so a missing drive can't stop the server booting.
- [ ] Accept ext4, XFS and Btrfs for instance data; refuse FAT, exFAT and NTFS, which lack Linux owners and permissions, and network shares, which don't work with the WAL mode Sharkord's database uses
- [ ] Refuse drives mounted with `noexec`, since Sharkord runs its voice server from the data folder
- [ ] Before creating or moving, check there's enough free space, and that the instance's user can get through every parent folder
- [ ] Make each instance's service wait for its drive (`RequiresMountsFor=`), so it never starts on an empty mount point while the drive is missing
- [ ] Show missing drives and low free space in the health check

## Importing and exporting

### Exporting

- [ ] Add "Export instance": archive its data folder with a small manifest (ID, domain, Sharkord version, certificate type, settings) while it keeps running, using the same safe database copy as backups
- [ ] Encrypt each export with a passphrase chosen for that export, so it can move to another server without sharing the vault
- [ ] Leave out `interface`, `drizzle` and `mediasoup`, which Sharkord unpacks again at every start
- [ ] Include the owner token only if the admin asks, and only inside the encrypted file

### Importing

- [ ] Accept three sources: a manager export (which asks for its passphrase), the data folder of a plain Sharkord install (`~/.config/sharkord`), and a Docker volume (the folder mounted at `/home/bun/.config/sharkord`)
- [ ] Read which Sharkord version last ran the data: it's the highest version number among the folders in `interface/`
- [ ] Refuse to run imported data on an older Sharkord version, since an older version can't open a database a newer one has upgraded
- [ ] For a server that's still running, copy it with `rsync` while it runs, then stop it and run `rsync` again, so the downtime is short and the final copy is complete
- [ ] Hand the files to the new instance's user; Docker volumes usually belong to UID 1000, the image's `bun` user
- [ ] Read the imported `config.ini` and offer to carry its settings into the env file; the manager's own ports and paths replace the old ones
- [ ] Ask for the owner token and check it before saving (see Owner tokens)
- [ ] Finish by pointing the domain at the new server; members keep their accounts, and only need a new address if the domain changes

## Shiver plugin

The manager installs and updates the companion plugin of [Shiver](https://github.com/GenericD00d/Shiver) on any instance in one click. Each Shiver release attaches it as `shiver-plugin-<version>.zip`, with checksums in `SHA256SUMS.txt`.

- [ ] Add "Install Shiver" and "Update Shiver" to each instance, plus "Update Shiver everywhere"
- [ ] Take the plugin zip from the latest Shiver release, and check it against that release's `SHA256SUMS.txt`
- [ ] Check its `manifest.json` before installing: the id must be `shiver`, and its plugin SDK version must match the instance's Sharkord (Shiver needs Sharkord 0.0.25 or newer)
- [ ] Back up the instance, then swap the new copy into its `plugins/shiver` folder, owned by the instance's user, keeping the old copy until the new one has loaded
- [ ] On the first install, switch on plugins for the instance and Shiver itself; Sharkord keeps both off by default

  Sharkord stores both switches in its database. The manager sets them with the instance stopped, and only on tested Sharkord versions; otherwise it shows the admin where to switch them on in Sharkord's settings.
- [ ] Restart the instance, since Sharkord loads plugins at startup, then confirm Sharkord logged Shiver as loaded; if it didn't, put the old copy back
- [ ] Show each instance's installed Shiver version next to the latest, and tell the admin when a new one is out
- [ ] Leave removal to Sharkord's own Settings → Plugins, which also deletes the data Shiver keeps for each user

## Certificates

### Let's Encrypt (default)

- [ ] Write the site file with no `tls` block

  Caddy then uses Let's Encrypt automatically. It adds ZeroSSL as a backup only if you set an email address.
- [ ] Before requesting, check that the domain points at this server's public IP; if not, show what to fix instead
- [ ] Show each domain's certificate status and expiry, read from Caddy's certificate folder

  With Caddy from its apt repository that's `/var/lib/caddy/.local/share/caddy/certificates/`, then `acme-v02.api.letsencrypt.org-directory/<domain>/` for Let's Encrypt or `local/<domain>/` for self-signed.
- [ ] "Retry now": recheck DNS, then reload Caddy

  Caddy retries a failed request by itself for up to 30 days, but the gaps between attempts grow to hours.
- [ ] "Test mode", using Let's Encrypt's staging server

  Let's Encrypt issues at most 5 certificates a week for the same domain. Staging certificates aren't trusted, but the limits are far higher.
- [ ] Optional: DNS-challenge support for servers that can't open ports 80 and 443; it needs a Caddy build with your DNS provider's plugin, which the apt package doesn't include

### Self-signed (option)

- [ ] Add the `pki` block and `skip_install_trust` to the manager's global settings file, which the main Caddyfile imports (below)

  Without the `pki` block, `lifetime 365d` gives 7-day certificates. `skip_install_trust` stops Caddy trying to add its root to the server's own trust store.
- [ ] Set `name` and `root_cn` in the `pki` block before the first self-signed instance, so members see your project's name when they install the root
- [ ] Write the site file with `tls { issuer internal { lifetime 365d } }`
- [ ] Offer the root certificate (`pki/authorities/local/root.crt` in Caddy's data folder) as a download, with install steps for Windows, macOS, iOS, Android and Linux
- [ ] Never fall back to self-signed automatically when Let's Encrypt fails

  Caddy would keep the self-signed certificate until it's due for renewal, months later.
- [ ] Warn the admin long before the root expires

  Caddy can't renew its own root yet, and the root lasts about 10 years. With this config Caddy refuses to start once the root has less than 2 years left.

```caddyfile
{
    skip_install_trust
    pki {
        ca local {
            name "Sharkord Manager"
            root_cn "Sharkord Manager Root"
            intermediate_lifetime 730d
            renewal_window_ratio 0.5
        }
    }
}

chat-b.example.com {
    tls {
        issuer internal {
            lifetime 365d
        }
    }
    reverse_proxy 127.0.0.1:5001
}
```

### Switching type

- [ ] Rewrite the site file, reload Caddy, then delete the old type's certificate folder

## Caddy

- [ ] Lay out `/etc/caddy/Caddyfile` with two import lines: the manager's global settings file inside the options block, then the site files

  `import sites/*.caddy` is valid even when the folder is empty; Caddy only logs a warning.
- [ ] Write one site file per instance, named after its ID: `/etc/caddy/sites/<id>.caddy`
- [ ] Validate before every reload, and on failure restore the previous file and show the error

  Run it as Caddy's own user: `sudo -u caddy HOME=/var/lib/caddy caddy validate --config /etc/caddy/Caddyfile`. Validating as root creates a second, unused certificate authority in root's home folder.
- [ ] Apply changes with `systemctl reload caddy`; if the new config fails, Caddy keeps running the old one
- [ ] Don't also change Caddy through its admin API, because the standard service reloads from the Caddyfile and drops API changes
- [ ] After each change, test the site through Caddy: `curl --resolve <domain>:443:127.0.0.1 https://<domain>/healthz` (add `--cacert` with the root for self-signed sites)
- [ ] Require Caddy 2.11 or newer, and leave Caddy's own updates to apt
- [ ] Move Caddy's admin interface from its open local port to a Unix socket only Caddy and root can use; otherwise any user on the server can change Caddy's configuration
- [ ] Show a short maintenance page while an instance is stopped, updating or waiting to be unlocked, instead of a blank error page

No extra setup is needed for Sharkord's WebSocket connection: `reverse_proxy` passes it through. Sharkord already trusts forwarded headers from 127.0.0.1 (its default `trustedProxies`), so its rate limits see real client addresses.

## Firewall and network

### On the server

- [ ] Support ufw and firewalld, and offer to switch ufw on when the server has no firewall

  When ufw or firewalld is active, add rules through it. An allow rule in a separate nftables table can't override another firewall's block, because traffic has to pass every table.
- [ ] Before switching ufw on, allow the SSH port (`sshd -T | grep ^port` shows it), or the admin locks themselves out
- [ ] Open 80 and 443 over TCP for Caddy, and optionally 443 over UDP, which Caddy uses for HTTP/3
- [ ] For each instance, open its WebRTC port on UDP and TCP, labelled with its ID: `ufw allow 40001/udp comment 'sharkord:<id>'`
- [ ] Leave instance HTTP ports closed; they only listen on 127.0.0.1
- [ ] Add "Show rules" and "Repair", which compare the firewall with the manager's records

### Outside the server

- [ ] Cloud firewalls: show the admin exactly which ports to open in the provider's panel
- [ ] Home routers: offer UPnP port mapping (`upnpc`, from the `miniupnpc` package) as an opt-in option, with step-by-step instructions as the fallback

  Many routers ship with UPnP off, and some admins prefer to keep it that way.
- [ ] Domains: check the records and show the A record to create; creating it automatically needs each DNS provider's API
- [ ] Require a public IPv4 address

  Sharkord's voice server only listens on IPv4, so voice fails on an IPv6-only server even if the website works.

### Public IP changes

- [ ] Re-check the public IPv4 address on a timer; when it changes, update every instance's `SHARKORD_WEBRTC_ANNOUNCED_ADDRESS` and restart it

  Sharkord reads the address only at startup, so voice stays broken until the restart.
- [ ] On home connections, update the domain's DNS too (dynamic DNS), or the site and its certificate renewals break as well

## Steps outside the manager

The manager can't do these itself, so it shows the matching how-to wherever a step comes up.

- [ ] Show each how-to below at the point in the menu where it's needed, and list them all under "Help"

**Install the manager:** download the .deb for your processor type (amd64 or arm64), run `sudo apt install ./sharkord-manager_<version>_<arch>.deb`, then start it with `sudo sharkord-manager`.

**Add an admin:** create the OS account if it doesn't exist yet (`sudo adduser <name>`), then add it with the manager's "Admins" option.

**Point a domain at the server:** at your domain's DNS provider (often where you bought it), add an A record for the name you want, such as `chat`, holding the server's public IPv4 address. It usually takes effect within minutes, sometimes hours; the manager's DNS check shows when it has.

**Open a cloud firewall:** in your hosting provider's control panel, find the firewall or security group attached to the server. Allow incoming TCP 80 and 443, UDP 443, and both TCP and UDP on the WebRTC range (40000–40999 by default).

**Forward ports on a home router:** first give the server a fixed local address with the router's DHCP reservation setting. Then open the router's admin page (its address is usually on a label, often 192.168.0.1 or 192.168.1.1), find "Port forwarding" (sometimes "Virtual server" or "NAT"), and forward the same ports to that address.

**Get around carrier-grade NAT:** ask your internet provider for a public IPv4 address (some give one free on request, others charge), or run the manager on a VPS instead.

**Keep a home domain up to date:** turn on your DNS provider's dynamic DNS, or use a dynamic DNS service, and run its updater on the server or in the router, which often has one built in.

**Add a new drive for Sharkord data:** connect it and check its name with `lsblk`. Formatting erases everything on it. On Mint, open the Disks app and format it as Ext4. On a server, run `sudo parted -s /dev/sdX mklabel gpt mkpart sharkord ext4 0% 100%`, then `sudo mkfs.ext4 -L sharkord /dev/sdX1`. Then use the manager's "Add storage location", which mounts it permanently.

**Install the root certificate (members of self-signed instances):** download it from the link the admin shares, then:

- Windows: double-click it, choose "Install Certificate", then "Local Machine", and place it in "Trusted Root Certification Authorities".
- macOS: open it in Keychain Access and add it to the System keychain, then open it there and set "When using this certificate" to "Always Trust".
- iPhone and iPad: open it and install the profile in Settings, then turn on full trust in Settings > General > About > Certificate Trust Settings.
- Android: Settings > Security > Encryption & credentials > Install a certificate > CA certificate (names vary by phone). Browsers then trust it; most other apps ignore added certificates.
- Linux: copy it to `/usr/local/share/ca-certificates/` as a `.crt` file and run `sudo update-ca-certificates`. Chrome-based browsers and the Sharkord desktop app keep their own list, so also run `certutil -d sql:$HOME/.pki/nssdb -A -t C,, -n sharkord -i root.crt` (from `libnss3-tools`).
- Firefox, on any system: Settings > Privacy & Security > Certificates > View Certificates > Authorities > Import, then tick "Trust this CA to identify websites".

**Store the recovery key:** write it down or keep it in a password manager, away from the server. It's the only way back into the vault, and into every backup, if the passphrase is lost.

**Move to a new server:** install the manager there and run first-time setup, then use "Import" with the export file and its passphrase, and point the domain at the new server.

**Get the Shiver apps (members):** download the Android APK or the Windows installer from [Shiver's releases page](https://github.com/GenericD00d/Shiver/releases). The Windows installer isn't code-signed, so SmartScreen warns on first run. Shiver takes over `sharkord://` links from Sharkord's own desktop app, so members should install one or the other.

## Deleting an instance

- [ ] Implement delete as these steps, in this order
  1. Ask for the admin's password and the instance ID to confirm, and offer an encrypted export
  2. Mark the instance as "deleting" in the manager's records
  3. Stop and disable the service: `systemctl disable --now sharkord@<id>`
  4. Remove the Caddy site file, validate, and reload Caddy
  5. Delete the domain's certificate folder
  6. Remove the instance's firewall rules and any UPnP mappings
  7. Free its ports
  8. Delete the data folder; for an encrypted one, unmount it, erase its keys with `cryptsetup erase`, then delete the container file
  9. Delete the env file, the version link, any unit drop-in, and the instance's owner token and keys from the vault
  10. Delete the system user: `userdel sharkord-<id>`
  11. Remove the instance from the manager's records
- [ ] Make every step succeed when its target is already gone, so an interrupted delete can just be run again
- [ ] Let "Repair" finish any instance left marked as "deleting"
- [ ] Store exports and backups outside the data folder (for example in `/var/backups/sharkord/`), and delete them after a set number of days
- [ ] Never delete the shared root certificate, the vault or the global Caddy settings along with an instance

The data folder holds the database, uploads and Sharkord's pre-upgrade database snapshots, so step 8 erases the community for good. For an encrypted folder, erasing the keys also leaves nothing readable on the disk. Step 3 comes first because systemd would otherwise restart a half-deleted instance, and Sharkord would create a fresh, empty one in its place.

## Menu, commands and repair

```
Sharkord Manager                       login: OS username and password
  1) Instances      create, import, export, start/stop, edit settings,
                    logs, update Sharkord, back up/restore, delete,
                    install or update Shiver
  2) Owner tokens   save, show
  3) Certificates   status and expiry, switch Let's Encrypt/self-signed,
                    retry now, test mode, download root certificate
  4) Firewall       show rules, repair, router port mapping
  5) Security       admins, unlock vault, change vault passphrase,
                    encrypt data folders, grow storage
  6) Storage        locations, add a drive, move an instance,
                    backup location
  7) System         first-time setup, health check, repair, help,
                    update manager
```

### Menu and commands

- [ ] Build the commands first, then put the menu on top

  Each menu option calls a command such as `sharkord-manager create --domain chat.example.com --tls letsencrypt`, so admins can script anything the menu does, and each step can be tested on its own.
- [ ] Pick a terminal menu library that works over SSH (for Go: Bubble Tea, or Huh for forms)
- [ ] Make "Create instance" ask only for a name, a domain and a certificate type, with defaults for everything else
- [ ] Show what will change and ask before anything destructive: delete, restore, switching certificate type, firewall changes
- [ ] Take a lock file at the start of every command, so two admins can't change things at once
- [ ] Log every action, with the time and the logged-in OS user, to `/var/log/sharkord-manager.log`
- [ ] Keep the menu in line with Sharkord's own design: its neutral greys with red only for destructive actions, and Sharkord's words for things, such as "server" for each instance and "secret token" for the owner token

### Health check and repair

- [ ] Run a health check from a systemd timer every few minutes
  - Services running, and `/healthz` answering
  - Certificates present and not close to expiry, including the root
  - Public IP unchanged
  - Firewall rules and site files matching the records
  - Enough free space on every storage location, any drive that's missing, and encrypted instances still waiting to be unlocked
- [ ] Show any problems at the top of the menu, and optionally send them by email or webhook
- [ ] Make "Repair" compare what should exist with what does, and fix the difference

  That covers restarting stopped services, restoring missing firewall rules or site files, finishing interrupted creates and deletes, and removing leftovers that belong to no instance.

## Testing

- [ ] Unit-test every generated file (env files, unit files, site files, firewall commands) against known-good examples
- [ ] Test on fresh VMs of each supported system: Ubuntu 24.04 and 26.04 through Multipass or LXD, Mint 22 from its ISO in a desktop VM
- [ ] Install, update and remove Shiver on a fresh instance, and check that its statuses and role colours appear for someone using a browser
- [ ] On Ubuntu 26.04, test the sudoers rule and the install scripts with `sudo-rs` and the Rust coreutils, which 26.04 installs by default
- [ ] Test on arm64 as well, such as an arm64 cloud server or a Raspberry Pi running Ubuntu
- [ ] Point every automated test at a test certificate server: Pebble (Let's Encrypt's local test server) in CI, Let's Encrypt's staging server on VMs with real domains
- [ ] Run two instances side by side, one per certificate type, and check both sites and both voice servers
- [ ] Automate a voice test: two Playwright browsers join one voice channel using Chromium's fake microphone (`--use-fake-device-for-media-stream`, `--use-fake-ui-for-media-stream`), and the test checks that audio arrives
- [ ] Try the Sharkord desktop app against both certificate types; self-signed only works once the root is installed on that computer
- [ ] Export an instance on one server and import it on another; also import from a Docker volume and from a plain install
- [ ] Restore an encrypted backup onto a fresh server using only the recovery key
- [ ] Reboot with encrypted instances, and check they stay stopped until the vault is unlocked, then start
- [ ] Give a VM a second virtual disk: create an instance on it, move another instance onto it, then boot with the disk detached and check that only its instances stay stopped
- [ ] Offer a FAT drive, an NTFS drive and a `noexec` mount as storage locations, and check each is refused
- [ ] Try a wrong password, a user outside `sharkord-admins`, and a wrong owner token
- [ ] Test the failure paths: wrong DNS, blocked ports, a port already taken, a Caddy config that fails validation, a full disk
- [ ] Kill the manager partway through a create and a delete, then check that "Repair" finishes both
- [ ] Test with ufw on, ufw off and firewalld, and confirm SSH stays reachable in all three
- [ ] Test upgrades: Sharkord with a rollback, Caddy through apt, and the manager itself with instances running

## Packaging and docs

### Packaging

- [ ] Build .deb packages for amd64 and arm64 (nfpm or GoReleaser both make them from a Go project)
- [ ] Declare what the manager relies on: `systemd` and PAM required, `ufw` and `cryptsetup` recommended, `miniupnpc`, `sqlite3` and `libnss3-tools` suggested
- [ ] Ship the sudoers rule in the package, and check it with `visudo -cf` while building, since a broken sudoers file stops sudo working at all
- [ ] Have the package's install script create the `sharkord-admins` group, the folders and the health-check timer, then tell the admin to run `sudo sharkord-manager`
- [ ] Include the MIT licence text in the package, as `/usr/share/doc/sharkord-manager/copyright`
- [ ] Publish each release with checksums, and later an apt repository so the manager updates with `apt upgrade`
- [ ] Never touch instances from the package's remove scripts; put "Remove everything" in the menu instead, deleting every instance first

### Docs

- [ ] Admin guide: installing, first-time setup, every menu option, and everything the manager changes on the system
- [ ] Member guide for self-signed instances: installing the root certificate on Windows, macOS, iOS, Android and Linux, including Firefox's separate import
- [ ] Troubleshooting: voice not connecting (ports, IPv4, IP changes), Let's Encrypt failing (DNS, ports 80 and 443, the weekly limit), carrier-grade NAT
- [ ] A table of tested Sharkord, Caddy, Ubuntu and Mint versions, updated with each release
- [ ] A changelog

## Sources

- [Sharkord source code](https://github.com/sharkord/sharkord), v0.0.30: settings, data folder, voice server, release files, health endpoint, database and owner-token hash
- [Caddy source code](https://github.com/caddyserver/caddy), v2.11.9: certificate authority settings and default issuers. The self-signed config above was run on a Caddy 2.11.9 build and gave one-year certificates.
- [CertMagic source code](https://github.com/caddyserver/certmagic), the certificate library Caddy uses: retries and cleanup
- [Ubuntu archive](http://archive.ubuntu.com/ubuntu/dists/): release files for 24.04 and 26.04, and the Caddy versions they ship
- [Let's Encrypt rate limits](https://letsencrypt.org/docs/rate-limits/): couldn't be opened from here, so check the weekly limit there
- Linux Mint 23's December 2026 target comes from news reports such as [Phoronix](https://www.phoronix.com/news/Mint-23-Alfa), not from Mint itself
