"""Light platform for Gardyn."""
from __future__ import annotations

from typing import Any

from homeassistant.components.light import ATTR_BRIGHTNESS, ColorMode, LightEntity
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import GardynConfigEntry
from .entity import GardynEntity


async def async_setup_entry(
    hass: HomeAssistant, entry: GardynConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    data = entry.runtime_data
    async_add_entities([GardynLight(data.coordinator, data.client, entry.unique_id)])


class GardynLight(GardynEntity, LightEntity):
    """The Gardyn grow light."""

    _attr_translation_key = "light"
    _attr_name = "Light"
    _attr_color_mode = ColorMode.BRIGHTNESS
    _attr_supported_color_modes = {ColorMode.BRIGHTNESS}

    def __init__(self, coordinator, client, identifier) -> None:
        super().__init__(coordinator, identifier)
        self._client = client
        self._attr_unique_id = f"{identifier}_light"

    @property
    def is_on(self) -> bool:
        return bool(self.coordinator.data["light"]["on"])

    @property
    def brightness(self) -> int:
        pct = self.coordinator.data["light"]["brightness"]
        return round(pct * 255 / 100)

    async def async_turn_on(self, **kwargs: Any) -> None:
        if ATTR_BRIGHTNESS in kwargs:
            pct = round(kwargs[ATTR_BRIGHTNESS] * 100 / 255)
            await self._client.set_brightness(pct)
        else:
            await self._client.light_on()
        await self.coordinator.async_request_refresh()

    async def async_turn_off(self, **kwargs: Any) -> None:
        await self._client.light_off()
        await self.coordinator.async_request_refresh()
