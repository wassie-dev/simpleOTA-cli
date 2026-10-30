# simpleota

The command-line tool for [SimpleOTA](https://simpleota.com): sign ESP32
firmware on your machine, upload it, roll it out and gate CI on the result.

Signing keys are generated and kept on your machine. Only public keys and
signatures are sent to SimpleOTA.

## Install

Linux and macOS:

```bash
curl -fsSL https://github.com/wassie-dev/simpleOTA-cli/releases/latest/download/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://github.com/wassie-dev/simpleOTA-cli/releases/latest/download/install.ps1 | iex
```

The script downloads the release for your OS and CPU, checks it against the
release's `checksums.txt`, and installs it without sudo: on Linux and macOS
where `simpleota` already is, else `/usr/local/bin` if writable, else
`~/.local/bin`; on Windows in `%LOCALAPPDATA%\Programs\simpleota`, which it
adds to your user PATH. Run the same command again, or `simpleota update`,
to update.

| Variable | Use |
| --- | --- |
| `SIMPLEOTA_VERSION` | Install a specific version, e.g. `0.1.0` |
| `SIMPLEOTA_INSTALL_DIR` | Install somewhere else |
| `SIMPLEOTA_NO_MODIFY_PATH=1` | Windows: leave the user PATH alone |

For example, to pin a version:

```bash
curl -fsSL https://github.com/wassie-dev/simpleOTA-cli/releases/latest/download/install.sh | SIMPLEOTA_VERSION=0.1.0 sh
```

You can also download an archive from the
[releases page](https://github.com/wassie-dev/simpleOTA-cli/releases) (on
macOS, a browser download is quarantined until you run
`xattr -d com.apple.quarantine simpleota`), or build with Go 1.27+:

```bash
go install github.com/wassie-dev/simpleOTA-cli/cmd/simpleota@latest
```

To uninstall, run `simpleota logout` to remove the token from the OS
keychain, then delete the `simpleota` binary and the config directory
(`~/.config/simpleota` on Linux, `~/Library/Application Support/simpleota`
on macOS, `%APPDATA%\simpleota` on Windows).

## Quickstart

```bash
# 1. Paste a personal token from the dashboard (kept in the OS keychain)
simpleota login
simpleota status

# 2. Create a signing key; the private key stays here, encrypted
simpleota keys generate --key-id prod-2026

# 3. Pin the printed signing_keys snippet in your firmware, then release
simpleota release build/app.bin --version-label 1.0.0 --chip esp32s3 --percent 10

# 4. Watch it, then widen it
simpleota deployment status --wait --timeout 30m
simpleota deployment ramp <deployment-id> --percent 100
```

Run `simpleota keys generate` a second time for a backup key, keep that one
offline, and pin both keys in your first signed firmware. Devices pin at most
two keys and cannot change them over the air.

There is no rollback command: SimpleOTA never offers a build at or below the
one a device runs. To go back, release the previous source again; it gets a
new, higher build number. If the rebuilt binary is byte-for-byte the earlier
one, `release` stops because the project already has it; pass
`--allow-duplicate` to upload it again.

### Failed uploads

`release` sends the upload once and never retries it, because every accepted
upload uses a new build number and counts against your plan's monthly limit.
The manifest carries the binary's SHA-256, so if the bytes change on the way,
the server rejects the upload and nothing is created.

If the connection drops or a gateway answers 5xx mid-upload, the build may
exist anyway. Run the same `release` again: it checks the project's builds
first and stops with the existing build number if the binary is already
there, so it cannot create a second build.

## Commands

Commands about the CLI itself sit at the top level; commands about a
SimpleOTA resource sit under its name.

| Command | What it does |
| --- | --- |
| `status` | Show the CLI's version and available update, login, project and signing key |
| `login` / `logout` | Store or remove a personal (`soto_pat_`) or project (`soto_proj_`) token |
| `update [--check] [--version <v>]` | Replace this binary with the latest (or a given) release, after checking its SHA-256 |
| `release <bin> --version-label <v> --chip <c>` | Sign, upload and deploy in one call. Stops if the project already has this binary (`--allow-duplicate` uploads it anyway); never retried |
| `deployment status [id] [--wait]` | Show a deployment's device progress; `--wait` gates CI |
| `deployment ramp <id> --percent <n>` | Offer the build to more devices |
| `deployment pause\|resume\|complete\|cancel <id>` | Change a deployment's state |
| `keys generate --key-id <id>` | Create a key locally and register its public key (personal token only) |
| `keys export [--format encrypted\|pem]` | Print the private key for a CI secret |

Global flags: `--json` ([schema](docs/json.md)), `--api-url`, `--project`.
`deployment status --wait` exits 0 once every device the current step reached
has confirmed, and counts a device once it has checked in and been offered
the build.

| Exit code | Meaning |
| --- | --- |
| 0 | OK |
| 1 | Error (for `status`: the API could not be reached) |
| 2 | Usage error |
| 3 | Not logged in, or the token was rejected (`status` too) |
| 4 | A device failed or rolled back, or the deployment is paused, cancelled or not started |
| 5 | `deployment status --wait` timed out |

| Variable | Use |
| --- | --- |
| `SIMPLEOTA_TOKEN` | API token; takes precedence over `login` |
| `SIMPLEOTA_SIGNING_KEY` | Exported key, encrypted or PEM |
| `SIMPLEOTA_SIGNING_KEY_PASSPHRASE` | Passphrase for an encrypted key |
| `SIMPLEOTA_API_URL` | API URL (default `https://simpleota.com`) |
| `SIMPLEOTA_CONFIG_DIR` | Config directory (default: the OS user config dir) |
| `SIMPLEOTA_NO_KEYRING` | Store the token in a 0600 file instead of the keychain |
| `SIMPLEOTA_DOWNLOAD_URL` | Mirror for `update` downloads (https, or http on localhost) |
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
    simpleota deployment status "$ID" --wait --timeout 30m
```

GitLab CI:

```yaml
release:
  script:
    - ID=$(simpleota release build/app.bin --version-label "$CI_COMMIT_TAG" --chip esp32s3 --percent 10 --json | jq -r .deployment_id)
    - simpleota deployment status "$ID" --wait --timeout 30m
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
