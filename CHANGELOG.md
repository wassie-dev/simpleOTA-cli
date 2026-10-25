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
- `release`: sign, upload and deploy a firmware binary in one call, then
  check the SHA-256 the server stored.
- `rollout`: ramp, pause, resume, complete or cancel a deployment.
- `status`: show a deployment's device progress; `--wait` gates CI on it.
- `--json` output with a documented schema, and fixed exit codes.
- Static binaries for Linux, macOS and Windows on amd64 and arm64.
