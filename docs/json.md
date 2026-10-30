# JSON output

With `--json`, each command prints one JSON value to stdout: an array for
`list` commands, an object otherwise. Notices and warnings go to stderr. Fields are added over time; existing fields keep
their names and types. Times are RFC 3339 in UTC.

## Errors

A failed command prints this object to stdout and exits with the code it
contains. `deployment wait` prints its status object first, then the
error. `status` prints only its status object and sets the exit code.

```json
{ "error": "not logged in to https://simpleota.com; run simpleota login, or set SIMPLEOTA_TOKEN", "exit_code": 3 }
```

## status

```json
{
  "version": "0.1.0",
  "latest_version": "0.2.0",
  "update_available": true,
  "api_url": "https://simpleota.com",
  "config_dir": "/home/me/.config/simpleota",
  "auth": {
    "state": "valid",
    "token_type": "personal",
    "source": "the OS keychain",
    "account": { "id": 7, "email": "me@example.com", "username": "me", "is_staff": false }
  },
  "project": { "id": "…", "slug": "fleet", "name": "Fleet", … },
  "project_source": "project use",
  "signing_key": { "source": "file", "path": "/home/me/.config/simpleota/keys/prod-2026.key" }
}
```

`latest_version` is empty and `update_error` says why when the check for a
newer release fails. `auth.state` is `valid`, `not_logged_in`, `rejected` or
`unreachable`, and `auth.error` gives the reason when there is one. `auth.source` is
`SIMPLEOTA_TOKEN`, `the OS keychain` or the path of the token file.
`auth.account` is present for a personal token on a server that reports
it. `project` is the current project, as in `project show`, or null when
none is selected. `project_source` says where it came from: `--project`,
`SIMPLEOTA_PROJECT`, `project use`, `the project token` or
`the only project`. `project_error` is set, `project` is null and the exit
code is 2 when `--project`, `SIMPLEOTA_PROJECT` or the project chosen with
`project use` cannot be found. `signing_key.source` is `file`,
`env` (SIMPLEOTA_SIGNING_KEY) or `none`; `signing_key.error` is set when the
file is missing or readable by other users.

## update

```json
{ "current": "0.1.0", "latest": "0.2.0", "update_available": true, "updated": true, "path": "/usr/local/bin/simpleota" }
```

With `--check`, `updated` is false and `path` is omitted. `latest` is the
version installed or checked: the newest release, or `--version`.

## login

```json
{
  "api_url": "https://simpleota.com",
  "token_type": "personal",
  "credential_store": "the OS keychain",
  "account": { "id": 7, "email": "me@example.com", "username": "me", "is_staff": false },
  "project": { "id": "…", "slug": "fleet", "name": "Fleet", … },
  "project_count": 1
}
```

`token_type` is `personal` or `project`. `credential_store` is `the OS
keychain` or the path of the token file. `account` is null for a project
token, and on a server that does not report it. `project` is the project
commands now act on: a project token's own project, or a personal token's
only project; otherwise null. `project_count` is how many projects the
token can see, or -1 when the server would not list them.

## project list

```json
[ { "id": "…", "slug": "fleet", "name": "Fleet", "default_framework": "esp_idf", "build_counter": 42, "created_at": "…", "current": true } ]
```

`current` is present, and true, on the current project.

## project show

```json
{ "id": "…", "slug": "fleet", "name": "Fleet", "description": "…", "default_framework": "esp_idf", "build_counter": 42, "created_at": "…" }
```

`build_counter` is the last build number the project allocated.

## project use

```json
{ "api_url": "https://simpleota.com", "project": { "id": "…", "slug": "fleet", "name": "Fleet", … } }
```

## logout

```json
{ "api_url": "https://simpleota.com", "logged_out": true }
```

`logged_out` is false when there was no stored token or project to remove.

## key generate

```json
{
  "key_id": "prod-2026",
  "project_id": "…",
  "public_key_pem": "-----BEGIN PUBLIC KEY-----\n…\n-----END PUBLIC KEY-----\n",
  "private_key_file": "/home/me/.config/simpleota/keys/prod-2026.key",
  "default_signing_key": true,
  "firmware_snippet": "static const simpleota_signing_key_t kSigningKeys[] = {…",
  "pinned_key_ids": ["prod-2026"]
}
```

`pinned_key_ids` names the keys `firmware_snippet` pins: the new key, then
the project's newest other active key.

## key export

```json
{ "format": "encrypted", "private_key": "-----BEGIN AGE ENCRYPTED FILE-----\n…" }
```

`format` is `encrypted` or `pem`.

## key list

```json
[ { "key_id": "prod-2026", "algorithm": "ed25519", "public_key_pem": "-----BEGIN PUBLIC KEY-----\n…", "revoked_at": null, "created_at": "…" } ]
```

Newest first, revoked keys included.

## release

```json
{
  "build_number": 42,
  "artifact_id": "…",
  "deployment_id": "…",
  "state": "active",
  "channel": "stable",
  "security_mode": "signed",
  "key_id": "prod-2026",
  "chip": "esp32s3",
  "sha256": "…",
  "size": 1048576,
  "also_active": [ { … } ]
}
```

`security_mode` is `basic` or `signed`. `key_id` is omitted for `basic`.
`chip` is the chip family in the manifest. `also_active` lists the other
active deployments for the same chip on the channel as
[deployment objects](#deployment-object), highest build first; it is always
an array.
`sha256` is the SHA-256 of the local file. It was sent in the manifest, and
the server accepts an upload only when the bytes it received hash the same,
so it is also the checksum the server stored.

## build list, build show, build archive, build unarchive

`build list` prints an array of build objects, newest first; `build show`,
`build archive` and `build unarchive` print one.

```json
{
  "build_number": 42,
  "id": "…",
  "version_label": "1.4.0",
  "framework": "esp_idf",
  "chip": "esp32s3",
  "board_id": "",
  "partition_profile": "",
  "nvs_schema_version": null,
  "security_mode": "signed",
  "sha256": "…",
  "size": 1048576,
  "archived": false,
  "created_at": "…",
  "warnings": []
}
```

`id` is the API's artifact ID. `warnings` is always an array.

## build upload

```json
{ "build_number": 42, "artifact_id": "…", "security_mode": "signed", "key_id": "prod-2026", "chip": "esp32s3", "sha256": "…", "size": 1048576 }
```

`key_id` is omitted for `basic`.

## device list, device show, device update

`device list` and `group member list` print an array of device objects;
`device show` and `device update` print one.

```json
{
  "id": "…",
  "device_id": "lamp-kitchen",
  "name": "Kitchen lamp",
  "framework": "esp_idf",
  "chip": "esp32s3",
  "board_id": "",
  "hardware_revision": "",
  "partition_profile": "",
  "nvs_schema_version": null,
  "security_mode": "signed",
  "current_build_number": 41,
  "current_version_label": "1.3.0",
  "last_ota_status": "confirmed",
  "active": true,
  "check_state": "…",
  "last_check_status": "…",
  "last_check_at": "…",
  "last_seen": "…",
  "channel_id": "…",
  "channel": "stable",
  "group_ids": []
}
```

`id` is the device's SimpleOTA UUID and `device_id` the ID its firmware
reports. `channel` is the channel's name. `current_build_number`,
`nvs_schema_version`, `last_check_at`, `last_seen` and `channel_id` are null
until the device reports them.

## device events

```json
[
  {
    "at": "…",
    "decision": "no_update",
    "status": "not_in_rollout",
    "deployment_id": null,
    "offered_build_number": null,
    "reported_build_number": 41,
    "reported_version": "1.3.0",
    "message": "…"
  }
]
```

Newest first. `decision` is `offered` or `no_update`. `status` is `offered`,
`up_to_date`, `no_compatible_build`, `device_inactive`, `over_limit`,
`not_in_rollout` or `no_active_deployment`.

## channel list, channel create

`channel list` prints an array of channel objects; `channel create` prints
one.

```json
{ "id": "…", "name": "stable", "description": "", "is_default": true }
```

## group list, group create

`group list` prints an array of group objects; `group create` prints one.

```json
{ "id": "…", "name": "kitchen", "description": "Kitchen lights" }
```

## group member add, group member remove

```json
{ "group": "kitchen", "devices": ["lamp-1", "lamp-2"], "added": true }
```

`group` and `devices` are as given on the command line. `added` is false
for `remove`.

## key revoke

```json
{ "key_id": "prod-2025", "project_id": "…" }
```

## audit

```json
[ { "id": "…", "at": "…", "event_type": "artifact.uploaded", "actor_id": 7, "target_type": "artifact", "target_id": "…", "payload": { "build_number": 42 } } ]
```

Newest first. `actor_id` is null for an event the server made itself.

## Deployment object

The `deployment` commands embed this object.

```json
{
  "id": "…",
  "project_id": "…",
  "build_number": 42,
  "state": "active",
  "rollout_strategy": "immediate",
  "rollout_percentage": 25,
  "created_at": "2026-10-01T12:00:00Z",
  "started_at": "2026-10-01T12:00:01Z",
  "completed_at": null
}
```

`state` is `draft`, `active`, `paused`, `completed` or `cancelled`.

## deployment ramp, pause, resume, complete, cancel

```json
{ "action": "pause", "deployment": { … }, "next_step": "To ship a fix or go back, …" }
```

`next_step` is present when the deployment is paused or cancelled.

## deployment list

An array of [deployment objects](#deployment-object), newest first.

## deployment create

```json
{ "deployment": { … }, "targets": ["channel stable"], "also_active": [ { … } ] }
```

`targets` names the channels and groups the deployment reaches, such as
`channel beta` or `group kitchen`; it is empty for `--all-devices`.
`also_active` is as in [release](#release).

## deployment show, deployment wait

```json
{
  "deployment": { … },
  "devices": { "offered": 3, "in_progress": 1, "confirmed": 1, "failed": 0, "rolled_back": 1 },
  "failures": [ { "device_id": "…", "status": "rolled_back", "reason": "task_wdt" } ],
  "outcome": "device_failed"
}
```

`devices.offered` counts devices that have been offered the build or have
reported against the deployment; the other counts split it. `device_id` is
the device's SimpleOTA UUID. `failures` is always an array.

`outcome` is one of:

| Outcome | Meaning | `wait` exit code |
| --- | --- | --- |
| `succeeded` | Every offered device confirmed, or the deployment is completed after at least one device was offered the build | 0 |
| `in_progress` | Still rolling out (only from `show`) | |
| `device_failed` | A device reported `failed` or `rolled_back` | 4 |
| `stopped` | The deployment is paused, cancelled or draft | 4 |
| `no_devices` | The deployment was completed before any device was offered the build | 4 |
| `timed_out` | `--timeout` passed first | 5 |
