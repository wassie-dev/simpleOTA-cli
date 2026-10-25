# JSON output

With `--json`, each command prints one JSON object to stdout. Notices and
warnings go to stderr. Fields are added over time; existing fields keep
their names and types. Times are RFC 3339 in UTC.

## Errors

A failed command prints this object to stdout and exits with the code it
contains. `status --wait` prints its status object first, then the error.

```json
{ "error": "not logged in to https://simpleota.com; run simpleota login, or set SIMPLEOTA_TOKEN", "exit_code": 3 }
```

## login

```json
{
  "api_url": "https://simpleota.com",
  "token_type": "project",
  "credential_store": "the OS keychain",
  "project": { "id": "…", "slug": "fleet", "name": "Fleet" }
}
```

`token_type` is `personal` or `project`. `credential_store` is `the OS
keychain` or the path of the token file.

## logout

```json
{ "api_url": "https://simpleota.com", "logged_out": true }
```

## keys generate

```json
{
  "key_id": "prod-2026",
  "project_id": "…",
  "public_key_pem": "-----BEGIN PUBLIC KEY-----\n…\n-----END PUBLIC KEY-----\n",
  "private_key_file": "/home/me/.config/simpleota/keys/prod-2026.key",
  "default_signing_key": true,
  "firmware_snippet": "static const simpleota_signing_key_t kSigningKeys[] = {…"
}
```

## keys export

```json
{ "format": "encrypted", "private_key": "-----BEGIN AGE ENCRYPTED FILE-----\n…" }
```

`format` is `encrypted` or `pem`.

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
  "sha256": "…",
  "sha256_verified": true,
  "size": 1048576
}
```

`security_mode` is `basic` or `signed`. `key_id` is omitted for `basic`.
`sha256_verified` is true when the checksum the server stored matches the
local file. It is false when the server's copy could not be read back; the
release still went out, and a warning is printed to stderr. A mismatch is an
error, and the new deployment is cancelled.

## Deployment object

`rollout` and `status` embed this object.

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

## rollout

```json
{ "action": "pause", "deployment": { … }, "next_step": "To ship a fix or go back, …" }
```

`next_step` is present when the deployment is paused or cancelled.

## status

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

| Outcome | Meaning | `--wait` exit code |
| --- | --- | --- |
| `succeeded` | Every offered device confirmed, or the deployment is completed | 0 |
| `in_progress` | Still rolling out (only without `--wait`) | |
| `device_failed` | A device reported `failed` or `rolled_back` | 4 |
| `stopped` | The deployment is paused, cancelled or draft | 4 |
| `timed_out` | `--timeout` passed first | 5 |
