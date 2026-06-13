from .helpers import setup_with_mocks


async def test_schedule_switch_toggle(hass, mock_entry, state_json, schedules_json):
    client = await setup_with_mocks(hass, mock_entry, state_json, schedules_json)
    assert hass.states.get("switch.gardyn_light_schedule").state == "on"

    await hass.services.async_call(
        "switch", "turn_off", {"entity_id": "switch.gardyn_light_schedule"}, blocking=True
    )
    client.set_schedule_enabled.assert_awaited_with("light", False)
