from unittest.mock import AsyncMock

from .helpers import setup_with_mocks


async def test_camera_image(hass, mock_entry, state_json, schedules_json):
    client = await setup_with_mocks(hass, mock_entry, state_json, schedules_json)
    client.camera_url = lambda which: f"http://1.2.3.4:5000/camera/{which}.jpg"

    from homeassistant.components.camera import async_get_image
    # Mock the HTTP fetch the camera performs.
    with __import__("unittest").mock.patch(
        "custom_components.gardyn.camera.GardynCamera._fetch", AsyncMock(return_value=b"\xff\xd8\xff\xd9")
    ):
        img = await async_get_image(hass, "camera.gardyn_upper_camera")
    assert img.content == b"\xff\xd8\xff\xd9"
