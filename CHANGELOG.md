# Changelog

## [0.1.0] - 2026-10-30

The first release, for openccu-lite.

- **The client:** connects to one or more upsd servers (Network UPS Tools, TCP 3493), plain or with STARTTLS (the
  certificate verified against the system roots or a CA of your own, or accepted as it is), with or without a login;
  one connection per server, reconnected when it fails; `LIST VAR` for every UPS at the polling interval.
- **The rules, per UPS:** low battery (LB) while on battery, forced shutdown (FSD, without a grace period), on
  battery and the charge below a percentage, on battery and the runtime below a number of seconds, on battery longer
  than a number of seconds, and communication lost while the UPS was last seen on battery (after the dead time).
- **The shutdown:** a grace period that is cancelled when mains returns in time, a cancel button that ignores the UPS
  until mains returns, a dry run that only logs, the choice between powering off and rebooting, and "every UPS must
  be critical" for a system fed by two UPS. The system is shut down through its own API
  (`systemctl start poweroff.target`); the page shows whether the addon's token holds the right for it.
- **The page:** a card per UPS with the status flags, charge, runtime, load, input and output voltage, battery
  voltage, time on battery, the rules firing and every variable; the servers with a *Test connection* button that
  lists their UPS; the UPS with their names discovered on the server; a test button for the dry run and one for the
  real thing; the event log (status changes, countdowns, shutdowns), kept across a restart. German and English, light
  and dark.
- Outgoing connections only; no listening port beyond the loopback.
