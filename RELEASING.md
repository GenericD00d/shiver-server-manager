# Releasing

Releases are built by CI from a tag. Pushing `v<version>` runs `.github/workflows/release.yml`,
which builds the packages on one runner per architecture and publishes them as a GitHub release.
Installed managers find it through self-update.

## Versions

- **The tag is the version.** Nothing in the tree carries it. `release.yml` passes it to
  `packaging/build.sh`, which builds it in with `-X main.version`, and to nfpm, which names the
  packages as `selfupdate.PackageName` expects.
- **Before tagging**, turn `## Unreleased` in CHANGELOG.md into `## <version> (<YYYY-MM-DD>)`.
- **Versions compare** as `selfupdate.ValidVersion` and `selfupdate.Newer` read them: `v1.2.3`, with
  an optional pre-release part.

## Keys

**There is no release signing key yet.** The `.deb` and `.rpm` packages aren't signed, which is why
openSUSE needs `--allow-unsigned-rpm`, and self-update checks a package only against the
`SHA256SUMS` published in the same release. That proves the download is intact, not who made it.

Signing is planned (see [docs/decisions.md](docs/decisions.md#open-items)):

- a minisign key pair, made and kept on the owner's machine;
- its public key compiled into the manager;
- each release's `SHA256SUMS` signed into `SHA256SUMS.minisig`, by a job that only a protected
  `release` environment with a required reviewer can run.

Until then, the only secret involved is the optional **self-update token**: a GitHub token, stored
by the manager, for reading a private repository's releases.

## Building

`release.yml` runs these for each architecture (amd64 and arm64), each on its own runner because
PAM needs cgo:

```bash
cd internal/web/ui && npm ci && npm run build && cd -   # the binary embeds internal/web/dist
go test ./...
sudo visudo -cf packaging/sudoers
docker run --rm -v "$PWD:/src" -v "$(go env GOROOT):/goroot:ro" -v "$(go env GOMODCACHE):/gomod" \
  almalinux:8 sh /src/packaging/build.sh "$VERSION"
ARCH=amd64 VERSION=$VERSION nfpm package -f packaging/nfpm.yaml -p deb -t dist/
ARCH=amd64 VERSION=$VERSION nfpm package -f packaging/nfpm.yaml -p rpm -t dist/
```

Building inside AlmaLinux 8, whose glibc is 2.28, makes one binary that runs on every supported
system. The same commands work by hand.

## Signing and publishing

The publish job collects both architectures' packages, writes `SHA256SUMS` over them, and creates
the GitHub release with `gh release create`, its notes taken from CHANGELOG.md. Nothing is signed
yet (see [Keys](#keys)).

## Verifying a release

Before announcing it, download the release's files and check them:

```bash
sha256sum -c SHA256SUMS
dpkg-deb --field shiver-server-manager_<version>_amd64.deb Version   # the tag, without the v
rpm -qp --queryformat '%{VERSION}\n' shiver-server-manager-<version>-1.x86_64.rpm
```

Then install one on a fresh VM, run `sudo shiver-server-manager version`, and try self-update from
the previous release. `test/install-check.sh` does the install, check and remove part.

## If a key is lost or leaked

- **The GitHub account or a token that can push tags.** Anyone holding it can publish a release
  that installed managers will offer, and it will pass the checksum. Revoke the token, delete the
  release, and say so publicly. This is the gap signing closes.
- **The self-update token.** Revoke it on GitHub, and save a new one in the manager.
- **Once there is a signing key**, losing it means no installed manager can accept a release
  signed by a new one, so it needs two backups, kept apart, as Shiver's RELEASING.md describes.
