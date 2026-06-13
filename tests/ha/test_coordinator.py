from datetime import timedelta
from unittest.mock import AsyncMock

import pytest
from homeassistant.helpers.update_coordinator import UpdateFailed

from custom_components.gardyn.api import GardyndApiError
from custom_components.gardyn.coordinator import GardynCoordinator


async def test_merges_state_and_schedules(hass, state_json, schedules_json):
    client = AsyncMock()
    client.get_state.return_value = state_json
    client.get_schedules.return_value = schedules_json
    coord = GardynCoordinator(hass, client, timedelta(seconds=15))
    data = await coord._async_update_data()
    assert data["light"]["on"] is True
    assert data["schedules_detail"]["light"]["entries"][0]["at"] == "06:00"


async def test_error_becomes_update_failed(hass):
    client = AsyncMock()
    client.get_state.side_effect = GardyndApiError("boom")
    coord = GardynCoordinator(hass, client, timedelta(seconds=15))
    with pytest.raises(UpdateFailed):
        await coord._async_update_data()
