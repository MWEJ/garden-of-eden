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
from .const import (
    CONF_SCAN_INTERVAL,
    DEFAULT_PORT,
    DEFAULT_SCAN_INTERVAL,
    DOMAIN,
    MIN_SCAN_INTERVAL,
    ZEROCONF_TYPE,
)


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
        # gardynd advertises instance "gardynd-<identifier>"; derive the unique
        # id from the discovery name so an already-configured device is
        # deduplicated (and its host/port refreshed) before prompting the user.
        instance = discovery_info.name.removesuffix(f".{ZEROCONF_TYPE}")
        identifier = instance.removeprefix("gardynd-")
        if identifier:
            await self.async_set_unique_id(identifier)
            self._abort_if_unique_id_configured(
                updates={CONF_HOST: self._host, CONF_PORT: self._port}
            )
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
