# Shiver Server Manager

Sets up, runs, backs up, imports, exports and deletes several [Sharkord](https://github.com/sharkord/sharkord) servers on one machine, each on its own domain, from a terminal menu, with Caddy handling HTTPS. It also installs and updates the [Shiver](https://github.com/GenericD00d/Shiver) plugin.

Not an official Sharkord project.

**Status:** early development. Nothing works yet beyond `shiver-server-manager version`.

Supported: Ubuntu 24.04 and 26.04, Linux Mint 22, on amd64 and arm64.

- `Checklist.md`: what the manager does
- `Implementation guide.md`: how each part is built
- `NOTES.md`: decisions, differences from the guide, and the bug log

## Building

```sh
CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" ./cmd/shiver-server-manager
go test ./...
```

## Licence

MIT
