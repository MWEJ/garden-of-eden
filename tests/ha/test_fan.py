from .helpers import setup_with_mocks


async def test_pump_percentage_and_set(hass, mock_entry, state_json, schedules_json):
    client = await setup_with_mocks(hass, mock_entry, state_json, schedules_json)
    state = hass.states.get("fan.gardyn_pump")
    assert state.state == "off"  # pump.on is false in fixture

    await hass.services.async_call(
        "fan", "set_percentage", {"entity_id": "fan.gardyn_pump", "percentage": 40}, blocking=True
    )
    client.set_speed.assert_awaited_with(40)

    await hass.services.async_call(
        "fan", "set_percentage", {"entity_id": "fan.gardyn_pump", "percentage": 0}, blocking=True
    )
    client.pump_off.assert_awaited()
