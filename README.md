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
`~/.local/bin`; on Windows in `%LOCALAPPDATA%\Programs\simpleota`. If that
directory is not on your PATH, the script adds it: on Windows to your user
PATH, and on Linux and macOS with one line in your shell's startup file
(`~/.zshrc` for zsh; `~/.bash_profile` on macOS or `~/.bashrc` on Linux for
bash; `conf.d/simpleota.fish` for fish; otherwise `~/.profile`). Open a new terminal afterwards.
Run the same command again, or `simpleota update`, to update.

| Variable | Use |
| --- | --- |
| `SIMPLEOTA_VERSION` | Install a specific version, e.g. `0.1.0` |
| `SIMPLEOTA_INSTALL_DIR` | Install somewhere else |
| `SIMPLEOTA_NO_MODIFY_PATH=1` | Leave your PATH and shell startup files alone |

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
# 1. Paste a personal token, made under API tokens in the dashboard's
#    account menu (kept in the OS keychain)
simpleota login
simpleota status

# 2. Choose the project to work on (skip this if you have only one)
simpleota project list
simpleota project use my-fleet

# 3. Create a signing key; the private key stays here, encrypted
simpleota key generate --key-id prod-2026

# 4. Pin the printed signing_keys snippet in your firmware, then release
simpleota release build/app.bin --version-label 1.0.0 --percent 10

# 5. Watch devices take it, then widen it
simpleota deployment wait --timeout 30m
simpleota deployment ramp <deployment-id> --percent 100
```

`login` stores the token and chooses no project. Resource commands act on
the current project, chosen in this order:

1. `--project <slug|id>`
2. `SIMPLEOTA_PROJECT`
3. the project chosen with `simpleota project use` (personal tokens only)
4. the only project the token can see; a project token sees just its own

A staff account sees its own projects, and opens any other project by ID.

Run `simpleota key generate` a second time for a backup key, keep that one
offline, and pin both keys in your first signed firmware. Devices pin at most
two keys and cannot change them over the air. Each `key generate` prints a
snippet that pins the new key and the project's newest other active key, so
the second run's snippet holds both.

`release` reads the chip family from the image header, which names the chip
the image was built for. It refuses a `--chip` that differs from the header
unless a device in the project reports that chip, since no other device would
be offered the build.

A device installs the highest build offered to it, so several deployments can
be active on one channel: an older one goes on reaching the devices a newer
one does not, such as those outside a 10% rollout. `release` lists the other
active deployments for the same chip on its channel; stop one with
`simpleota deployment complete <id>`.

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

Commands follow the API's resources. Commands about your login and the CLI
sit at the top level; everything in a project sits under the resource's
name, with a plural alias (`simpleota devices list` works too). Every
command has `--help` with examples.

```
simpleota
├── login | logout | status | update [--check] [--version <v>]
├── release <bin> --version-label <v>    sign, upload and deploy in one call
├── project   list | show [slug|id] | use <slug|id>
├── build     list | show <n> | upload <bin> | archive <n> | unarchive <n>
├── deployment
│   ├── list | show [id] | wait [id] [--timeout]
│   ├── create --build <n> [--channel]... [--group]... [--all-devices] [--percent]
│   └── ramp <id> --percent <n> | pause | resume | complete | cancel <id>
├── device    list [--channel] [--group] [--state] | show <device>
│             events <device> | update <device> [--name] [--channel] [--state]
├── channel   list | create <name> [--default]
├── group     list | create <name>
│   └── member  list <group> | add <group> <device>... | remove <group> <device>...
├── key       list | generate --key-id <id> | export | revoke <key-id> --yes
└── audit     [--type <event>] [--limit <n>]
```

| Command | What it does |
| --- | --- |
| `login` / `logout` | Store or remove a personal (`soto_pat_`) or project (`soto_proj_`) token |
| `status` | Show the CLI's version and available update, the account, the current project and where it came from, and the signing key |
| `update` | Replace this binary with the latest (or a given) release, after checking its SHA-256 |
| `release` | Sign, upload and deploy in one call. Stops if the project already has this binary (`--allow-duplicate` uploads it anyway); never retried |
| `project list\|show\|use` | List projects, show one, or choose the current project |
| `build list\|show` | List the project's builds (the API calls them artifacts) or show one by build number |
| `build upload` | Sign and upload a build without deploying it; checked like `release` |
| `build archive\|unarchive` | Take a build out of service, or return it |
| `deployment list\|show` | List deployments, or show one's state and device progress |
| `deployment create` | Deploy an uploaded build to channels (stable by default), groups, or every device, and start it |
| `deployment wait` | Wait for a deployment to succeed or fail; gates CI |
| `deployment ramp\|pause\|resume\|complete\|cancel` | Change a deployment's state |
| `device list\|show` | List devices, filtered by channel, group or state, or show one |
| `device events` | Show a device's recent update checks and why it was or was not offered a build |
| `device update` | Rename a device, move it to a channel, or (de)activate it |
| `channel list\|create` | List release channels, or create one |
| `group list\|create`, `group member list\|add\|remove` | Manage device groups and their devices |
| `key list\|generate\|export` | List the project's public keys, create a key locally and register its public key (personal token only), or print a private key for a CI secret |
| `key revoke` | Revoke a public key on the project; needs a personal token and `--yes` |
| `audit` | Show the project's audit log |

Global flags: `--json` ([schema](docs/json.md)), `--api-url`, `--project`.
`deployment wait` exits 0 once every device the current step reached has
confirmed, and counts a device once it has checked in and been offered the
build. A deployment completed before any device was offered the build exits
4, since no device updated.

| Exit code | Meaning |
| --- | --- |
| 0 | OK |
| 1 | Error (for `status`: the API could not be reached) |
| 2 | Usage error, including no current project when the token sees several (for `status`: the chosen project cannot be found) |
| 3 | Not logged in, or the token was rejected (`status` too) |
| 4 | A device failed or rolled back, the deployment is paused, cancelled or not started, or it was completed before any device was offered the build |
| 5 | `deployment wait` timed out |

| Variable | Use |
| --- | --- |
| `SIMPLEOTA_TOKEN` | API token; takes precedence over `login` |
| `SIMPLEOTA_PROJECT` | Project slug or ID to act on; takes precedence over `project use` |
| `SIMPLEOTA_SIGNING_KEY` | Exported key, encrypted or PEM |
| `SIMPLEOTA_SIGNING_KEY_PASSPHRASE` | Passphrase for an encrypted key |
| `SIMPLEOTA_API_URL` | API URL (default `https://simpleota.com`) |
| `SIMPLEOTA_CONFIG_DIR` | Config directory (default: the OS user config dir) |
| `SIMPLEOTA_NO_KEYRING` | Store the token in a 0600 file instead of the keychain |
| `SIMPLEOTA_DOWNLOAD_URL` | Mirror for `update` downloads (https, or http on localhost) |
| `NO_COLOR` | Disable colour |

## CI

Make an `api`-scoped project token in the dashboard, and export the key once
with `simpleota key export`. Store both as secrets, and put `simpleota` on
the runner's PATH from the releases page.

GitHub Actions:

```yaml
- name: Release firmware
  env:
    SIMPLEOTA_TOKEN: ${{ secrets.SIMPLEOTA_TOKEN }}
    SIMPLEOTA_SIGNING_KEY: ${{ secrets.SIMPLEOTA_SIGNING_KEY }}
    SIMPLEOTA_SIGNING_KEY_PASSPHRASE: ${{ secrets.SIMPLEOTA_SIGNING_KEY_PASSPHRASE }}
  run: |
    ID=$(simpleota release build/app.bin --version-label "${GITHUB_REF_NAME}" --percent 10 --json | jq -r .deployment_id)
    simpleota deployment wait "$ID" --timeout 30m
```

GitLab CI:

```yaml
release:
  script:
    - ID=$(simpleota release build/app.bin --version-label "$CI_COMMIT_TAG" --percent 10 --json | jq -r .deployment_id)
    - simpleota deployment wait "$ID" --timeout 30m
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
