from .helpers import setup_with_mocks


async def test_water_threshold_number(hass, mock_entry, state_json, schedules_json):
    client = await setup_with_mocks(hass, mock_entry, state_json, schedules_json)
    assert hass.states.get("number.gardyn_water_low_threshold").state == "10.0"

    await hass.services.async_call(
        "number", "set_value",
        {"entity_id": "number.gardyn_water_low_threshold", "value": 8.5}, blocking=True
    )
    client.set_water_threshold.assert_awaited_with(8.5)
