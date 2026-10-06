from __future__ import annotations

from typing import TYPE_CHECKING

from trafficlight.web import WebServer
from .base import BaseOutput

if TYPE_CHECKING:
    from trafficlight.proto_utils.proto import Proto


class WebOutput(BaseOutput):
    server: WebServer

    async def start(self) -> None:
        self.server = WebServer()
        await self.server.start()

    async def add_record(self, rpc_id: int, rpc_status: int, protos: list[Proto], rpc_handle: int | None = None) -> None:
        self.server.add_record(rpc_id=rpc_id, rpc_status=rpc_status, protos=protos, rpc_handle=rpc_handle)
