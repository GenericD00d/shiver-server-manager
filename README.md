<p align="center"><img src="logo.png" alt="Shiver Server Manager" width="160"></p>

# Shiver Server Manager

Sets up, runs, backs up, imports, exports and deletes several [Sharkord](https://github.com/sharkord/sharkord) servers on one machine, each on its own domain, from a terminal menu, with Caddy handling HTTPS. It also installs and updates the [Shiver](https://github.com/GenericD00d/Shiver) plugins (Shiver, Temp Share, Media Player), listed in `plugins.json`.

Not an official Sharkord project.

Supported, on amd64 and arm64: Ubuntu 24.04 and 26.04 (and Linux Mint 22), Debian 12 and 13, Fedora 43 and newer, RHEL, AlmaLinux and Rocky Linux 9 and 10, and openSUSE Tumbleweed and Leap 16.

    sudo apt install ./shiver-server-manager_<version>_<arch>.deb       # Ubuntu, Mint, Debian
    sudo dnf install ./shiver-server-manager-<version>-1.<arch>.rpm     # Fedora, RHEL, AlmaLinux, Rocky
    sudo shiver-server-manager

(openSUSE: `sudo zypper install --allow-unsigned-rpm ./shiver-server-manager-<version>-1.<arch>.rpm`.)

There's also a web page for servers without a desktop (`web enable`, then an SSH tunnel to port 8443, or an admin domain through Caddy).

## Docs

- [Admin guide](docs/admin-guide.md): installing, first-time setup, every menu option, and everything the manager changes on the system
- [Member guide](docs/member-guide.md): installing a self-signed server's root certificate
- [Troubleshooting](docs/troubleshooting.md)
- [Tested versions](docs/tested-versions.md)
- [Changelog](CHANGELOG.md)

For whoever works on it:

- `Checklist.md`: what the manager does
- `Implementation guide.md`: how each part is built
- `NOTES.md`: decisions, differences from the guide, the bug log, and manual checks still to do

## Building

The web page's front end (React, with Sharkord's own shadcn/ui components and theme) is in `internal/web/ui`; build it first, since the binary embeds `internal/web/dist`:

    cd internal/web/ui && npm ci && npm run build

To look at it during development with sample data: `SSM_WEB_DEMO=1 go test ./internal/web -run TestDemo -timeout 0`, then http://127.0.0.1:8443 (admin / demo).

PAM needs cgo, so build on Linux. Releases build inside AlmaLinux 8, whose glibc (2.28) is older than every supported system's, so one binary runs on all of them (see `packaging/build.sh`):

    docker run --rm -v "$PWD:/src" -v "$(go env GOROOT):/goroot:ro" -v "$(go env GOMODCACHE):/gomod" almalinux:8 sh /src/packaging/build.sh $VERSION
    ARCH=amd64 VERSION=$VERSION nfpm package -f packaging/nfpm.yaml -p deb -t dist/
    ARCH=amd64 VERSION=$VERSION nfpm package -f packaging/nfpm.yaml -p rpm -t dist/

For development, `CGO_ENABLED=1 go build ./cmd/shiver-server-manager` and `go test ./...` with `libpam0g-dev` (or `pam-devel`) work on any of them.

Everything else builds and tests on any OS; Linux-only parts have stand-ins.

CI runs the unit tests, then installs the package on every supported system in a container (install scripts, sudoers and PAM files, the binary runs), runs the end-to-end tests inside Debian, Fedora, AlmaLinux 9 and 10, and openSUSE Tumbleweed and Leap cloud images under KVM, and installs the .deb on a fresh Ubuntu 24.04 runner (amd64 and arm64) to drive the real binary through every milestone as root: PAM logins, real Sharkord servers, Caddy with Pebble (Let's Encrypt's test server), ufw, LUKS, a second disk, backups and imports, the real Shiver plugin, the health check, two browsers on a voice call, and the web page with a real PAM login. Pushing a `v*` tag publishes the .debs.

## Licence

MIT
