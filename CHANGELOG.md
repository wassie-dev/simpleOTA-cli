# Changelog

All notable changes to simpleota are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
