<p align="center"><img src="logo.png" alt="Shiver Server Manager" width="160"></p>

# Shiver Server Manager

Sets up, runs, backs up, imports, exports and deletes several [Sharkord](https://github.com/sharkord/sharkord) servers on one machine, each on its own domain, from a terminal menu, with Caddy handling HTTPS. It also installs and updates the [Shiver](https://github.com/GenericD00d/Shiver) plugin.

Not an official Sharkord project.

Supported: Ubuntu 24.04 and 26.04, Linux Mint 22, on amd64 and arm64.

    sudo apt install ./shiver-server-manager_<version>_<arch>.deb
    sudo shiver-server-manager

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

PAM needs cgo, so build on Linux (Ubuntu 24.04, the oldest supported base) with `libpam0g-dev`:

    CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" ./cmd/shiver-server-manager
    go test ./...
    ARCH=amd64 VERSION=$VERSION nfpm package -f packaging/nfpm.yaml -p deb -t dist/

Everything else builds and tests on any OS; Linux-only parts have stand-ins.

CI runs the unit tests, then installs the .deb on a fresh Ubuntu 24.04 runner (amd64 and arm64) and drives the real binary through every milestone as root: PAM logins, real Sharkord servers, Caddy with Pebble (Let's Encrypt's test server), ufw, LUKS, a second disk, backups and imports, the real Shiver plugin, the health check, two browsers on a voice call, and the web page with a real PAM login. Pushing a `v*` tag publishes the .debs.

## Licence

MIT
