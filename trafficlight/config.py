from __future__ import annotations

import sys
from enum import Enum

import tomllib
from pydantic import BaseSettings, PositiveInt, SecretStr, ValidationError
from pydantic.env_settings import SettingsSourceCallable


class Output(Enum):
    UI = "ui"
    WEB = "web"
    PRINT = "print"
    DISCORD = "discord"


class Config(BaseSettings):
    host: str = "0.0.0.0"
    port: int = 3335
    # every request the receiver gets is passed on to this url too, i.e. a Golbat's /raw. Empty: don't forward
    forward_url: str = ""
    forward_token: SecretStr = SecretStr("")
    output: Output = Output.UI
    webhook: str = ""
    web_host: str = "127.0.0.1"
    web_port: int = 3336
    web_open_browser: bool = True
    web_max_records: PositiveInt = 5000
    # empty: no login
    web_password: SecretStr = SecretStr("")

    class Config:
        # every option can also be set as an environment variable, i.e. TRAFFICLIGHT_OUTPUT=web
        env_prefix = "trafficlight_"
        # options that don't exist (anymore) are skipped, like they always were
        extra = "ignore"

        @classmethod
        def customise_sources(
            cls,
            init_settings: SettingsSourceCallable,
            env_settings: SettingsSourceCallable,
            file_secret_settings: SettingsSourceCallable,
        ) -> tuple[SettingsSourceCallable, ...]:
            # environment variables win over config.toml
            return env_settings, init_settings, file_secret_settings


try:
    with open("config.toml", mode="rb") as _config_file:
        _raw_config = tomllib.load(_config_file)
except FileNotFoundError:
    _raw_config = {}

try:
    _config = Config(**_raw_config)
except ValidationError as e:
    print(f"Config validation error!\n{e}")
    sys.exit(1)

config = _config
