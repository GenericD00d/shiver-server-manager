# Security

## Reporting a problem

**Please do not open a public issue for a security problem.** Use GitHub's private reporting
instead: **Security → Advisories → Report a vulnerability** on
[this repository](https://github.com/GenericD00d/shiver-server-manager/security/advisories/new).
That opens a thread only you and the maintainer can see.

If that is not available to you for any reason, open an ordinary issue saying only that you have
something to report and asking for a way to send it, with no details in the issue itself.

Expect a first reply within a week. This is one person's side project.

## What this project handles

The manager **runs as root**. It installs and runs Sharkord servers, each as its own system user
(`sharkord-<id>`), and changes the system in the places listed in
[What the manager changes on the system](docs/admin-guide.md#what-the-manager-changes-on-the-system).

It holds:

- **the vault**: each server's owner token and data-folder key, encrypted with age. The vault's
  identity is encrypted with the vault passphrase, and shown once at setup as the recovery key;
- **backups**, encrypted to the vault's public key, and **exports**, encrypted to a passphrase;
- **alert settings**, including an SMTP password, in a root-only file outside the vault, because the
  health timer runs without unlocking it;
- optionally, **a GitHub token** for self-update.

**No independent security audit has been done.** Its internal reviews are listed under
[Review history](#review-history).

## Trust boundaries

What the manager does not trust, where it comes in, and what stands in its way. The rules that
enforce each line, and their tests, are in [AGENTS.md](AGENTS.md#rules-that-must-not-break).

| Untrusted | Where it comes in | What guards it |
|---|---|---|
| Anyone with a shell as a `shiver-admins` member, without that member's password | `sudo shiver-server-manager`, which the sudoers rule allows with any arguments and no sudo password | The manager's own PAM login for every command except `version`, `help` and `completion`; hidden `internal-*` commands refuse root; a dropped environment; no start if the manager's files are writable by anyone but root |
| A server's own process, and anything else running as `sharkord-<id>`, such as a plugin | That server's data folder, which the manager reads and writes as root | The unit's hardening (`packaging/sharkord@.service`); root reads and writes that folder only through `os.Root`, which never follows a link out of it, and unpacks into staging folders the server's user can't write |
| Browsers reaching the web page | `127.0.0.1:8443`, or an admin domain through Caddy | PAM login and group check, the login throttle, sessions with idle and absolute limits, a Host allow-list, same-origin plus the app's header on every change, a strict CSP, the password again for destructive actions, and fixed commands per action |
| Downloads: Sharkord, the manager's own packages, the plugins, `plugins.json` | GitHub, over HTTPS | SHA-256 checksums from the same release. These catch a damaged download, not a compromised account; see [Design decisions](#design-decisions-worth-knowing) |
| Archives an admin restores or imports, and plugin archives | Files the admin names | Plain files and folders only, relative paths, size caps, `O_EXCL` |
| A folder imported from another install | `import` | Walked with `Lstat`; links, device files and pipes refused |
| Anyone on the internet | Caddy on 443, and each server's voice ports | Caddy's TLS, and firewall rules for each server's own ports only |

## In scope

- **A way past the login**: anything started through sudo that changes the system, or reads a
  secret, without the PAM login.
- **A server reaching further than its own folder**: a compromised Sharkord or a malicious plugin,
  running as `sharkord-<id>`, getting root to act outside that server's data folder, or reaching
  another server.
- **The web page**: a login bypass, cross-site requests, a command or option injected through an
  action's inputs, or a secret in a job's output.
- **Secrets** reaching a log, a command's arguments, the state file or `--json` output.
- **Downloads** installing something other than what the release published.

## Out of scope

- **Root**, and anyone who can already write the manager's files. It refuses to run then.
- **An admin with their own password.** Members of `shiver-admins` administer the machine by
  design: they can delete servers, restore backups and read owner tokens.
- **Sharkord's own vulnerabilities.** Report those to Sharkord, unless the manager makes one worse.
- **The packages being unsigned.** Known, and planned; see below.

## Design decisions worth knowing

- **The login is the gate, not sudo.** The sudoers rule lets admins run the binary as root with any
  arguments, so every command logs in, and hidden commands refuse root. The bug log in
  [docs/decisions.md](docs/decisions.md#bug-log) has the one time a hidden command didn't.
- **The timers run as root with no sudo user**, and skip the login. Nothing else does.
- **The web page is a root service.** Each action runs the CLI as a child process with fixed
  arguments, as the acting admin, so it reuses the same checks.
- **A failed login doesn't say why.** The web page gives the same message for a wrong password and
  for a right password on an account outside `shiver-admins`, so it can't confirm passwords for the
  machine's other accounts. Only the throttle says something different.
- **Each server is isolated** by its own system user and the unit's hardening: `NoNewPrivileges`,
  `ProtectSystem=strict` with only its data folder writable, `ProtectHome`, `PrivateTmp`,
  `PrivateDevices`, an empty capability set, and restricted address families and namespaces.
- **Root works in a server's data folder only through `os.Root`.** That folder belongs to the
  server's user, so every backup read, restore write and plugin install goes through a root on it,
  which never follows a link out of it. Restores and plugins are unpacked into staging folders that
  stay root's until their contents are in place, and a plugin is swapped in with the server stopped.
- **The manager writes Sharkord's database only to switch plugins on**, and does it as the server's
  own user, through `internal-enable-plugins`.
- **Downloads are checked against checksums published in the same release.** That proves a
  download is intact, not who made it: someone who controlled the GitHub account could publish a
  matching pair. Signing releases with a key that never leaves the owner's hands is planned; see
  [docs/decisions.md](docs/decisions.md#open-items).

## Review history

| Date | Review | Fixed in | What it found |
|---|---|---|---|
| 2026-11-10 | During the build | `8ce5779` | Hidden `internal-*` commands skipped the login and took any paths when run through sudo |
| 2026-11-13 | Review of the four Shiver repos, internal and LLM-assisted | This branch | Root unpacked restores and plugins into a folder the server's own user could change, while the server ran; the web login told a right password from a wrong one for accounts outside `shiver-admins`; downloads are checked only against same-release checksums (not fixed yet: needs a signing key) |
