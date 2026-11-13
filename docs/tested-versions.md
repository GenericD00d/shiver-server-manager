# Tested versions

Updated with each release. "CI" means the automated tests ran there on every change; "manual" means it was checked by hand for the release.

| Manager | Sharkord | Plugin SDK | Shiver plugin | Caddy | Systems |
| --- | --- | --- | --- | --- | --- |
| 0.2.0 | 0.0.30 | 2 | 0.3.2 (Shiver 0.2.7) | 2.11.7 (COPR), 2.11.x (apt), 2.11.4 (Tumbleweed), 2.10.0 (Leap 16) | Full tests: Ubuntu 24.04 amd64 and arm64, Debian 13, Fedora 44, AlmaLinux 9 and 10, openSUSE Tumbleweed and Leap 16 (CI; VMs amd64, SELinux enforcing on Fedora, AlmaLinux and Tumbleweed). Package install: Ubuntu 24.04 and 26.04, Debian 12 and 13, Fedora 43 and 44, AlmaLinux and Rocky Linux 9 and 10, openSUSE Leap 16 and Tumbleweed (CI, containers) |
| 0.1.0 | 0.0.30 | 2 | 0.3.2 (Shiver 0.2.7) | 2.11.9 | Ubuntu 24.04 amd64 and arm64 (CI); package install on Ubuntu 26.04 (CI, container) |

Still to be checked by hand before 0.1.0 is called done (see [docs/decisions.md](decisions.md), Open items): Ubuntu 26.04 and Linux Mint 22 on VMs, a real reboot with encrypted servers, the Sharkord desktop app against both certificate types, root certificates on an iPhone and an Android phone, UPnP on a real home router, firewalld, and a voice call between two machines.
