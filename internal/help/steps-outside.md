# Steps outside the manager

The manager can't do these itself. Each one is shown where it comes up, and all of them are here.

## Install the manager

Download the package for your system and processor from the releases page, then install it and start the manager:

- Ubuntu, Linux Mint, Debian: the `.deb` (`amd64` or `arm64`)

      sudo apt install ./shiver-server-manager_<version>_<arch>.deb

- Fedora, RHEL, AlmaLinux, Rocky Linux: the `.rpm` (`x86_64` or `aarch64`)

      sudo dnf install ./shiver-server-manager-<version>-1.<arch>.rpm

- openSUSE: the same `.rpm`

      sudo zypper install --allow-unsigned-rpm ./shiver-server-manager-<version>-1.<arch>.rpm

Then:

    sudo shiver-server-manager

## Add an admin

Create the OS account if it doesn't exist yet (`sudo adduser <name>` on Ubuntu and Debian, `sudo useradd -m <name> && sudo passwd <name>` elsewhere), then add it with Security > Admins (or `shiver-server-manager admins add <name>`). Admins start the manager with `sudo shiver-server-manager`.

## Point a domain at the server

At your domain's DNS provider (often where you bought it), add an A record for the name you want, such as `chat`, holding the server's public IPv4 address. It usually takes effect within minutes, sometimes hours; the manager's DNS check shows when it has. If the name also has an AAAA (IPv6) record, point it at this server too or remove it: Let's Encrypt tries IPv6 first.

## Open a cloud firewall

In your hosting provider's control panel, find the firewall or security group attached to the server. Allow incoming TCP 80 and 443, UDP 443, and both TCP and UDP on the voice port range (40000-40999 by default).

## Forward ports on a home router

First give the server a fixed local address with the router's DHCP reservation setting. Then open the router's admin page (its address is usually on a label, often 192.168.0.1 or 192.168.1.1), find "Port forwarding" (sometimes "Virtual server" or "NAT"), and forward the same ports to that address. Forward each server's voice port on its own (40000, 40001, ...; `instance info <id>` shows it) rather than the whole range: some routers accept a large range and then silently drop it, while 80 and 443 keep working. If your router supports UPnP, Firewall > Router port mapping can do this for you.

## Get around carrier-grade NAT

Ask your internet provider for a public IPv4 address (some give one free on request, others charge), or run the manager on a VPS instead.

## Keep a home domain up to date

Turn on your DNS provider's dynamic DNS, or use a dynamic DNS service, and run its updater on the server or in the router, which often has one built in. The manager restarts servers with the new address itself, but it can't change your DNS.

## Add a new drive for Sharkord data

Connect it and check its name with `lsblk`. Formatting erases everything on it.

- On Mint, open the Disks app and format it as Ext4.
- On a server: `sudo parted -s /dev/sdX mklabel gpt mkpart sharkord ext4 0% 100%`, then `sudo mkfs.ext4 -L sharkord /dev/sdX1`.

Then use Storage > Add a drive, which mounts it permanently.

## Install the root certificate (members of self-signed servers)

Download it from the link the admin shares (http://<domain>/sharkord-root.crt), and compare its fingerprint with the one the admin sent you another way. Then:

- **Windows:** double-click it, choose "Install Certificate", then "Local Machine", and place it in "Trusted Root Certification Authorities".
- **macOS:** open it in Keychain Access and add it to the System keychain, then open it there and set "When using this certificate" to "Always Trust".
- **iPhone and iPad:** open it and install the profile in Settings, then turn on full trust in Settings > General > About > Certificate Trust Settings.
- **Android:** Settings > Security > Encryption & credentials > Install a certificate > CA certificate (names vary by phone). Browsers then trust it; most other apps ignore added certificates.
- **Linux:** copy it to `/usr/local/share/ca-certificates/` as a `.crt` file and run `sudo update-ca-certificates`. Chrome-based browsers and the Sharkord desktop app keep their own list, so also run `certutil -d sql:$HOME/.pki/nssdb -A -t C,, -n sharkord -i root.crt` (from `libnss3-tools`).
- **Firefox, on any system:** Settings > Privacy & Security > Certificates > View Certificates > Authorities > Import, then tick "Trust this CA to identify websites".

## Store the recovery key

Write it down or keep it in a password manager, away from the server. It's the only way back into the vault, and into every backup, if the passphrase is lost.

## Keep backups somewhere else too

Backups are encrypted, so they're safe to copy off the server, for example with rclone: `rclone sync /var/backups/sharkord remote:sharkord-backups`. Restoring one needs the vault passphrase or the recovery key.

## Move to a new server

Install the manager there and run first-time setup, then use Import with the export file and its passphrase, and point the domain at the new server.

## Get the Shiver apps (members)

Download the Android APK or the Windows installer from Shiver's releases page: https://github.com/GenericD00d/Shiver/releases. The Windows installer isn't code-signed, so SmartScreen warns on first run. Shiver takes over `sharkord://` links from Sharkord's own desktop app, so members should install one or the other.

## Update the manager

Use System > Updates (or `sudo shiver-server-manager self-update`). By hand, install the new release's package the same way as the first one.

Servers keep running while the manager updates.
