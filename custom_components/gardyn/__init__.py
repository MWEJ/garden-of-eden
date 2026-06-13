"""The Gardyn integration."""
from __future__ import annotations

from dataclasses import dataclass
from datetime import timedelta

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import CONF_HOST, CONF_PORT
from homeassistant.core import HomeAssistant
from homeassistant.helpers.aiohttp_client import async_get_clientsession

from .api import GardyndClient
from .const import CONF_SCAN_INTERVAL, DEFAULT_SCAN_INTERVAL, PLATFORMS
from .coordinator import GardynCoordinator


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
