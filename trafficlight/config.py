from __future__ import annotations

import sys
from enum import Enum

import tomllib
from pydantic import BaseSettings, PositiveInt, ValidationError
from pydantic.env_settings import SettingsSourceCallable


class Output(Enum):
    UI = "ui"
    WEB = "web"
    PRINT = "print"
    DISCORD = "discord"


class Config(BaseSettings):
    host: str = "0.0.0.0"
    port: int = 3335
    output: Output = Output.UI
    webhook: str = ""
    web_host: str = "127.0.0.1"
    web_port: int = 3336
    web_open_browser: bool = True
    web_max_records: PositiveInt = 5000

    class Config:
        # every option can also be set as an environment variable, i.e. TRAFFICLIGHT_OUTPUT=web
        env_prefix = "trafficlight_"

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
