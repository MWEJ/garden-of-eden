"""Camera platform for Gardyn."""
from __future__ import annotations

import asyncio

import aiohttp
from homeassistant.components.camera import Camera
from homeassistant.core import HomeAssistant
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import GardynConfigEntry
from .entity import GardynEntity

_CAMERAS = (("upper", "Upper Camera"), ("lower", "Lower Camera"))


async def async_setup_entry(
    hass: HomeAssistant, entry: GardynConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    data = entry.runtime_data
    async_add_entities(
        GardynCamera(hass, data.coordinator, data.client, entry.unique_id, which, name)
        for which, name in _CAMERAS
    )


class GardynCamera(GardynEntity, Camera):
    def __init__(self, hass, coordinator, client, identifier, which, name) -> None:
        GardynEntity.__init__(self, coordinator, identifier)
        Camera.__init__(self)
        self._hass = hass
        self._client = client
        self._which = which
        self._attr_name = name
        self._attr_unique_id = f"{identifier}_{which}_camera"

    async def _fetch(self) -> bytes | None:
        session = async_get_clientsession(self._hass)
        try:
            async with asyncio.timeout(10):
                async with session.get(self._client.camera_url(self._which)) as resp:
                    if resp.status != 200:
                        return None
                    return await resp.read()
        except (aiohttp.ClientError, asyncio.TimeoutError):
            return None

    async def async_camera_image(self, width: int | None = None, height: int | None = None) -> bytes | None:
        return await self._fetch()
