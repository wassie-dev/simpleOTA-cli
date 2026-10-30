# Changelog

All notable changes to simpleota are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

The commands are laid out by API resource, and `login` no longer chooses a
project. Commands that changed name or behaviour are listed under Changed.

### Added

- `project list`, `project show` and `project use`: list projects, show one,
  and choose the current project that resource commands act on. The current
  project is `--project`, then `SIMPLEOTA_PROJECT`, then the one chosen with
  `project use`, then the only project the token can see.
- `build list` and `build show <build-number>`.
- `deployment list`.
- `device list` (filter with `--channel`, `--group` or `--state`) and
  `device show <device-id|uuid>`.
- `channel list` and `channel create`.
- `key list`, and `key revoke`, which needs a personal token and `--yes`.
- `build upload`, which uploads a build without deploying it, and
  `build archive` and `build unarchive`.
- `deployment create`, which deploys an uploaded build to channels,
  groups or every device, and starts it.
- `device events`, which shows a device's recent update checks, and
  `device update`, which renames, moves or (de)activates a device.
- `group list`, `group create` and `group member list|add|remove`.
- `audit`, which shows the project's audit log.
- `status` shows the account a personal token belongs to and where the
  current project came from (`project_source` in JSON).
- Help with examples on every command and group, and grouped commands in
  `simpleota --help`.
- `release` reads the chip family from the image header, so `--chip` is
  optional. A `--chip` that differs from the header is refused unless a
  device in the project reports that chip.
- `release` lists the other active deployments for the same chip on its
  channel (`also_active` in JSON), which go on reaching the devices the new
  build does not.

### Fixed

- `login` with a staff account's personal token failed with "this token
  cannot list projects". The CLI now asks the API's `/me/` endpoint for the
  account and lists its own projects; another account's project is opened
  by ID.
- `deployment wait` exited 0 for a deployment completed before any device
  was offered the build. It now exits 4 with outcome `no_devices`.
- `status --project <ref>` exited 0 without saying the project was not
  found. It now reports it in `project_error` and exits 2.
- `status` showed an empty project name for a staff account.
- `key generate` for a backup key printed a snippet with only the new key.
  The snippet now pins the new key and the project's newest other active
  key (`pinned_key_ids` in JSON).
- `logout` said "Logged out" when nothing was stored; it now says so, and
  `logged_out` is false.
- `update --version` to an older release said "Updated"; it now says
  "Downgraded".

### Changed

- `login` only checks and stores the token, using the API's `/me/` endpoint
  to report the account. It no longer chooses or remembers a project, and
  logging in again clears the project chosen with `project use`. Its JSON
  has `account` and `project_count`, and `project` is null unless the token
  sees exactly one project.
- `deployment status` is split into `deployment show` and
  `deployment wait`, which replaces `deployment status --wait`.
- `keys` is renamed `key`; `keys` still works as an alias.
- `login` explains which tokens it accepts and where to make them in the
  dashboard before asking for one.
- `install.sh`: after adding the install directory to PATH, it also prints
  the command that puts it on PATH in the current terminal, which a script
  cannot change for you.

## [0.1.1] - 2026-10-30

### Changed

- `install.sh` adds the install directory to PATH when it is not there yet,
  with one line in your shell's startup file (`~/.zshrc` for zsh,
  `~/.bash_profile` or `~/.bashrc` for bash, `conf.d/simpleota.fish` for
  fish, else `~/.profile`). Set `SIMPLEOTA_NO_MODIFY_PATH=1` to skip it. It
  used to print a hint that always named `~/.profile`, which zsh does not
  read.

## [0.1.0] - 2026-10-30

### Added

- `login` and `logout`: store a personal or project token from the dashboard
  in the OS keychain, or in a 0600 file when there is none.
- `keys generate`: create an Ed25519 signing key on this machine, keep it
  encrypted with a passphrase, register only the public key, and print the
  `signing_keys` snippet for the 1.0 firmware clients.
- `keys export`: print the private key, encrypted or as PEM, for a CI secret.
- `release`: sign, upload and deploy a firmware binary in one call. The
  manifest carries the binary's SHA-256 for the server to check, the upload
  is never retried, and a binary the project already has is refused unless
  `--allow-duplicate` is passed.
- `deployment status`: show a deployment's device progress; `--wait` gates
  CI on it.
- `deployment ramp`, `pause`, `resume`, `complete` and `cancel`: change a
  deployment's state.
- `status`: show the CLI's version and any newer release, the login and
  whether its token works, the project, and the signing key.
- `update`: replace the binary with the latest or a given release after
  checking its SHA-256.
- `--json` output with a documented schema, and fixed exit codes.
- Static binaries for Linux, macOS and Windows on amd64 and arm64.
- `install.sh` and `install.ps1`: one-line install and update scripts,
  published with each release, that verify the download's checksum.

[Unreleased]: https://github.com/wassie-dev/simpleOTA-cli/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/wassie-dev/simpleOTA-cli/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/wassie-dev/simpleOTA-cli/releases/tag/v0.1.0
