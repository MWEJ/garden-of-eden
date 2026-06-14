# Home Assistant automations (legacy)

These `lights.yml` / `pump.yml` automations drove the Gardyn schedule from Home
Assistant. gardynd now runs the schedule **on-device** (see
`config.example.yaml` `schedules:` and the `Light Schedule` / `Pump Schedule`
switches in Home Assistant).

Keep these only if you prefer HA-driven scheduling; if so, disable the on-device
schedule by setting `schedules.light.enabled: false` and
`schedules.pump.enabled: false`. (Once the planned dedicated HA integration for
editing the on-device schedule ships — a separate project — you'll also be able
to toggle the `Light Schedule` / `Pump Schedule` switches off in Home Assistant.)
