"""Number platform (water-low threshold) for Gardyn."""
from __future__ import annotations

from homeassistant.components.number import NumberEntity, NumberMode
from homeassistant.const import UnitOfLength
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import GardynConfigEntry
from .entity import GardynEntity


async def async_setup_entry(
    hass: HomeAssistant, entry: GardynConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    data = entry.runtime_data
    async_add_entities([GardynWaterThreshold(data.coordinator, data.client, entry.unique_id)])


class GardynWaterThreshold(GardynEntity, NumberEntity):
    _attr_translation_key = "water_low_threshold"
    _attr_name = "Water Low Threshold"
    _attr_native_min_value = 0
    _attr_native_max_value = 15
    _attr_native_step = 0.5
    _attr_native_unit_of_measurement = UnitOfLength.CENTIMETERS
    _attr_mode = NumberMode.BOX

    def __init__(self, coordinator, client, identifier) -> None:
        super().__init__(coordinator, identifier)
        self._client = client
        self._attr_unique_id = f"{identifier}_water_low_threshold"

    @property
    def native_value(self) -> float:
        return float(self.coordinator.data["water"]["low_threshold_cm"])

    async def async_set_native_value(self, value: float) -> None:
        await self._client.set_water_threshold(value)
        await self.coordinator.async_request_refresh()
