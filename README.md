# simpleota

The command-line tool for [SimpleOTA](https://simpleota.com): sign ESP32
firmware on your machine, upload it, roll it out and gate CI on the result.

Signing keys are generated and kept on your machine. Only public keys and
signatures are sent to SimpleOTA.

## Install

Download a binary for Linux, macOS or Windows (amd64 or arm64) from the
[releases page](https://github.com/wassie-dev/simpleOTA-cli/releases) and
check it against `checksums.txt`. On macOS, a binary downloaded in a browser
is quarantined until you run `xattr -d com.apple.quarantine simpleota`.
Or build it with Go 1.27+:

```bash
go install github.com/wassie-dev/simpleOTA-cli/cmd/simpleota@latest
```

## Quickstart

```bash
# 1. Paste a personal token from the dashboard (kept in the OS keychain)
simpleota login

# 2. Create a signing key; the private key stays here, encrypted
simpleota keys generate --key-id prod-2026

# 3. Pin the printed signing_keys snippet in your firmware, then release
simpleota release build/app.bin --version-label 1.0.0 --chip esp32s3 --percent 10

# 4. Watch it, then widen it
simpleota status --wait --timeout 30m
simpleota rollout ramp <deployment-id> --percent 100
```

Run `simpleota keys generate` a second time for a backup key, keep that one
offline, and pin both keys in your first signed firmware. Devices pin at most
two keys and cannot change them over the air.

There is no rollback command: SimpleOTA never offers a build at or below the
one a device runs. To go back, release the previous source again; it gets a
new, higher build number.

## Commands

| Command | What it does |
| --- | --- |
| `login` / `logout` | Store or remove a personal (`soto_pat_`) or project (`soto_proj_`) token |
| `keys generate --key-id <id>` | Create a key locally and register its public key (personal token only) |
| `keys export [--format encrypted\|pem]` | Print the private key for a CI secret |
| `release <bin> --version-label <v> --chip <c>` | Sign, upload and deploy in one call, then verify the stored SHA-256. Never retried: each upload uses a build number |
| `rollout ramp\|pause\|resume\|complete\|cancel <id>` | Change a deployment's state |
| `status [id] [--wait]` | Show progress; `--wait` gates CI |

Global flags: `--json` ([schema](docs/json.md)), `--api-url`, `--project`.
`--wait` exits 0 once every device the current step reached has confirmed,
and counts a device once it has checked in and been offered the build.

| Exit code | Meaning |
| --- | --- |
| 0 | OK |
| 1 | Error |
| 2 | Usage error |
| 3 | Not logged in, or the token was rejected |
| 4 | A device failed or rolled back, or the deployment is paused, cancelled or not started |
| 5 | `--wait` timed out |

| Variable | Use |
| --- | --- |
| `SIMPLEOTA_TOKEN` | API token; takes precedence over `login` |
| `SIMPLEOTA_SIGNING_KEY` | Exported key, encrypted or PEM |
| `SIMPLEOTA_SIGNING_KEY_PASSPHRASE` | Passphrase for an encrypted key |
| `SIMPLEOTA_API_URL` | API URL (default `https://simpleota.com`) |
| `SIMPLEOTA_CONFIG_DIR` | Config directory (default: the OS user config dir) |
| `SIMPLEOTA_NO_KEYRING` | Store the token in a 0600 file instead of the keychain |
| `NO_COLOR` | Disable colour |

## CI

Make an `api`-scoped project token in the dashboard, and export the key once
with `simpleota keys export`. Store both as secrets, and put `simpleota` on
the runner's PATH from the releases page.

GitHub Actions:

```yaml
- name: Release firmware
  env:
    SIMPLEOTA_TOKEN: ${{ secrets.SIMPLEOTA_TOKEN }}
    SIMPLEOTA_SIGNING_KEY: ${{ secrets.SIMPLEOTA_SIGNING_KEY }}
    SIMPLEOTA_SIGNING_KEY_PASSPHRASE: ${{ secrets.SIMPLEOTA_SIGNING_KEY_PASSPHRASE }}
  run: |
    ID=$(simpleota release build/app.bin --version-label "${GITHUB_REF_NAME}" --chip esp32s3 --percent 10 --json | jq -r .deployment_id)
    simpleota status "$ID" --wait --timeout 30m
```

GitLab CI:

```yaml
release:
  script:
    - ID=$(simpleota release build/app.bin --version-label "$CI_COMMIT_TAG" --chip esp32s3 --percent 10 --json | jq -r .deployment_id)
    - simpleota status "$ID" --wait --timeout 30m
```

## Development

```bash
make test        # unit tests with -race
make lint        # vet, staticcheck, gosec, govulncheck (make tools installs them)
make devserver   # local SimpleOTA server with a seeded user, project and tokens
make e2e         # end-to-end suite against that server
```

`scripts/devserver.sh` clones [wassie-dev/simpleOTA](https://github.com/wassie-dev/simpleOTA)
into `.devserver/` and runs it on `http://127.0.0.1:8765` with SQLite and
local storage. It needs Python 3.12+. Plain `http` is accepted only for
localhost.

## License

MIT. Report security issues privately as described in [SECURITY.md](SECURITY.md).
