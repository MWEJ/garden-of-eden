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
