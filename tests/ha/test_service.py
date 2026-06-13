import pytest
import voluptuous as vol

from .helpers import setup_with_mocks


async def test_set_schedule_calls_api(hass, mock_entry, state_json, schedules_json):
    client = await setup_with_mocks(hass, mock_entry, state_json, schedules_json)
    await hass.services.async_call(
        "gardyn", "set_schedule",
        {
            "config_entry": mock_entry.entry_id,
            "channel": "light",
            "enabled": True,
            "entries": [{"at": "06:00", "action": "on", "brightness": 70}],
        },
        blocking=True,
    )
    client.set_schedule.assert_awaited_with(
        "light", {"enabled": True, "entries": [{"at": "06:00", "action": "on", "brightness": 70}]}
    )


async def test_set_schedule_rejects_bad_entry(hass, mock_entry, state_json, schedules_json):
    await setup_with_mocks(hass, mock_entry, state_json, schedules_json)
    with pytest.raises(vol.Invalid):
        await hass.services.async_call(
            "gardyn", "set_schedule",
            {
                "config_entry": mock_entry.entry_id,
                "channel": "light",
                "entries": [{"at": "6oclock", "action": "on"}],
            },
            blocking=True,
        )
