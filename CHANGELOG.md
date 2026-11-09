# Changelog

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
