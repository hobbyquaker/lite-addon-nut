# lite-addon-nut

*[Deutsch](README.md)*

A **client for Network UPS Tools (NUT)** for [openccu-lite](https://github.com/hobbyquaker/openccu-lite): it connects
to the `upsd` that has your UPS – a NAS, a Proxmox host, a Raspberry Pi at the UPS's USB cable –, shows the UPS's
state and **shuts the system down cleanly** before the UPS runs out of energy. That protects the SD card, the file
system and the device files of the radio interfaces.

![The status with a running countdown](docs/screenshots/02-countdown.png)

## What it does

- **Connects to one or more upsd servers** (TCP 3493), plain or with **STARTTLS** (the certificate verified against
  the system roots or a CA of your own, or accepted as it is), with or without a login (`upsd.users`). One connection
  per server, reconnected when it fails; one `LIST VAR` per UPS at the polling interval (default 5 s). **Outgoing
  connections only; no open port.**
- **Shows per UPS** the status (on line, on battery, low battery, charging, discharging, bypass, overload, …), charge,
  runtime, load, input and output voltage, battery voltage, temperature, how long it has been on battery, and on
  request every variable the server delivers.
- **Rules per UPS** that shut the system down:

  | Rule | When |
  | --- | --- |
  | Low battery (LB) | the UPS is on battery and says `LB` – on by default |
  | Forced shutdown (FSD) | the NUT master set `FSD` – on by default, **no grace period** |
  | Charge below X % | on battery and `battery.charge` below the threshold |
  | Runtime below Y s | on battery and `battery.runtime` below the threshold |
  | On battery longer than Z s | whatever the charge and runtime say |
  | Communication lost | the server stops answering (or the UPS is `DATA-STALE`) and the UPS was last seen on battery – after the dead time (default 60 s); `upsmon`'s rule – on by default |

- **Grace period** (default 30 s): a countdown runs between the rule and the shutdown, shown on the page. When mains
  returns in time it is **cancelled**. A button cancels it by hand too; the UPS that caused it is then ignored until it
  is back on mains.
- **Action:** power off (the default; the UPS cuts the load later, and when mains returns the system boots with the
  power) or reboot.
- **Dry run:** logs and shows what would happen, never shuts down. A **Test** button for the dry run and one that
  tests the whole path for real.
- **Several UPS:** any one may trigger, or – for a system fed by two UPS – only when every one is critical.
- **Event log:** status changes, countdowns, cancels, shutdowns and dry runs, the last 200, kept across a restart –
  after a shutdown this says why.

## Installation

1. Install the package `nut-<arch>-<version>.tar.gz` from the [releases page](https://github.com/hobbyquaker/lite-addon-nut/releases)
   through the system's Addons page. The addon is not in the catalogue yet.
2. On the addon's page (*UPS* in the addon menu, or *Settings* on the Addons page) enter the upsd under **Settings →
   Servers** (host, port, a user and password when needed, STARTTLS) and press **Test connection**: the answer names
   the server's version and its UPS.
3. Add a UPS under **UPS**: **Discover** fetches the names from the server (`LIST UPS`); then set the rules.
4. Under **Shutdown** choose the action, the grace period and the dry run. To check it, switch the dry run on first,
   pull the UPS's mains plug and watch: the status card shows `OB`, the countdown runs, and *Events* says what the
   addon would have done. Then switch the dry run off.

The upsd must listen on an address this system reaches (`LISTEN 0.0.0.0 3493` in `upsd.conf`, or the server's
address) and its firewall must let the port through. On a Synology NAS that is *Enable network UPS server* with this
system's IP in the list of permitted devices (the UPS is called `ups` there, the user `monuser` with the password
`secret`). Reading needs no login on most servers; a wrong password is not noticed on a read, because upsd checks it
only at an action.

## How it shuts down

The addon shuts the system down through the system's own API, not with `shutdown` or `systemctl` from its own
process: `POST /api/system/v1/services/poweroff.target/start` (or `reboot.target`), which the system runs as
`systemctl start poweroff.target`. For that the manifest declares the scope **`system:write`** – the only one the addon
has. The scope `power`, which the proper routes `POST /halt` and `POST /reboot` need, is never granted to an addon
token; the service control of `system:write` is the narrowest path that works today. The status page shows under
*Shutdown right* whether the token holds it; the system's Addons page shows the scope as well.

The addon runs as its own user without privileges, opens no port and connects only to the servers you enter. The
configuration with the passwords is in `/usr/local/etc/config/addons/nut/nut.json` (mode 0600), the event log in
`/usr/local/addons/nut/var/events.json`. An uninstall leaves the configuration for a reinstall.

## Screenshots

| | |
| --- | --- |
| ![Status](docs/screenshots/01-status.png) | ![Status, dark](docs/screenshots/01-status-dunkel.png) |
| ![Settings](docs/screenshots/03-einstellungen.png) | ![A UPS and its rules](docs/screenshots/04-usv-dunkel.png) |
| ![Events](docs/screenshots/05-ereignisse.png) | ![Countdown, dark](docs/screenshots/02-countdown-dunkel.png) |

## Development

```sh
go test -race ./...                         # the Go tests, with an upsd double
(cd ui && npm ci && npm run check && npm test && npm run build)
(cd ui && npx playwright test)              # the page against the system double (scripts/dev-serve.sh)
NUT_DOCKER=1 go test -count=1 -v ./test/upsd/   # against a real upsd (Debian nut-server, dummy-ups) in docker
VERSION=0.1.0 scripts/build.sh              # the packages for aarch64, x86_64 and armv7l
```

`scripts/dev-serve.sh` runs the daemon against a small demo system with two UPS: the page at
`http://127.0.0.1:18297/addons/nut/`; `curl 'http://127.0.0.1:18080/dev/ups?name=eaton&status=OB+LB&charge=10'`
switches the UPS. The page's API is in [docs/api.md](docs/api.md).

## Licence

MIT. See [LICENSE](LICENSE).
