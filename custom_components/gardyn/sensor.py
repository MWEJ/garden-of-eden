"""Sensor platform for Gardyn."""
from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass

from homeassistant.components.sensor import (
    SensorDeviceClass,
    SensorEntity,
    SensorEntityDescription,
    SensorStateClass,
)
from homeassistant.const import (
    PERCENTAGE,
    UnitOfElectricCurrent,
    UnitOfElectricPotential,
    UnitOfLength,
    UnitOfPower,
    UnitOfTemperature,
)
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import GardynConfigEntry
from .entity import GardynEntity


@dataclass(frozen=True, kw_only=True)
class GardynSensorDescription(SensorEntityDescription):
    value_fn: Callable[[dict], float | None]


SENSORS: tuple[GardynSensorDescription, ...] = (
    GardynSensorDescription(
        key="temperature", translation_key="temperature", name="Temperature",
        device_class=SensorDeviceClass.TEMPERATURE, native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
        value_fn=lambda d: d["sensors"]["temperature_c"],
    ),
    GardynSensorDescription(
        key="humidity", translation_key="humidity", name="Humidity",
        device_class=SensorDeviceClass.HUMIDITY, native_unit_of_measurement=PERCENTAGE,
        state_class=SensorStateClass.MEASUREMENT,
        value_fn=lambda d: d["sensors"]["humidity_pct"],
    ),
    GardynSensorDescription(
        key="pcb_temp", translation_key="pcb_temp", name="PCB Temperature",
        device_class=SensorDeviceClass.TEMPERATURE, native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
        value_fn=lambda d: d["sensors"]["pcb_temp_c"],
    ),
    GardynSensorDescription(
        key="water_level", translation_key="water_level", name="Water Level",
        device_class=SensorDeviceClass.DISTANCE, native_unit_of_measurement=UnitOfLength.CENTIMETERS,
        state_class=SensorStateClass.MEASUREMENT,
        value_fn=lambda d: d["sensors"]["water_level_cm"],
    ),
    GardynSensorDescription(
        key="pump_voltage", translation_key="pump_voltage", name="Pump Voltage",
        device_class=SensorDeviceClass.VOLTAGE, native_unit_of_measurement=UnitOfElectricPotential.VOLT,
        state_class=SensorStateClass.MEASUREMENT,
        value_fn=lambda d: (d["sensors"].get("pump") or {}).get("bus_voltage"),
    ),
    GardynSensorDescription(
        key="pump_current", translation_key="pump_current", name="Pump Current",
        device_class=SensorDeviceClass.CURRENT, native_unit_of_measurement=UnitOfElectricCurrent.AMPERE,
        state_class=SensorStateClass.MEASUREMENT,
        value_fn=lambda d: (d["sensors"].get("pump") or {}).get("current"),
    ),
    GardynSensorDescription(
        key="pump_power", translation_key="pump_power", name="Pump Power",
        device_class=SensorDeviceClass.POWER, native_unit_of_measurement=UnitOfPower.WATT,
        state_class=SensorStateClass.MEASUREMENT,
        value_fn=lambda d: (d["sensors"].get("pump") or {}).get("power"),
    ),
)


async def async_setup_entry(
    hass: HomeAssistant, entry: GardynConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    data = entry.runtime_data
    entities: list = [GardynSensor(data.coordinator, entry.unique_id, d) for d in SENSORS]
    entities += [
        GardynScheduleSensor(data.coordinator, entry.unique_id, ch) for ch in ("light", "pump")
    ]
    async_add_entities(entities)


class GardynSensor(GardynEntity, SensorEntity):
    def __init__(self, coordinator, identifier, description: GardynSensorDescription) -> None:
        super().__init__(coordinator, identifier)
        self.entity_description = description
        self._attr_unique_id = f"{identifier}_{description.key}"

    @property
    def native_value(self) -> float | None:
        return self.entity_description.value_fn(self.coordinator.data)

    @property
    def available(self) -> bool:
        return super().available and self.entity_description.value_fn(self.coordinator.data) is not None


class GardynScheduleSensor(GardynEntity, SensorEntity):
    """State = entry count; attributes carry the full schedule + enabled flag."""

    def __init__(self, coordinator, identifier, channel: str) -> None:
        super().__init__(coordinator, identifier)
        self._channel = channel
        self._attr_translation_key = f"{channel}_schedule"
        self._attr_name = f"{channel.capitalize()} Schedule"
        self._attr_unique_id = f"{identifier}_{channel}_schedule"

    def _sched(self) -> dict:
        return self.coordinator.data["schedules_detail"].get(self._channel, {})

    @property
    def native_value(self) -> int:
        return len(self._sched().get("entries", []))

    @property
    def extra_state_attributes(self) -> dict:
        sched = self._sched()
        return {"enabled": sched.get("enabled", False), "entries": sched.get("entries", [])}
