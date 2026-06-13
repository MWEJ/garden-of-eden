from ipaddress import ip_address
from unittest.mock import AsyncMock, patch

from homeassistant.config_entries import SOURCE_USER, SOURCE_ZEROCONF
from homeassistant.data_entry_flow import FlowResultType
from homeassistant.helpers.service_info.zeroconf import ZeroconfServiceInfo

from custom_components.gardyn.const import DOMAIN


def _patch_client(state):
    return patch("custom_components.gardyn.config_flow.GardyndClient", _factory(state))


def _factory(state):
    def make(*args, **kwargs):
        c = AsyncMock()
        c.healthz = AsyncMock(return_value=True)
        c.get_state = AsyncMock(return_value=state)
        return c
    return make


async def test_user_happy_path(hass, state_json):
    with _patch_client(state_json):
        result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": SOURCE_USER})
        result = await hass.config_entries.flow.async_configure(
            result["flow_id"], {"host": "1.2.3.4", "port": 5000}
        )
    assert result["type"] is FlowResultType.CREATE_ENTRY
    assert result["result"].unique_id == "gardyn-xx"
    assert result["data"] == {"host": "1.2.3.4", "port": 5000}


async def test_user_cannot_connect(hass):
    def make(*a, **k):
        c = AsyncMock()
        from custom_components.gardyn.api import GardyndApiError
        c.healthz = AsyncMock(side_effect=GardyndApiError("nope"))
        return c
    with patch("custom_components.gardyn.config_flow.GardyndClient", make):
        result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": SOURCE_USER})
        result = await hass.config_entries.flow.async_configure(
            result["flow_id"], {"host": "9.9.9.9", "port": 5000}
        )
    assert result["type"] is FlowResultType.FORM
    assert result["errors"] == {"base": "cannot_connect"}


async def test_zeroconf_discovery(hass, state_json):
    info = ZeroconfServiceInfo(
        ip_address=ip_address("1.2.3.4"), ip_addresses=[ip_address("1.2.3.4")],
        hostname="gardyn.local.", name="gardynd-gardyn-xx._gardynd._tcp.local.",
        port=5000, type="_gardynd._tcp.local.", properties={},
    )
    with _patch_client(state_json):
        result = await hass.config_entries.flow.async_init(
            DOMAIN, context={"source": SOURCE_ZEROCONF}, data=info
        )
        assert result["type"] is FlowResultType.FORM
        assert result["step_id"] == "zeroconf_confirm"
        result = await hass.config_entries.flow.async_configure(result["flow_id"], {})
    assert result["type"] is FlowResultType.CREATE_ENTRY
    assert result["result"].unique_id == "gardyn-xx"
