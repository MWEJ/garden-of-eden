from unittest.mock import AsyncMock, patch

from pytest_homeassistant_custom_component.common import MockConfigEntry


async def setup_with_mocks(hass, entry: MockConfigEntry, state_json, schedules_json):
    """Set up the entry with a mocked GardyndClient; returns the client mock."""
    entry.add_to_hass(hass)
    client = AsyncMock()
    client.get_state = AsyncMock(return_value=state_json)
    client.get_schedules = AsyncMock(return_value=schedules_json)
    with patch("custom_components.gardyn.GardyndClient", return_value=client):
        assert await hass.config_entries.async_setup(entry.entry_id)
        await hass.async_block_till_done()
    return client
