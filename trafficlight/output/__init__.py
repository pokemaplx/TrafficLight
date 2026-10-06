from trafficlight.config import Output as _OutputType
from .base import BaseOutput
from .discord import DiscordOutput
from .print_ import PrintOutput
from .ui import UiOutput
from .web import WebOutput


def get_output(output_type: _OutputType) -> BaseOutput:
    if output_type == _OutputType.PRINT:
        return PrintOutput()
    elif output_type == _OutputType.DISCORD:
        return DiscordOutput()
    elif output_type == _OutputType.WEB:
        return WebOutput()

    return UiOutput()
