"""The Gardyn integration."""
from __future__ import annotations

import re
from dataclasses import dataclass
from datetime import timedelta

import voluptuous as vol

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import CONF_HOST, CONF_PORT
from homeassistant.core import HomeAssistant, ServiceCall
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import config_validation as cv
from homeassistant.helpers.aiohttp_client import async_get_clientsession

from .api import GardyndClient
from .const import CONF_SCAN_INTERVAL, DEFAULT_SCAN_INTERVAL, DOMAIN, PLATFORMS
from .coordinator import GardynCoordinator


_TIME_RE = re.compile(r"^([01]\d|2[0-3]):[0-5]\d$")


def _valid_entry(entry: dict) -> dict:
    if not _TIME_RE.match(str(entry.get("at", ""))):
        raise vol.Invalid(f"bad time {entry.get('at')!r} (want HH:MM)")
    if entry.get("action") not in ("on", "off"):
        raise vol.Invalid("action must be 'on' or 'off'")
    if "brightness" in entry and not 0 <= int(entry["brightness"]) <= 100:
        raise vol.Invalid("brightness must be 0..100")
    return entry


SET_SCHEDULE_SCHEMA = vol.Schema(
    {
        vol.Required("config_entry"): cv.string,
        vol.Required("channel"): vol.In(["light", "pump"]),
        vol.Optional("enabled", default=True): cv.boolean,
        vol.Required("entries"): [vol.All(dict, _valid_entry)],
    }
)


async def async_setup(hass: HomeAssistant, config) -> bool:
    async def _handle_set_schedule(call: ServiceCall) -> None:
        entry_id = call.data["config_entry"]
        entry: ConfigEntry | None = hass.config_entries.async_get_entry(entry_id)
        if entry is None or entry.domain != DOMAIN:
            raise HomeAssistantError(f"unknown gardyn config entry {entry_id}")
        client = entry.runtime_data.client
        schedule = {"enabled": call.data["enabled"], "entries": call.data["entries"]}
        await client.set_schedule(call.data["channel"], schedule)
        await entry.runtime_data.coordinator.async_request_refresh()

    hass.services.async_register(DOMAIN, "set_schedule", _handle_set_schedule, schema=SET_SCHEDULE_SCHEMA)
    return True


@dataclass
class GardynData:
    """Objects stored on the config entry."""

    client: GardyndClient
    coordinator: GardynCoordinator


type GardynConfigEntry = ConfigEntry[GardynData]


async def async_setup_entry(hass: HomeAssistant, entry: GardynConfigEntry) -> bool:
    session = async_get_clientsession(hass)
    client = GardyndClient(entry.data[CONF_HOST], entry.data[CONF_PORT], session)
    interval = entry.options.get(CONF_SCAN_INTERVAL, DEFAULT_SCAN_INTERVAL)
    coordinator = GardynCoordinator(hass, client, timedelta(seconds=interval))
    await coordinator.async_config_entry_first_refresh()

    entry.runtime_data = GardynData(client=client, coordinator=coordinator)
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    entry.async_on_unload(entry.add_update_listener(_async_reload))
    return True


async def async_unload_entry(hass: HomeAssistant, entry: GardynConfigEntry) -> bool:
    return await hass.config_entries.async_unload_platforms(entry, PLATFORMS)


async def _async_reload(hass: HomeAssistant, entry: GardynConfigEntry) -> None:
    await hass.config_entries.async_reload(entry.entry_id)
