# Changelog

All notable changes to simpleota are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- `login` with a staff account's personal token failed with "this token
  cannot list projects". It now asks for the Project ID on a terminal, and
  otherwise explains that `--project <project-id>` is needed.

### Changed

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
