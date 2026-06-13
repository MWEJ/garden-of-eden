"""DataUpdateCoordinator polling the gardynd REST API."""
from __future__ import annotations

import logging
from datetime import timedelta

from homeassistant.core import HomeAssistant
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed

from .api import GardyndApiError, GardyndClient
from .const import DOMAIN

_LOGGER = logging.getLogger(__name__)


class GardynCoordinator(DataUpdateCoordinator[dict]):
    """Polls /state and /schedules, merging them into one snapshot."""

    def __init__(self, hass: HomeAssistant, client: GardyndClient, interval: timedelta) -> None:
        super().__init__(hass, _LOGGER, name=DOMAIN, update_interval=interval)
        self.client = client

    async def _async_update_data(self) -> dict:
        try:
            state = await self.client.get_state()
            schedules = await self.client.get_schedules()
        except GardyndApiError as err:
            raise UpdateFailed(str(err)) from err
        return {**state, "schedules_detail": schedules}
