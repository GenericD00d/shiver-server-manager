<img src="../logo.png" alt="Shiver Server Manager" width="72" align="right">

# Troubleshooting

Start with System > Health check (`sudo shiver-server-manager health`) and System > Repair (`repair --dry-run` shows what it would change). Every action is logged in `/var/log/shiver-server-manager.log`.

## Voice doesn't connect, but the website works

Voice goes straight to each server's own port (UDP and TCP, 40000-40999), not through Caddy.

- **The port isn't open.** Firewall > Show rules should list `<port>/udp` and `<port>/tcp` for the server; Firewall > Repair rules adds missing ones. A cloud provider's firewall or security group needs the same ports (Help: Open a cloud firewall), and so does a home router (Help: Forward ports on a home router, or Firewall > Router port mapping).
- **No public IPv4.** Sharkord's voice server only listens on IPv4, so voice fails on an IPv6-only server even when the website works.
- **The public address changed.** Sharkord announces its address at startup. The health check notices a change within 5 minutes and restarts the servers; `setup` does it at once. If you set the address by hand (`setup --public-ip`), set it again or switch back with `--public-ip auto`.
- **Behind carrier-grade NAT.** See below.

## Let's Encrypt doesn't give a certificate

Certificates > Certificate status shows the last error from Caddy.

- **DNS.** The domain's A record must hold this server's public IPv4 address (Help: Point a domain at the server). If it also has an AAAA (IPv6) record, that must point here too or be removed: Let's Encrypt tries IPv6 first. Changes can take minutes to hours.
- **Ports 80 and 443.** Let's Encrypt checks the domain over port 80 or 443 from outside. Open them in every firewall in the way, including your provider's.
- **The weekly limit.** Let's Encrypt issues at most 5 certificates a week for the same domain. If you've been creating and deleting the same server, wait, or use Test mode (staging server, far higher limits) while experimenting.
- **Retry now.** Caddy retries by itself for 30 days, but the gaps grow to hours. Certificates > Retry now has it try at once.

## Carrier-grade NAT

If your router's internet address is in 100.64.0.0/10, or isn't your public address, your internet provider shares one public address between many customers, and nothing outside can reach your server. Setup and Firewall > Router port mapping say so when they find it. Ask your provider for a public IPv4 address (some give one free on request, others charge), or run the manager on a VPS.

## A server shows "locked"

It's encrypted and the machine has restarted. Log in and use Security > Unlock the vault. If you've lost the vault passphrase, Security > Recover the vault with the recovery key.

## A server shows "drive missing"

Its storage location's drive isn't there (or a different drive is). Servers on that drive stay stopped rather than starting empty. Reconnect the drive (it's mounted from `/etc/fstab` with `nofail`), then `sudo mount <mount point>`, unlock the vault if the servers are encrypted, and start them.

## Something stopped partway

A create, delete, move, import or update that was interrupted (lost SSH session, power cut) is recorded. System > Repair finishes it. An interrupted import needs its source again: run the same import command.

## A server won't start

Servers > Show logs. If it happened after a settings change, the old settings were put back automatically. If a container is full (health check warns at 80%), grow it. The unit is `sharkord@<id>`; `systemctl status sharkord@<id>` shows systemd's view.

## "Another run of the manager holds the lock"

Two admins (or an admin and the health timer) can't change things at once. The message names who holds it; wait for them to finish.
