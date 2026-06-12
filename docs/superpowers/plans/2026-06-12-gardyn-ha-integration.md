# gardyn Home Assistant Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `gardyn` Home Assistant custom integration — a local-polling REST front end to a gardynd Pi that exposes the light, pump, sensors, cameras, and schedule controls as native HA entities plus a `set_schedule` service.

**Architecture:** An async `aiohttp` API client wraps gardynd's REST contract; a `DataUpdateCoordinator` polls `GET /state` + `GET /schedules` and is the single source of truth; entity platforms (light, fan, sensor, binary_sensor, switch, number, camera) read the coordinator and issue control calls; a config flow (manual + zeroconf) creates one entry per Gardyn.

**Tech Stack:** Python 3.12, Home Assistant (2024.12+ APIs: `entry.runtime_data`, typed `ConfigEntry`), `aiohttp`, `pytest-homeassistant-custom-component`.

**Spec:** `docs/superpowers/specs/2026-06-12-gardyn-ha-integration-design.md`
**Sequencing:** Implement on branch `ha-integration` after gardynd Plan 3 lands (the integration is tested against `gardynd --hw=mock`). All paths below are relative to the repo root.

---

## File Structure

```
custom_components/gardyn/__init__.py        setup/unload entry, runtime_data, service
custom_components/gardyn/manifest.json      domain, zeroconf, config_flow
custom_components/gardyn/const.py           DOMAIN, defaults, PLATFORMS
custom_components/gardyn/api.py             GardyndClient (async REST)
custom_components/gardyn/coordinator.py     GardynCoordinator
custom_components/gardyn/entity.py          GardynEntity base
custom_components/gardyn/config_flow.py     user + zeroconf + options flow
custom_components/gardyn/light.py           light
custom_components/gardyn/fan.py             pump
custom_components/gardyn/sensor.py          env/pcb/water/power + schedule sensors
custom_components/gardyn/binary_sensor.py   water_low, overtemp
custom_components/gardyn/switch.py          schedule-enable switches
custom_components/gardyn/number.py          water-low threshold
custom_components/gardyn/camera.py          upper/lower cameras
custom_components/gardyn/services.yaml      set_schedule schema
custom_components/gardyn/strings.json       + translations/en.json
hacs.json                                   HACS metadata
tests/ha/conftest.py                        fixtures (hass, mock entry, /state fixture)
tests/ha/fixtures/state.json                sample GET /state body
tests/ha/test_*.py                          per-module tests
requirements_test.txt                       pytest-homeassistant-custom-component
```

The integration is plain Python under `custom_components/` and does **not**
interact with the Go build; its tests run under pytest, separate from `go test`.

---

### Task 1: Scaffold — manifest, const, hacs, test harness

**Depends on:** none

**Files:**
- Create: `custom_components/gardyn/manifest.json`
- Create: `custom_components/gardyn/const.py`
- Create: `custom_components/gardyn/__init__.py` (minimal, expanded in Task 4)
- Create: `hacs.json`
- Create: `requirements_test.txt`
- Create: `tests/ha/conftest.py`
- Create: `tests/ha/fixtures/state.json`

- [ ] **Step 1: Write the manifest**

`custom_components/gardyn/manifest.json`:
```json
{
  "domain": "gardyn",
  "name": "Gardyn",
  "version": "0.1.0",
  "documentation": "https://github.com/iot-root/garden-of-eden",
  "issue_tracker": "https://github.com/iot-root/garden-of-eden/issues",
  "codeowners": [],
  "iot_class": "local_polling",
  "integration_type": "device",
  "config_flow": true,
  "requirements": [],
  "zeroconf": ["_gardynd._tcp.local."]
}
```

- [ ] **Step 2: Write const.py**

`custom_components/gardyn/const.py`:
```python
"""Constants for the Gardyn integration."""
from __future__ import annotations

from homeassistant.const import Platform

DOMAIN = "gardyn"

DEFAULT_PORT = 5000
DEFAULT_SCAN_INTERVAL = 15  # seconds
MIN_SCAN_INTERVAL = 5

CONF_SCAN_INTERVAL = "scan_interval"

ZEROCONF_TYPE = "_gardynd._tcp.local."

PLATFORMS: list[Platform] = [
    Platform.LIGHT,
    Platform.FAN,
    Platform.SENSOR,
    Platform.BINARY_SENSOR,
    Platform.SWITCH,
    Platform.NUMBER,
    Platform.CAMERA,
]
```

- [ ] **Step 3: Write the minimal `__init__.py` (expanded in Task 4)**

`custom_components/gardyn/__init__.py`:
```python
"""The Gardyn integration."""
from __future__ import annotations
```

- [ ] **Step 4: Write HACS + test harness files**

`hacs.json`:
```json
{
  "name": "Gardyn",
  "content_in_root": false,
  "render_readme": true,
  "homeassistant": "2024.12.0"
}
```

`requirements_test.txt`:
```
pytest-homeassistant-custom-component
```

`tests/ha/conftest.py`:
```python
"""Shared fixtures for Gardyn integration tests."""
from __future__ import annotations

import json
from pathlib import Path

import pytest
from pytest_homeassistant_custom_component.common import MockConfigEntry

from custom_components.gardyn.const import DOMAIN

pytest_plugins = ["pytest_homeassistant_custom_component"]


@pytest.fixture(autouse=True)
def auto_enable_custom_integrations(enable_custom_integrations):
    """Enable loading custom_components in tests."""
    yield


@pytest.fixture
def state_json() -> dict:
    path = Path(__file__).parent / "fixtures" / "state.json"
    return json.loads(path.read_text())


@pytest.fixture
def schedules_json() -> dict:
    return {
        "light": {"enabled": True, "entries": [{"at": "06:00", "action": "on", "brightness": 70}]},
        "pump": {"enabled": True, "entries": [{"at": "09:30", "action": "on"}]},
    }


@pytest.fixture
def mock_entry() -> MockConfigEntry:
    return MockConfigEntry(
        domain=DOMAIN,
        title="Gardyn",
        unique_id="gardyn-xx",
        data={"host": "1.2.3.4", "port": 5000},
    )
```

`tests/ha/fixtures/state.json`:
```json
{
  "available": true,
  "uptime_s": 100,
  "light": {"on": true, "brightness": 70},
  "pump": {"on": false, "speed": 100},
  "sensors": {
    "temperature_c": 22.5, "humidity_pct": 55.0,
    "pcb_temp_c": 31.2, "water_level_cm": 7.4,
    "pump": {"bus_voltage": 12.0, "current": 0.5, "power": 6.0}
  },
  "water": {"low_threshold_cm": 10.0, "low": false},
  "overtemp": false,
  "schedules": {"light": {"enabled": true}, "pump": {"enabled": true}}
}
```

- [ ] **Step 5: Verify the harness collects**

Run: `pip install -r requirements_test.txt && python -m pytest tests/ha/ -q`
Expected: 0 tests collected, no import/collection errors.

---

### Task 2: API client

**Depends on:** Task 1

**Files:**
- Create: `custom_components/gardyn/api.py`
- Test: `tests/ha/test_api.py`

- [ ] **Step 1: Write the failing test**

`tests/ha/test_api.py`:
```python
import pytest
from aiohttp import ClientSession

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python -m pytest tests/ha/test_api.py -q`
Expected: ImportError — `GardyndClient` not found.

- [ ] **Step 3: Implement the client**

`custom_components/gardyn/api.py`:
```python
"""Async REST client for the gardynd service."""
from __future__ import annotations

import asyncio
from typing import Any

import aiohttp

REQUEST_TIMEOUT = 10


class GardyndApiError(Exception):
    """Raised on any gardynd API failure."""


class GardyndClient:
    """Thin async wrapper over the gardynd REST contract."""

    def __init__(self, host: str, port: int, session: aiohttp.ClientSession | None) -> None:
        self._base = f"http://{host}:{port}"
        self._session = session

    async def _request(self, method: str, path: str, json: Any | None = None) -> Any:
        assert self._session is not None
        url = f"{self._base}{path}"
        try:
            async with asyncio.timeout(REQUEST_TIMEOUT):
                async with self._session.request(method, url, json=json) as resp:
                    if resp.status >= 400:
                        raise GardyndApiError(f"{method} {path} -> {resp.status}")
                    if resp.content_type == "application/json":
                        return await resp.json()
                    return await resp.read()
        except (aiohttp.ClientError, asyncio.TimeoutError) as err:
            raise GardyndApiError(f"{method} {path}: {err}") from err

    async def get_state(self) -> dict:
        return await self._request("GET", "/state")

    async def get_schedules(self) -> dict:
        return await self._request("GET", "/schedules")

    async def healthz(self) -> bool:
        await self._request("GET", "/healthz")
        return True

    async def light_on(self) -> None:
        await self._request("POST", "/light/on")

    async def light_off(self) -> None:
        await self._request("POST", "/light/off")

    async def set_brightness(self, pct: int) -> None:
        await self._request("POST", "/light/brightness", json={"value": pct})

    async def pump_on(self) -> None:
        await self._request("POST", "/pump/on")

    async def pump_off(self) -> None:
        await self._request("POST", "/pump/off")

    async def set_speed(self, pct: int) -> None:
        await self._request("POST", "/pump/speed", json={"value": pct})

    async def set_schedule(self, channel: str, schedule: dict) -> None:
        await self._request("PUT", f"/schedules/{channel}", json=schedule)

    async def set_schedule_enabled(self, channel: str, enabled: bool) -> None:
        await self._request("POST", f"/schedule/{channel}/enabled", json={"enabled": enabled})

    async def set_water_threshold(self, cm: float) -> None:
        await self._request("POST", "/water/low-threshold", json={"cm": cm})

    def camera_url(self, which: str) -> str:
        return f"{self._base}/camera/{which}.jpg"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python -m pytest tests/ha/test_api.py -q`
Expected: PASS.

---

### Task 3: Coordinator

**Depends on:** Task 2

**Files:**
- Create: `custom_components/gardyn/coordinator.py`
- Test: `tests/ha/test_coordinator.py`

- [ ] **Step 1: Write the failing test**

`tests/ha/test_coordinator.py`:
```python
from datetime import timedelta
from unittest.mock import AsyncMock

import pytest
from homeassistant.helpers.update_coordinator import UpdateFailed

from custom_components.gardyn.api import GardyndApiError
from custom_components.gardyn.coordinator import GardynCoordinator


async def test_merges_state_and_schedules(hass, state_json, schedules_json):
    client = AsyncMock()
    client.get_state.return_value = state_json
    client.get_schedules.return_value = schedules_json
    coord = GardynCoordinator(hass, client, timedelta(seconds=15))
    data = await coord._async_update_data()
    assert data["light"]["on"] is True
    assert data["schedules_detail"]["light"]["entries"][0]["at"] == "06:00"


async def test_error_becomes_update_failed(hass):
    client = AsyncMock()
    client.get_state.side_effect = GardyndApiError("boom")
    coord = GardynCoordinator(hass, client, timedelta(seconds=15))
    with pytest.raises(UpdateFailed):
        await coord._async_update_data()
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python -m pytest tests/ha/test_coordinator.py -q`
Expected: ImportError — `GardynCoordinator` not found.

- [ ] **Step 3: Implement the coordinator**

`custom_components/gardyn/coordinator.py`:
```python
"""DataUpdateCoordinator polling the gardynd REST API."""
from __future__ import annotations

import logging
from datetime import timedelta

from homeassistant.core import HomeAssistant
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed

from .api import GardyndApiError, GardyndClient
from .const import DOMAIN

_LOGGER = logging.getLogger(__name__)


class GardynCoordinator(DataUpdateCoordinator[dict]):
    """Polls /state and /schedules, merging them into one snapshot."""

    def __init__(self, hass: HomeAssistant, client: GardyndClient, interval: timedelta) -> None:
        super().__init__(hass, _LOGGER, name=DOMAIN, update_interval=interval)
        self.client = client

    async def _async_update_data(self) -> dict:
        try:
            state = await self.client.get_state()
            schedules = await self.client.get_schedules()
        except GardyndApiError as err:
            raise UpdateFailed(str(err)) from err
        return {**state, "schedules_detail": schedules}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python -m pytest tests/ha/test_coordinator.py -q`
Expected: PASS.

---

### Task 4: Entity base + entry setup/unload

**Depends on:** Task 3

**Files:**
- Create: `custom_components/gardyn/entity.py`
- Modify: `custom_components/gardyn/__init__.py`
- Create: `tests/ha/helpers.py` (shared platform-test setup helper)
- Test: `tests/ha/test_init.py`

- [ ] **Step 1: Write the failing test**

`tests/ha/test_init.py`:
```python
from unittest.mock import AsyncMock, patch

from homeassistant.config_entries import ConfigEntryState

from custom_components.gardyn.const import DOMAIN


async def test_setup_and_unload(hass, mock_entry, state_json, schedules_json):
    mock_entry.add_to_hass(hass)
    with patch("custom_components.gardyn.GardyndClient") as client_cls, patch(
        "homeassistant.config_entries.ConfigEntries.async_forward_entry_setups",
        AsyncMock(return_value=True),
    ):
        client = client_cls.return_value
        client.get_state = AsyncMock(return_value=state_json)
        client.get_schedules = AsyncMock(return_value=schedules_json)

        assert await hass.config_entries.async_setup(mock_entry.entry_id)
        await hass.async_block_till_done()
        assert mock_entry.state is ConfigEntryState.LOADED
        assert mock_entry.runtime_data.coordinator.data["light"]["on"] is True

    with patch(
        "homeassistant.config_entries.ConfigEntries.async_unload_platforms",
        AsyncMock(return_value=True),
    ):
        assert await hass.config_entries.async_unload(mock_entry.entry_id)
        assert mock_entry.state is ConfigEntryState.NOT_LOADED
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python -m pytest tests/ha/test_init.py -q`
Expected: FAIL — `async_setup_entry` not defined / `GardyndClient` not importable from package.

- [ ] **Step 3: Implement the entity base**

`custom_components/gardyn/entity.py`:
```python
"""Base entity for the Gardyn integration."""
from __future__ import annotations

from homeassistant.helpers.device_info import DeviceInfo
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from .const import DOMAIN
from .coordinator import GardynCoordinator


class GardynEntity(CoordinatorEntity[GardynCoordinator]):
    """Common device_info + availability for all Gardyn entities."""

    _attr_has_entity_name = True

    def __init__(self, coordinator: GardynCoordinator, identifier: str) -> None:
        super().__init__(coordinator)
        self._identifier = identifier
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, identifier)},
            name="Gardyn",
            manufacturer="gardyn-of-eden",
            model="gardyn",
        )

    @property
    def available(self) -> bool:
        return super().available and self.coordinator.data is not None
```

- [ ] **Step 4: Implement entry setup/unload**

`custom_components/gardyn/__init__.py`:
```python
"""The Gardyn integration."""
from __future__ import annotations

from dataclasses import dataclass
from datetime import timedelta

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import CONF_HOST, CONF_PORT
from homeassistant.core import HomeAssistant
from homeassistant.helpers.aiohttp_client import async_get_clientsession

from .api import GardyndClient
from .const import CONF_SCAN_INTERVAL, DEFAULT_SCAN_INTERVAL, PLATFORMS
from .coordinator import GardynCoordinator


@dataclass
class GardynData:
    """Objects stored on the config entry."""

    client: GardyndClient
    coordinator: GardynCoordinator


type GardynConfigEntry = ConfigEntry[GardynData]


async def async_setup_entry(hass: HomeAssistant, entry: GardynConfigEntry) -> bool:
    session = async_get_clientsession(hass)
    client = GardyndClient(entry.data[CONF_HOST], entry.data[CONF_PORT], session)
    interval = entry.options.get(CONF_SCAN_INTERVAL, DEFAULT_SCAN_INTERVAL)
    coordinator = GardynCoordinator(hass, client, timedelta(seconds=interval))
    await coordinator.async_config_entry_first_refresh()

    entry.runtime_data = GardynData(client=client, coordinator=coordinator)
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    entry.async_on_unload(entry.add_update_listener(_async_reload))
    return True


async def async_unload_entry(hass: HomeAssistant, entry: GardynConfigEntry) -> bool:
    return await hass.config_entries.async_unload_platforms(entry, PLATFORMS)


async def _async_reload(hass: HomeAssistant, entry: GardynConfigEntry) -> None:
    await hass.config_entries.async_reload(entry.entry_id)
```

- [ ] **Step 5: Add the shared platform-test helper**

`tests/ha/helpers.py` (used by every platform task; lives here so Tasks 6–12 are
independent):
```python
from unittest.mock import AsyncMock, patch

from pytest_homeassistant_custom_component.common import MockConfigEntry


async def setup_with_mocks(hass, entry: MockConfigEntry, state_json, schedules_json):
    """Set up the entry with a mocked GardyndClient; returns the client mock."""
    entry.add_to_hass(hass)
    client = AsyncMock()
    client.get_state = AsyncMock(return_value=state_json)
    client.get_schedules = AsyncMock(return_value=schedules_json)
    with patch("custom_components.gardyn.GardyndClient", return_value=client):
        assert await hass.config_entries.async_setup(entry.entry_id)
        await hass.async_block_till_done()
    return client
```

- [ ] **Step 6: Run test to verify it passes**

Run: `python -m pytest tests/ha/test_init.py -q`
Expected: PASS.

---

### Task 5: Config flow (user + zeroconf + options)

**Depends on:** Task 2 (client), Task 1 (const)

**Files:**
- Create: `custom_components/gardyn/config_flow.py`
- Test: `tests/ha/test_config_flow.py`

- [ ] **Step 1: Write the failing test**

`tests/ha/test_config_flow.py`:
```python
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python -m pytest tests/ha/test_config_flow.py -q`
Expected: ImportError / no config flow registered.

- [ ] **Step 3: Implement the config flow**

`custom_components/gardyn/config_flow.py`:
```python
"""Config flow for the Gardyn integration."""
from __future__ import annotations

from typing import Any

import voluptuous as vol
from homeassistant.config_entries import (
    ConfigFlow,
    ConfigFlowResult,
    OptionsFlow,
    ConfigEntry,
)
from homeassistant.const import CONF_HOST, CONF_PORT
from homeassistant.core import callback
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.service_info.zeroconf import ZeroconfServiceInfo

from .api import GardyndApiError, GardyndClient
from .const import CONF_SCAN_INTERVAL, DEFAULT_PORT, DEFAULT_SCAN_INTERVAL, DOMAIN, MIN_SCAN_INTERVAL


class GardynConfigFlow(ConfigFlow, domain=DOMAIN):
    """Handle the Gardyn config flow."""

    def __init__(self) -> None:
        self._host: str | None = None
        self._port: int = DEFAULT_PORT

    async def _validate(self, host: str, port: int) -> str:
        """Return the device identifier or raise GardyndApiError."""
        client = GardyndClient(host, port, async_get_clientsession(self.hass))
        await client.healthz()
        state = await client.get_state()
        return str(state.get("identifier") or self.context.get("unique_id") or f"{host}:{port}")

    async def async_step_user(self, user_input: dict[str, Any] | None = None) -> ConfigFlowResult:
        errors: dict[str, str] = {}
        if user_input is not None:
            try:
                identifier = await self._validate(user_input[CONF_HOST], user_input[CONF_PORT])
            except GardyndApiError:
                errors["base"] = "cannot_connect"
            else:
                await self.async_set_unique_id(identifier)
                self._abort_if_unique_id_configured()
                return self.async_create_entry(title="Gardyn", data=user_input)
        return self.async_show_form(
            step_id="user",
            data_schema=vol.Schema(
                {
                    vol.Required(CONF_HOST): str,
                    vol.Required(CONF_PORT, default=DEFAULT_PORT): int,
                }
            ),
            errors=errors,
        )

    async def async_step_zeroconf(self, discovery_info: ZeroconfServiceInfo) -> ConfigFlowResult:
        self._host = str(discovery_info.ip_address)
        self._port = discovery_info.port or DEFAULT_PORT
        return await self.async_step_zeroconf_confirm()

    async def async_step_zeroconf_confirm(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        errors: dict[str, str] = {}
        if user_input is not None:
            try:
                identifier = await self._validate(self._host, self._port)
            except GardyndApiError:
                errors["base"] = "cannot_connect"
            else:
                await self.async_set_unique_id(identifier)
                self._abort_if_unique_id_configured()
                return self.async_create_entry(
                    title="Gardyn", data={CONF_HOST: self._host, CONF_PORT: self._port}
                )
        return self.async_show_form(step_id="zeroconf_confirm", errors=errors)

    @staticmethod
    @callback
    def async_get_options_flow(config_entry: ConfigEntry) -> OptionsFlow:
        return GardynOptionsFlow()


class GardynOptionsFlow(OptionsFlow):
    """Adjust the poll interval."""

    async def async_step_init(self, user_input: dict[str, Any] | None = None) -> ConfigFlowResult:
        if user_input is not None:
            return self.async_create_entry(title="", data=user_input)
        current = self.config_entry.options.get(CONF_SCAN_INTERVAL, DEFAULT_SCAN_INTERVAL)
        return self.async_show_form(
            step_id="init",
            data_schema=vol.Schema(
                {vol.Required(CONF_SCAN_INTERVAL, default=current): vol.All(int, vol.Range(min=MIN_SCAN_INTERVAL))}
            ),
        )
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python -m pytest tests/ha/test_config_flow.py -q`
Expected: PASS.

---

### Task 6: Light platform

**Depends on:** Task 4

**Files:**
- Create: `custom_components/gardyn/light.py`
- Test: `tests/ha/test_light.py`

- [ ] **Step 1: Write the failing test**

`tests/ha/test_light.py`:
```python
from unittest.mock import AsyncMock, patch

from custom_components.gardyn.const import DOMAIN
from .helpers import setup_with_mocks  # created in Task 4


async def test_light_state_and_turn_on(hass, mock_entry, state_json, schedules_json):
    client = await setup_with_mocks(hass, mock_entry, state_json, schedules_json)
    state = hass.states.get("light.gardyn_light")
    assert state.state == "on"
    assert state.attributes["brightness"] == round(70 * 255 / 100)

    await hass.services.async_call(
        "light", "turn_on", {"entity_id": "light.gardyn_light", "brightness": 255}, blocking=True
    )
    client.set_brightness.assert_awaited_with(100)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python -m pytest tests/ha/test_light.py -q`
Expected: FAIL — `light.gardyn_light` not found (no light platform).
(The `setup_with_mocks` helper was created in Task 4.)

- [ ] **Step 3: Implement the light platform**

`custom_components/gardyn/light.py`:
```python
"""Light platform for Gardyn."""
from __future__ import annotations

from typing import Any

from homeassistant.components.light import ATTR_BRIGHTNESS, ColorMode, LightEntity
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import GardynConfigEntry
from .entity import GardynEntity


async def async_setup_entry(
    hass: HomeAssistant, entry: GardynConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    data = entry.runtime_data
    async_add_entities([GardynLight(data.coordinator, data.client, entry.unique_id)])


class GardynLight(GardynEntity, LightEntity):
    """The Gardyn grow light."""

    _attr_translation_key = "light"
    _attr_name = "Light"
    _attr_color_mode = ColorMode.BRIGHTNESS
    _attr_supported_color_modes = {ColorMode.BRIGHTNESS}

    def __init__(self, coordinator, client, identifier) -> None:
        super().__init__(coordinator, identifier)
        self._client = client
        self._attr_unique_id = f"{identifier}_light"

    @property
    def is_on(self) -> bool:
        return bool(self.coordinator.data["light"]["on"])

    @property
    def brightness(self) -> int:
        pct = self.coordinator.data["light"]["brightness"]
        return round(pct * 255 / 100)

    async def async_turn_on(self, **kwargs: Any) -> None:
        if ATTR_BRIGHTNESS in kwargs:
            pct = round(kwargs[ATTR_BRIGHTNESS] * 100 / 255)
            await self._client.set_brightness(pct)
        else:
            await self._client.light_on()
        await self.coordinator.async_request_refresh()

    async def async_turn_off(self, **kwargs: Any) -> None:
        await self._client.light_off()
        await self.coordinator.async_request_refresh()
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python -m pytest tests/ha/test_light.py -q`
Expected: PASS.

---

### Task 7: Fan platform (pump)

**Depends on:** Task 4

**Files:**
- Create: `custom_components/gardyn/fan.py`
- Test: `tests/ha/test_fan.py`

- [ ] **Step 1: Write the failing test**

`tests/ha/test_fan.py`:
```python
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python -m pytest tests/ha/test_fan.py -q`
Expected: FAIL — `fan.gardyn_pump` not found.

- [ ] **Step 3: Implement the fan platform**

`custom_components/gardyn/fan.py`:
```python
"""Fan platform (pump) for Gardyn."""
from __future__ import annotations

from typing import Any

from homeassistant.components.fan import FanEntity, FanEntityFeature
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import GardynConfigEntry
from .entity import GardynEntity


async def async_setup_entry(
    hass: HomeAssistant, entry: GardynConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    data = entry.runtime_data
    async_add_entities([GardynPump(data.coordinator, data.client, entry.unique_id)])


class GardynPump(GardynEntity, FanEntity):
    """The Gardyn water pump as a variable-speed fan."""

    _attr_translation_key = "pump"
    _attr_name = "Pump"
    _attr_supported_features = (
        FanEntityFeature.SET_SPEED | FanEntityFeature.TURN_ON | FanEntityFeature.TURN_OFF
    )

    def __init__(self, coordinator, client, identifier) -> None:
        super().__init__(coordinator, identifier)
        self._client = client
        self._attr_unique_id = f"{identifier}_pump"

    @property
    def is_on(self) -> bool:
        return bool(self.coordinator.data["pump"]["on"])

    @property
    def percentage(self) -> int:
        return int(self.coordinator.data["pump"]["speed"])

    async def async_set_percentage(self, percentage: int) -> None:
        if percentage == 0:
            await self._client.pump_off()
        else:
            await self._client.set_speed(percentage)
        await self.coordinator.async_request_refresh()

    async def async_turn_on(self, percentage: int | None = None, preset_mode: str | None = None, **kwargs: Any) -> None:
        if percentage is not None:
            await self._client.set_speed(percentage)
        else:
            await self._client.pump_on()
        await self.coordinator.async_request_refresh()

    async def async_turn_off(self, **kwargs: Any) -> None:
        await self._client.pump_off()
        await self.coordinator.async_request_refresh()
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python -m pytest tests/ha/test_fan.py -q`
Expected: PASS.

---

### Task 8: Sensor platform (telemetry + schedule sensors)

**Depends on:** Task 4

**Files:**
- Create: `custom_components/gardyn/sensor.py`
- Test: `tests/ha/test_sensor.py`

- [ ] **Step 1: Write the failing test**

`tests/ha/test_sensor.py`:
```python
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python -m pytest tests/ha/test_sensor.py -q`
Expected: FAIL — sensors not found.

- [ ] **Step 3: Implement the sensor platform**

`custom_components/gardyn/sensor.py`:
```python
"""Sensor platform for Gardyn."""
from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass

from homeassistant.components.sensor import (
    SensorDeviceClass,
    SensorEntity,
    SensorEntityDescription,
    SensorStateClass,
)
from homeassistant.const import (
    PERCENTAGE,
    UnitOfElectricCurrent,
    UnitOfElectricPotential,
    UnitOfLength,
    UnitOfPower,
    UnitOfTemperature,
)
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import GardynConfigEntry
from .entity import GardynEntity


@dataclass(frozen=True, kw_only=True)
class GardynSensorDescription(SensorEntityDescription):
    value_fn: Callable[[dict], float | None]


SENSORS: tuple[GardynSensorDescription, ...] = (
    GardynSensorDescription(
        key="temperature", translation_key="temperature", name="Temperature",
        device_class=SensorDeviceClass.TEMPERATURE, native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
        value_fn=lambda d: d["sensors"]["temperature_c"],
    ),
    GardynSensorDescription(
        key="humidity", translation_key="humidity", name="Humidity",
        device_class=SensorDeviceClass.HUMIDITY, native_unit_of_measurement=PERCENTAGE,
        state_class=SensorStateClass.MEASUREMENT,
        value_fn=lambda d: d["sensors"]["humidity_pct"],
    ),
    GardynSensorDescription(
        key="pcb_temp", translation_key="pcb_temp", name="PCB Temperature",
        device_class=SensorDeviceClass.TEMPERATURE, native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
        value_fn=lambda d: d["sensors"]["pcb_temp_c"],
    ),
    GardynSensorDescription(
        key="water_level", translation_key="water_level", name="Water Level",
        device_class=SensorDeviceClass.DISTANCE, native_unit_of_measurement=UnitOfLength.CENTIMETERS,
        state_class=SensorStateClass.MEASUREMENT,
        value_fn=lambda d: d["sensors"]["water_level_cm"],
    ),
    GardynSensorDescription(
        key="pump_voltage", translation_key="pump_voltage", name="Pump Voltage",
        device_class=SensorDeviceClass.VOLTAGE, native_unit_of_measurement=UnitOfElectricPotential.VOLT,
        state_class=SensorStateClass.MEASUREMENT,
        value_fn=lambda d: (d["sensors"].get("pump") or {}).get("bus_voltage"),
    ),
    GardynSensorDescription(
        key="pump_current", translation_key="pump_current", name="Pump Current",
        device_class=SensorDeviceClass.CURRENT, native_unit_of_measurement=UnitOfElectricCurrent.AMPERE,
        state_class=SensorStateClass.MEASUREMENT,
        value_fn=lambda d: (d["sensors"].get("pump") or {}).get("current"),
    ),
    GardynSensorDescription(
        key="pump_power", translation_key="pump_power", name="Pump Power",
        device_class=SensorDeviceClass.POWER, native_unit_of_measurement=UnitOfPower.WATT,
        state_class=SensorStateClass.MEASUREMENT,
        value_fn=lambda d: (d["sensors"].get("pump") or {}).get("power"),
    ),
)


async def async_setup_entry(
    hass: HomeAssistant, entry: GardynConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    data = entry.runtime_data
    entities: list = [GardynSensor(data.coordinator, entry.unique_id, d) for d in SENSORS]
    entities += [
        GardynScheduleSensor(data.coordinator, entry.unique_id, ch) for ch in ("light", "pump")
    ]
    async_add_entities(entities)


class GardynSensor(GardynEntity, SensorEntity):
    def __init__(self, coordinator, identifier, description: GardynSensorDescription) -> None:
        super().__init__(coordinator, identifier)
        self.entity_description = description
        self._attr_unique_id = f"{identifier}_{description.key}"

    @property
    def native_value(self) -> float | None:
        return self.entity_description.value_fn(self.coordinator.data)

    @property
    def available(self) -> bool:
        return super().available and self.entity_description.value_fn(self.coordinator.data) is not None


class GardynScheduleSensor(GardynEntity, SensorEntity):
    """State = entry count; attributes carry the full schedule + enabled flag."""

    def __init__(self, coordinator, identifier, channel: str) -> None:
        super().__init__(coordinator, identifier)
        self._channel = channel
        self._attr_translation_key = f"{channel}_schedule"
        self._attr_name = f"{channel.capitalize()} Schedule"
        self._attr_unique_id = f"{identifier}_{channel}_schedule"

    def _sched(self) -> dict:
        return self.coordinator.data["schedules_detail"].get(self._channel, {})

    @property
    def native_value(self) -> int:
        return len(self._sched().get("entries", []))

    @property
    def extra_state_attributes(self) -> dict:
        sched = self._sched()
        return {"enabled": sched.get("enabled", False), "entries": sched.get("entries", [])}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python -m pytest tests/ha/test_sensor.py -q`
Expected: PASS.

---

### Task 9: Binary sensor platform

**Depends on:** Task 4

**Files:**
- Create: `custom_components/gardyn/binary_sensor.py`
- Test: `tests/ha/test_binary_sensor.py`

- [ ] **Step 1: Write the failing test**

`tests/ha/test_binary_sensor.py`:
```python
import copy

from .helpers import setup_with_mocks


async def test_binary_sensors(hass, mock_entry, state_json, schedules_json):
    s = copy.deepcopy(state_json)
    s["water"]["low"] = True
    s["overtemp"] = False
    await setup_with_mocks(hass, mock_entry, s, schedules_json)
    assert hass.states.get("binary_sensor.gardyn_water_low").state == "on"
    assert hass.states.get("binary_sensor.gardyn_over_temp").state == "off"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python -m pytest tests/ha/test_binary_sensor.py -q`
Expected: FAIL — binary sensors not found.

- [ ] **Step 3: Implement the platform**

`custom_components/gardyn/binary_sensor.py`:
```python
"""Binary sensor platform for Gardyn."""
from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass

from homeassistant.components.binary_sensor import (
    BinarySensorDeviceClass,
    BinarySensorEntity,
    BinarySensorEntityDescription,
)
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import GardynConfigEntry
from .entity import GardynEntity


@dataclass(frozen=True, kw_only=True)
class GardynBinaryDescription(BinarySensorEntityDescription):
    value_fn: Callable[[dict], bool]


BINARY_SENSORS: tuple[GardynBinaryDescription, ...] = (
    GardynBinaryDescription(
        key="water_low", translation_key="water_low", name="Water Low",
        device_class=BinarySensorDeviceClass.PROBLEM,
        value_fn=lambda d: bool(d["water"]["low"]),
    ),
    GardynBinaryDescription(
        key="over_temp", translation_key="over_temp", name="Over Temp",
        device_class=BinarySensorDeviceClass.PROBLEM,
        value_fn=lambda d: bool(d["overtemp"]),
    ),
)


async def async_setup_entry(
    hass: HomeAssistant, entry: GardynConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    data = entry.runtime_data
    async_add_entities(GardynBinarySensor(data.coordinator, entry.unique_id, d) for d in BINARY_SENSORS)


class GardynBinarySensor(GardynEntity, BinarySensorEntity):
    def __init__(self, coordinator, identifier, description: GardynBinaryDescription) -> None:
        super().__init__(coordinator, identifier)
        self.entity_description = description
        self._attr_unique_id = f"{identifier}_{description.key}"

    @property
    def is_on(self) -> bool:
        return self.entity_description.value_fn(self.coordinator.data)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python -m pytest tests/ha/test_binary_sensor.py -q`
Expected: PASS.

---

### Task 10: Switch platform (schedule enable)

**Depends on:** Task 4

**Files:**
- Create: `custom_components/gardyn/switch.py`
- Test: `tests/ha/test_switch.py`

- [ ] **Step 1: Write the failing test**

`tests/ha/test_switch.py`:
```python
from .helpers import setup_with_mocks


async def test_schedule_switch_toggle(hass, mock_entry, state_json, schedules_json):
    client = await setup_with_mocks(hass, mock_entry, state_json, schedules_json)
    assert hass.states.get("switch.gardyn_light_schedule").state == "on"

    await hass.services.async_call(
        "switch", "turn_off", {"entity_id": "switch.gardyn_light_schedule"}, blocking=True
    )
    client.set_schedule_enabled.assert_awaited_with("light", False)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python -m pytest tests/ha/test_switch.py -q`
Expected: FAIL — switch not found.

- [ ] **Step 3: Implement the platform**

`custom_components/gardyn/switch.py`:
```python
"""Switch platform (schedule enable) for Gardyn."""
from __future__ import annotations

from typing import Any

from homeassistant.components.switch import SwitchEntity
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import GardynConfigEntry
from .entity import GardynEntity


async def async_setup_entry(
    hass: HomeAssistant, entry: GardynConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    data = entry.runtime_data
    async_add_entities(
        GardynScheduleSwitch(data.coordinator, data.client, entry.unique_id, ch)
        for ch in ("light", "pump")
    )


class GardynScheduleSwitch(GardynEntity, SwitchEntity):
    def __init__(self, coordinator, client, identifier, channel: str) -> None:
        super().__init__(coordinator, identifier)
        self._client = client
        self._channel = channel
        self._attr_translation_key = f"{channel}_schedule"
        self._attr_name = f"{channel.capitalize()} Schedule"
        self._attr_unique_id = f"{identifier}_{channel}_schedule_switch"

    @property
    def is_on(self) -> bool:
        return bool(self.coordinator.data["schedules"][self._channel]["enabled"])

    async def async_turn_on(self, **kwargs: Any) -> None:
        await self._client.set_schedule_enabled(self._channel, True)
        await self.coordinator.async_request_refresh()

    async def async_turn_off(self, **kwargs: Any) -> None:
        await self._client.set_schedule_enabled(self._channel, False)
        await self.coordinator.async_request_refresh()
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python -m pytest tests/ha/test_switch.py -q`
Expected: PASS.

---

### Task 11: Number platform (water threshold)

**Depends on:** Task 4

**Files:**
- Create: `custom_components/gardyn/number.py`
- Test: `tests/ha/test_number.py`

- [ ] **Step 1: Write the failing test**

`tests/ha/test_number.py`:
```python
from .helpers import setup_with_mocks


async def test_water_threshold_number(hass, mock_entry, state_json, schedules_json):
    client = await setup_with_mocks(hass, mock_entry, state_json, schedules_json)
    assert hass.states.get("number.gardyn_water_low_threshold").state == "10.0"

    await hass.services.async_call(
        "number", "set_value",
        {"entity_id": "number.gardyn_water_low_threshold", "value": 8.5}, blocking=True
    )
    client.set_water_threshold.assert_awaited_with(8.5)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python -m pytest tests/ha/test_number.py -q`
Expected: FAIL — number not found.

- [ ] **Step 3: Implement the platform**

`custom_components/gardyn/number.py`:
```python
"""Number platform (water-low threshold) for Gardyn."""
from __future__ import annotations

from homeassistant.components.number import NumberEntity, NumberMode
from homeassistant.const import UnitOfLength
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import GardynConfigEntry
from .entity import GardynEntity


async def async_setup_entry(
    hass: HomeAssistant, entry: GardynConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    data = entry.runtime_data
    async_add_entities([GardynWaterThreshold(data.coordinator, data.client, entry.unique_id)])


class GardynWaterThreshold(GardynEntity, NumberEntity):
    _attr_translation_key = "water_low_threshold"
    _attr_name = "Water Low Threshold"
    _attr_native_min_value = 0
    _attr_native_max_value = 15
    _attr_native_step = 0.5
    _attr_native_unit_of_measurement = UnitOfLength.CENTIMETERS
    _attr_mode = NumberMode.BOX

    def __init__(self, coordinator, client, identifier) -> None:
        super().__init__(coordinator, identifier)
        self._client = client
        self._attr_unique_id = f"{identifier}_water_low_threshold"

    @property
    def native_value(self) -> float:
        return float(self.coordinator.data["water"]["low_threshold_cm"])

    async def async_set_native_value(self, value: float) -> None:
        await self._client.set_water_threshold(value)
        await self.coordinator.async_request_refresh()
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python -m pytest tests/ha/test_number.py -q`
Expected: PASS.

---

### Task 12: Camera platform

**Depends on:** Task 4

**Files:**
- Create: `custom_components/gardyn/camera.py`
- Test: `tests/ha/test_camera.py`

- [ ] **Step 1: Write the failing test**

`tests/ha/test_camera.py`:
```python
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python -m pytest tests/ha/test_camera.py -q`
Expected: FAIL — camera not found.

- [ ] **Step 3: Implement the platform**

`custom_components/gardyn/camera.py`:
```python
"""Camera platform for Gardyn."""
from __future__ import annotations

import asyncio

import aiohttp
from homeassistant.components.camera import Camera
from homeassistant.core import HomeAssistant
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import GardynConfigEntry
from .entity import GardynEntity

_CAMERAS = (("upper", "Upper Camera"), ("lower", "Lower Camera"))


async def async_setup_entry(
    hass: HomeAssistant, entry: GardynConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    data = entry.runtime_data
    async_add_entities(
        GardynCamera(hass, data.coordinator, data.client, entry.unique_id, which, name)
        for which, name in _CAMERAS
    )


class GardynCamera(GardynEntity, Camera):
    def __init__(self, hass, coordinator, client, identifier, which, name) -> None:
        GardynEntity.__init__(self, coordinator, identifier)
        Camera.__init__(self)
        self._hass = hass
        self._client = client
        self._which = which
        self._attr_name = name
        self._attr_unique_id = f"{identifier}_{which}_camera"

    async def _fetch(self) -> bytes | None:
        session = async_get_clientsession(self._hass)
        try:
            async with asyncio.timeout(10):
                async with session.get(self._client.camera_url(self._which)) as resp:
                    if resp.status != 200:
                        return None
                    return await resp.read()
        except (aiohttp.ClientError, asyncio.TimeoutError):
            return None

    async def async_camera_image(self, width: int | None = None, height: int | None = None) -> bytes | None:
        return await self._fetch()
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python -m pytest tests/ha/test_camera.py -q`
Expected: PASS.

---

### Task 13: `set_schedule` service

**Depends on:** Task 4

**Files:**
- Modify: `custom_components/gardyn/__init__.py`
- Create: `custom_components/gardyn/services.yaml`
- Test: `tests/ha/test_service.py`

- [ ] **Step 1: Write the failing test**

`tests/ha/test_service.py`:
```python
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python -m pytest tests/ha/test_service.py -q`
Expected: FAIL — service `gardyn.set_schedule` not registered.

- [ ] **Step 3: Register the service + schema**

Add to `custom_components/gardyn/__init__.py` (imports + a one-time service
registration in `async_setup`, and a per-entry guard so it registers once):
```python
import re

import voluptuous as vol
from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant, ServiceCall
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import config_validation as cv

from .const import DOMAIN

_TIME_RE = re.compile(r"^([01]\d|2[0-3]):[0-5]\d$")


def _valid_entry(entry: dict) -> dict:
    if not _TIME_RE.match(str(entry.get("at", ""))):
        raise vol.Invalid(f"bad time {entry.get('at')!r} (want HH:MM)")
    if entry.get("action") not in ("on", "off"):
        raise vol.Invalid("action must be 'on' or 'off'")
    if "brightness" in entry and not 0 <= int(entry["brightness"]) <= 100:
        raise vol.Invalid("brightness must be 0..100")
    return entry


SET_SCHEDULE_SCHEMA = vol.Schema(
    {
        vol.Required("config_entry"): cv.string,
        vol.Required("channel"): vol.In(["light", "pump"]),
        vol.Optional("enabled", default=True): cv.boolean,
        vol.Required("entries"): [vol.All(dict, _valid_entry)],
    }
)


async def async_setup(hass: HomeAssistant, config) -> bool:
    async def _handle_set_schedule(call: ServiceCall) -> None:
        entry_id = call.data["config_entry"]
        entry: ConfigEntry | None = hass.config_entries.async_get_entry(entry_id)
        if entry is None or entry.domain != DOMAIN:
            raise HomeAssistantError(f"unknown gardyn config entry {entry_id}")
        client = entry.runtime_data.client
        schedule = {"enabled": call.data["enabled"], "entries": call.data["entries"]}
        await client.set_schedule(call.data["channel"], schedule)
        await entry.runtime_data.coordinator.async_request_refresh()

    hass.services.async_register(DOMAIN, "set_schedule", _handle_set_schedule, schema=SET_SCHEDULE_SCHEMA)
    return True
```

`custom_components/gardyn/services.yaml`:
```yaml
set_schedule:
  fields:
    config_entry:
      required: true
      selector:
        config_entry:
          integration: gardyn
    channel:
      required: true
      selector:
        select:
          options: ["light", "pump"]
    enabled:
      required: false
      default: true
      selector:
        boolean:
    entries:
      required: true
      selector:
        object:
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python -m pytest tests/ha/test_service.py -q`
Expected: PASS.

---

### Task 14: Translations, README, full suite, single commit

**Depends on:** Tasks 1–13

**Files:**
- Create: `custom_components/gardyn/strings.json`
- Create: `custom_components/gardyn/translations/en.json`
- Modify: `README.md`

- [ ] **Step 1: Add strings + translations**

`custom_components/gardyn/strings.json`:
```json
{
  "config": {
    "step": {
      "user": {
        "data": { "host": "Host", "port": "Port" }
      },
      "zeroconf_confirm": {
        "description": "Set up the discovered Gardyn?"
      }
    },
    "error": { "cannot_connect": "Failed to connect to gardynd" },
    "abort": { "already_configured": "This Gardyn is already configured" }
  },
  "options": {
    "step": {
      "init": { "data": { "scan_interval": "Polling interval (seconds)" } }
    }
  },
  "services": {
    "set_schedule": {
      "name": "Set schedule",
      "description": "Replace a channel's on-device schedule.",
      "fields": {
        "config_entry": { "name": "Gardyn", "description": "Target Gardyn." },
        "channel": { "name": "Channel", "description": "light or pump." },
        "enabled": { "name": "Enabled", "description": "Enable the schedule." },
        "entries": { "name": "Entries", "description": "List of {at, action, brightness}." }
      }
    }
  }
}
```
Copy the same content to `custom_components/gardyn/translations/en.json`.

- [ ] **Step 2: Add a README section**

Append to `README.md`:
```markdown
## Home Assistant integration (gardyn)

Install via HACS (add this repo as a custom repository, type *Integration*),
restart Home Assistant, then **Settings → Devices & Services → Add Integration →
Gardyn**. The Pi is auto-discovered via zeroconf, or enter its host/port (default
5000).

Edit schedules with the `gardyn.set_schedule` service, e.g.:
```yaml
service: gardyn.set_schedule
data:
  config_entry: <your gardyn entry>
  channel: light
  enabled: true
  entries:
    - { at: "06:00", action: "on", brightness: 70 }
    - { at: "20:00", action: "off" }
```
Schedules run autonomously on the Gardyn; Home Assistant only edits and displays
them.
```

- [ ] **Step 3: Run the full integration test suite**

Run: `python -m pytest tests/ha/ -q`
Expected: all tests PASS.

- [ ] **Step 4: Lint check (if hassfest/ruff available)**

Run: `python -m pytest tests/ha/ -q && python -c "import json,glob; [json.load(open(f)) for f in glob.glob('custom_components/gardyn/*.json')]"`
Expected: JSON valid, tests green. (If `ruff` is configured in the repo, also run `ruff check custom_components/gardyn`.)

- [ ] **Step 5: Commit (single commit for the whole plan)**

Run:
```
git add custom_components/ tests/ha/ hacs.json requirements_test.txt README.md
git commit -m "feat: Home Assistant 'gardyn' integration (REST front end to gardynd)"
```

---

## Self-Review

**Spec coverage:** config entry + config flow (manual) ✓ Task 5; zeroconf ✓ Task
5; options/poll interval ✓ Task 5; coordinator polling /state + /schedules ✓ Task
3; light ✓ T6; fan/pump ✓ T7; sensors (env/pcb/water/power) ✓ T8; schedule
sensors with entry attributes ✓ T8; binary_sensors water_low/overtemp ✓ T9;
schedule-enable switches ✓ T10; water-threshold number ✓ T11; cameras ✓ T12;
`set_schedule` service → PUT /schedules ✓ T13; availability via coordinator ✓
(entity.py, T4); null sensor → unavailable ✓ T8; device grouping by identifier ✓
T4; HACS packaging ✓ T1/T14; tests with pytest-homeassistant-custom-component ✓
throughout. No spec requirement is unaddressed.

**Placeholder scan:** None. The lint step notes ruff "if configured" — a
conditional on an existing tool, not deferred work. The translations step copies
a concrete JSON, not a TBD.

**Type consistency:** `GardyndClient` method names (`get_state`, `get_schedules`,
`light_on/off`, `set_brightness`, `pump_on/off`, `set_speed`, `set_schedule`,
`set_schedule_enabled`, `set_water_threshold`, `camera_url`, `healthz`) defined
in Task 2 are the exact names called in Tasks 4–13. `GardynData{client,
coordinator}` (T4) and `entry.runtime_data` are read identically in every
platform's `async_setup_entry`. `GardynEntity(coordinator, identifier)` signature
(T4) is matched by every entity constructor. Coordinator data keys (`light`,
`pump`, `sensors`, `water`, `overtemp`, `schedules`, `schedules_detail`) used by
platforms match what Task 3 produces and the `/state` fixture in Task 1.

**Dependency audit:** Task 1 (none) creates the package + const (with the full
`PLATFORMS` list, so platform tasks never edit a shared file). Task 2 → T1. Task
3 → T2. Task 4 → T3. Task 5 → T2+T1 (separate file `config_flow.py`). Tasks 6–12
each create one disjoint platform file plus its own test, read `entry.runtime_data`
from T4, and use the shared `tests/ha/helpers.py` created in T4 → they depend
only on T4 and are mutually parallel (no shared file among them, since `PLATFORMS`
was fully declared in T1). Task 13 modifies `__init__.py` (created T1/expanded
T4) → depends on T4; no other task edits `__init__.py`. Task 14 touches
translations/README + runs the full suite → depends on all. No task marked
`Depends on: none` (only Task 1) shares a file with another. Audit clean.
