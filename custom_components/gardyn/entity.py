"""Base entity for the Gardyn integration."""
from __future__ import annotations

from homeassistant.helpers.device_info import DeviceInfo
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from .const import DOMAIN
from .coordinator import GardynCoordinator


class GardynEntity(CoordinatorEntity[GardynCoordinator]):
    """Common device_info + availability for all Gardyn entities."""

    _attr_has_entity_name = True

    def __init__(self, coordinator: GardynCoordinator, identifier: str) -> None:
        super().__init__(coordinator)
        self._identifier = identifier
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, identifier)},
            name="Gardyn",
            manufacturer="gardyn-of-eden",
            model="gardyn",
        )

    @property
    def available(self) -> bool:
        return super().available and self.coordinator.data is not None
