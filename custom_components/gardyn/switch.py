"""Switch platform (schedule enable) for Gardyn."""
from __future__ import annotations

from typing import Any

from homeassistant.components.switch import SwitchEntity
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import GardynConfigEntry
from .entity import GardynEntity


async def async_setup_entry(
    hass: HomeAssistant, entry: GardynConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    data = entry.runtime_data
    async_add_entities(
        GardynScheduleSwitch(data.coordinator, data.client, entry.unique_id, ch)
        for ch in ("light", "pump")
    )


class GardynScheduleSwitch(GardynEntity, SwitchEntity):
    def __init__(self, coordinator, client, identifier, channel: str) -> None:
        super().__init__(coordinator, identifier)
        self._client = client
        self._channel = channel
        self._attr_translation_key = f"{channel}_schedule"
        self._attr_name = f"{channel.capitalize()} Schedule"
        self._attr_unique_id = f"{identifier}_{channel}_schedule_switch"

    @property
    def is_on(self) -> bool:
        return bool(self.coordinator.data["schedules"][self._channel]["enabled"])

    async def async_turn_on(self, **kwargs: Any) -> None:
        await self._client.set_schedule_enabled(self._channel, True)
        await self.coordinator.async_request_refresh()

    async def async_turn_off(self, **kwargs: Any) -> None:
        await self._client.set_schedule_enabled(self._channel, False)
        await self.coordinator.async_request_refresh()
