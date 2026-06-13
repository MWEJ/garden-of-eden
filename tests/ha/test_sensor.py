import copy

from .helpers import setup_with_mocks


async def test_telemetry_sensors(hass, mock_entry, state_json, schedules_json):
    await setup_with_mocks(hass, mock_entry, state_json, schedules_json)
    assert hass.states.get("sensor.gardyn_temperature").state == "22.5"
    assert hass.states.get("sensor.gardyn_water_level").state == "7.4"
    assert hass.states.get("sensor.gardyn_pump_power").state == "6.0"


async def test_null_sensor_unavailable(hass, mock_entry, schedules_json, state_json):
    s = copy.deepcopy(state_json)
    s["sensors"]["temperature_c"] = None
    await setup_with_mocks(hass, mock_entry, s, schedules_json)
    assert hass.states.get("sensor.gardyn_temperature").state == "unavailable"


async def test_schedule_sensor_attributes(hass, mock_entry, state_json, schedules_json):
    await setup_with_mocks(hass, mock_entry, state_json, schedules_json)
    state = hass.states.get("sensor.gardyn_light_schedule")
    assert state.state == "1"  # one entry
    assert state.attributes["entries"][0]["at"] == "06:00"
