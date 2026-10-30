# The page's API

The daemon serves the page and its API at `/addons/nut/` (lighttpd proxies it to `127.0.0.1:8297`). Every request
carries the system's session; only administrators are admitted (an API token of the scope `addon:nut` or Full access
passes the system's gate as well). Every write (`POST`, `PUT`, `DELETE`) needs the header `X-CCU-Addon-Request`.
Errors are `{"error": "<code>", "message": "…"}`: `invalid` (400 for bad JSON, 422 for a refused value), `not-found`
(404), `connect`/`upsd` (502, the probe).

| Route | What |
| --- | --- |
| `GET api/v1/health` | `{ok, version, uptime_s, ready}`; with `?ready=1` 503 until the first poll |
| `GET api/v1/config` | the configuration without any password: `servers`, `ups`, `settings`, the `defaults` (settings and rules), the token's `right` (`{checked, ok, scopes, error?}`), the `version` |
| `POST api/v1/servers`, `PUT api/v1/servers/{id}`, `DELETE api/v1/servers/{id}` | a server: `{name, host, port, username, password, tls: off\|starttls, tls_verify, ca_cert}`; an empty password on an update keeps the stored one; deleting a server removes its UPS. Answers the configuration. |
| `POST api/v1/servers/probe` | the Test button: a server body (an `id` with an empty password uses the stored one) → `{version, tls, ups: [{name, description}]}` (`VER` and `LIST UPS`), or 502 with `connect` (no connection) or `upsd` (the server's `ERR …`) |
| `POST api/v1/ups`, `PUT api/v1/ups/{id}`, `DELETE api/v1/ups/{id}` | a UPS: `{server, name, label, enabled, rules: {low_battery, fsd, charge_below, runtime_below, on_battery_longer, comm_lost}}` (a threshold of 0 is off). Answers the configuration. |
| `PUT api/v1/settings` | `{enabled, action: poweroff\|reboot, dry_run, grace_s (0-3600), poll_s (1-300), dead_s (5-3600), require_all}`. Answers the configuration. |
| `GET api/v1/status` | `{state, right, now}`: `state` = `{enabled, dry_run, action, require_all, grace_s, ups: [UPSState], countdown?: {since, until, reasons, dry_run, action}, shutdown?: {at, action, dry_run, test, reasons, error?}, issued, polls, last_poll, ready}`; `UPSState` = `{id, label, server, server_name, name, enabled, connected, error?, last_ok?, data_age_s?, status: [flags], on_battery, on_battery_s, charge?, runtime?, load?, input_voltage?, output_voltage?, battery_voltage?, temperature?, model?, mfr?, serial?, vars?, reasons: [{rule, value, threshold}], suppressed, rules}`. Times are unix milliseconds. `?recheck=1` asks the system for the token's scopes again. |
| `GET api/v1/events`, `DELETE api/v1/events` | the last 200 events (`{at, ups?, label?, kind, status?, prev?, charge?, runtime?, reasons?, grace_s?, dry_run?, action?, test?, message?}`; kinds `connected`, `comm_lost`, `comm_back`, `status`, `countdown`, `cancelled`, `shutdown`, `dry_run`, `error`), kept in `var/events.json` across a restart; delete clears them |
| `POST api/v1/poll` | polls every UPS now and answers like `GET api/v1/status` |
| `POST api/v1/shutdown/test` | `{real}`: `false` runs the dry run (an event, nothing else), `true` orders the shutdown now through the system; answers the shutdown record |
| `POST api/v1/shutdown/cancel` | ends a running countdown: `{cancelled}`; the UPS that caused it are ignored until mains returns |

## What the daemon calls on the system

- `GET /api/auth/v1/state` with its token, at start (waiting for the system) and for the page's *Shutdown right*: the
  scopes must hold `system:write`.
- `POST /api/system/v1/services/poweroff.target/start` (or `reboot.target`) with its token when a rule fires: the
  system runs `systemctl start poweroff.target`. An addon token is never granted the `power` scope that `POST /halt`
  and `POST /reboot` need; the service control of `system:write` is the narrowest right that works.
