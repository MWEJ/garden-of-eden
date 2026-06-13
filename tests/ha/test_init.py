from unittest.mock import AsyncMock, patch

from homeassistant.config_entries import ConfigEntryState


async def test_setup_and_unload(hass, mock_entry, state_json, schedules_json):
    mock_entry.add_to_hass(hass)
    with patch("custom_components.gardyn.GardyndClient") as client_cls, patch(
        "homeassistant.config_entries.ConfigEntries.async_forward_entry_setups",
        AsyncMock(return_value=True),
    ):
        client = client_cls.return_value
        client.get_state = AsyncMock(return_value=state_json)
        client.get_schedules = AsyncMock(return_value=schedules_json)

        assert await hass.config_entries.async_setup(mock_entry.entry_id)
        await hass.async_block_till_done()
        assert mock_entry.state is ConfigEntryState.LOADED
        assert mock_entry.runtime_data.coordinator.data["light"]["on"] is True

    with patch(
        "homeassistant.config_entries.ConfigEntries.async_unload_platforms",
        AsyncMock(return_value=True),
    ):
        assert await hass.config_entries.async_unload(mock_entry.entry_id)
        assert mock_entry.state is ConfigEntryState.NOT_LOADED
