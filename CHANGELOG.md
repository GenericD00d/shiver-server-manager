# Changelog

## 0.1.2 (2026-11-10)

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
