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
