"""Fan platform (pump) for Gardyn."""
from __future__ import annotations

from typing import Any

from homeassistant.components.fan import FanEntity, FanEntityFeature
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import GardynConfigEntry
from .entity import GardynEntity


async def async_setup_entry(
    hass: HomeAssistant, entry: GardynConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    data = entry.runtime_data
    async_add_entities([GardynPump(data.coordinator, data.client, entry.unique_id)])


class GardynPump(GardynEntity, FanEntity):
    """The Gardyn water pump as a variable-speed fan."""

    _attr_translation_key = "pump"
    _attr_name = "Pump"
    _attr_supported_features = (
        FanEntityFeature.SET_SPEED | FanEntityFeature.TURN_ON | FanEntityFeature.TURN_OFF
    )

    def __init__(self, coordinator, client, identifier) -> None:
        super().__init__(coordinator, identifier)
        self._client = client
        self._attr_unique_id = f"{identifier}_pump"

    @property
    def is_on(self) -> bool:
        return bool(self.coordinator.data["pump"]["on"])

    @property
    def percentage(self) -> int:
        return int(self.coordinator.data["pump"]["speed"])

    async def async_set_percentage(self, percentage: int) -> None:
        if percentage == 0:
            await self._client.pump_off()
        else:
            await self._client.set_speed(percentage)
        await self.coordinator.async_request_refresh()

    async def async_turn_on(self, percentage: int | None = None, preset_mode: str | None = None, **kwargs: Any) -> None:
        if percentage is not None:
            await self._client.set_speed(percentage)
        else:
            await self._client.pump_on()
        await self.coordinator.async_request_refresh()

    async def async_turn_off(self, **kwargs: Any) -> None:
        await self._client.pump_off()
        await self.coordinator.async_request_refresh()
