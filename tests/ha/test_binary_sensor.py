import copy

from .helpers import setup_with_mocks


async def test_binary_sensors(hass, mock_entry, state_json, schedules_json):
    s = copy.deepcopy(state_json)
    s["water"]["low"] = True
    s["overtemp"] = False
    await setup_with_mocks(hass, mock_entry, s, schedules_json)
    assert hass.states.get("binary_sensor.gardyn_water_low").state == "on"
    assert hass.states.get("binary_sensor.gardyn_over_temp").state == "off"
