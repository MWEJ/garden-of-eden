from .helpers import setup_with_mocks


async def test_light_state_and_turn_on(hass, mock_entry, state_json, schedules_json):
    client = await setup_with_mocks(hass, mock_entry, state_json, schedules_json)
    state = hass.states.get("light.gardyn_light")
    assert state.state == "on"
    assert state.attributes["brightness"] == round(70 * 255 / 100)

    await hass.services.async_call(
        "light", "turn_on", {"entity_id": "light.gardyn_light", "brightness": 255}, blocking=True
    )
    client.set_brightness.assert_awaited_with(100)
