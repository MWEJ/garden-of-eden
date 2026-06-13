"""Binary sensor platform for Gardyn."""
from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass

from homeassistant.components.binary_sensor import (
    BinarySensorDeviceClass,
    BinarySensorEntity,
    BinarySensorEntityDescription,
)
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import GardynConfigEntry
from .entity import GardynEntity


@dataclass(frozen=True, kw_only=True)
class GardynBinaryDescription(BinarySensorEntityDescription):
    value_fn: Callable[[dict], bool]


BINARY_SENSORS: tuple[GardynBinaryDescription, ...] = (
    GardynBinaryDescription(
        key="water_low", translation_key="water_low", name="Water Low",
        device_class=BinarySensorDeviceClass.PROBLEM,
        value_fn=lambda d: bool(d["water"]["low"]),
    ),
    GardynBinaryDescription(
        key="over_temp", translation_key="over_temp", name="Over Temp",
        device_class=BinarySensorDeviceClass.PROBLEM,
        value_fn=lambda d: bool(d["overtemp"]),
    ),
)


async def async_setup_entry(
    hass: HomeAssistant, entry: GardynConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    data = entry.runtime_data
    async_add_entities(GardynBinarySensor(data.coordinator, entry.unique_id, d) for d in BINARY_SENSORS)


class GardynBinarySensor(GardynEntity, BinarySensorEntity):
    def __init__(self, coordinator, identifier, description: GardynBinaryDescription) -> None:
        super().__init__(coordinator, identifier)
        self.entity_description = description
        self._attr_unique_id = f"{identifier}_{description.key}"

    @property
    def is_on(self) -> bool:
        return self.entity_description.value_fn(self.coordinator.data)
