import pytest

from custom_components.gardyn.api import GardyndClient, GardyndApiError


async def test_get_state(aioclient_mock, hass):
    aioclient_mock.get("http://1.2.3.4:5000/state", json={"light": {"on": True}})
    client = GardyndClient("1.2.3.4", 5000, hass.helpers.aiohttp_client.async_get_clientsession())
    state = await client.get_state()
    assert state["light"]["on"] is True


async def test_set_brightness_posts(aioclient_mock, hass):
    aioclient_mock.post("http://1.2.3.4:5000/light/brightness", json={"value": 42})
    client = GardyndClient("1.2.3.4", 5000, hass.helpers.aiohttp_client.async_get_clientsession())
    await client.set_brightness(42)
    assert aioclient_mock.mock_calls[-1][2] == {"value": 42}  # json body


async def test_error_raises(aioclient_mock, hass):
    aioclient_mock.get("http://1.2.3.4:5000/state", status=500)
    client = GardyndClient("1.2.3.4", 5000, hass.helpers.aiohttp_client.async_get_clientsession())
    with pytest.raises(GardyndApiError):
        await client.get_state()


def test_camera_url(hass):
    client = GardyndClient("1.2.3.4", 5000, None)
    assert client.camera_url("upper") == "http://1.2.3.4:5000/camera/upper.jpg"
