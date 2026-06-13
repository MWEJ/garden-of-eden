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
