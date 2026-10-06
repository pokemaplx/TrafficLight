from __future__ import annotations

import asyncio
import ipaddress
import json
import secrets
import socket
import time
import webbrowser
from collections import OrderedDict
from datetime import datetime
from pathlib import Path
from typing import TYPE_CHECKING, Any, Awaitable, Callable, Iterable
from urllib.parse import urlsplit

from aiohttp import WSMsgType, web

from trafficlight.config import config
from .auth import COOKIE_NAME, SESSION_SECONDS, Auth
from .serialize import proto_detail, proto_export, proto_summary

if TYPE_CHECKING:
    from trafficlight.proto_utils import Proto

STATIC_DIR = Path(__file__).parent / "static"
# content types are set explicitly because Windows' registry sometimes maps .js to text/plain
STATIC_FILES = {
    "index.html": "text/html",
    "app.js": "text/javascript",
    "style.css": "text/css",
    "logo.png": "image/png",
    "favicon.ico": "image/x-icon",
}
# Inter and JetBrains Mono, licensed under the SIL Open Font License (see static/fonts)
FONT_FILES = {
    "inter-latin.woff2",
    "inter-latin-ext.woff2",
    "jetbrains-mono-latin.woff2",
    "jetbrains-mono-latin-ext.woff2",
}
NO_CACHE = {"Cache-Control": "no-cache"}
WILDCARD_HOSTS = ("", "0.0.0.0", "::")

HISTORY_CHUNK_SIZE = 500
MAX_CLIENT_BACKLOG = 1000


class Record:
    def __init__(self, record_id: int, rpc_id: int, rpc_status: int, rpc_handle: int | None, protos: list[Proto]):
        self.id: int = record_id
        self.time: float = time.time()
        self.rpc_id: int = rpc_id
        self.rpc_status: int = rpc_status
        self.rpc_handle: int | None = rpc_handle
        self.protos: list[Proto] = protos

        # serialized once, every browser gets the same
        self.summary: str = json.dumps(
            {
                "id": record_id,
                "time": round(self.time * 1000),
                "rpc_id": rpc_id,
                "rpc_status": rpc_status,
                "rpc_handle": rpc_handle,
                "protos": [proto_summary(proto) for proto in protos],
            }
        )

    def export(self) -> dict[str, Any]:
        # same shape mitms send, so an export can be posted to Traffic Light again
        return {
            "time": datetime.fromtimestamp(self.time).astimezone().isoformat(timespec="milliseconds"),
            "rpcid": self.rpc_id,
            "rpcstatus": self.rpc_status,
            "rpchandle": self.rpc_handle,
            "protos": [proto_export(proto) for proto in self.protos],
        }


class Client:
    """A connected browser. Messages are queued so they arrive in order without slowing down the receiver"""

    def __init__(self, ws: web.WebSocketResponse):
        self.ws: web.WebSocketResponse = ws
        self._queue: asyncio.Queue[str] = asyncio.Queue()
        self._closing: asyncio.Task | None = None

    @property
    def backlog(self) -> int:
        return self._queue.qsize()

    def send(self, message: str) -> None:
        self._queue.put_nowait(message)

    def close(self) -> None:
        self._closing = asyncio.create_task(self.ws.close())

    async def write(self) -> None:
        try:
            while True:
                await self.ws.send_str(await self._queue.get())
        except ConnectionError:
            await self.ws.close()


class WebServer:
    def __init__(self) -> None:
        self._records: OrderedDict[int, Record] = OrderedDict()
        self._next_id: int = 1
        self._clients: set[Client] = set()
        self._paused: bool = False
        self._serving: asyncio.Task | None = None
        self._auth: Auth = Auth(config.web_password.get_secret_value())
        # lets browsers tell a reconnect from a restart
        self._session: str = secrets.token_hex(8)

    async def start(self) -> None:
        app = web.Application(middlewares=[_guard(self._auth), _login_required(self._auth)])
        app.add_routes(
            [
                web.get("/", self._static),
                # browsers ask for it on their own, i.e. for bookmarks
                web.get("/favicon.ico", self._static),
                web.get("/static/{name}", self._static),
                web.get("/static/fonts/{name}", self._font),
                web.get("/ws", self._websocket),
                web.get("/api/records/{record_id}/{proto_index}", self._proto),
                web.get("/api/export", self._export),
                web.get("/api/session", self._session_check),
                web.get("/login", self._login_page),
                web.post("/login", self._login),
                web.post("/logout", self._logout),
            ]
        )

        runner = web.AppRunner(app, access_log=None)
        await runner.setup()
        try:
            await web.TCPSite(runner, config.web_host, config.web_port).start()
        except OSError as e:
            raise SystemExit(f"Couldn't start the web UI on {config.web_host}:{config.web_port}: {e}")
        self._serving = asyncio.create_task(self._serve(runner))

        url = _web_url()
        print(f"Traffic Light is running at {url}")
        print(f"Send your traffic to {_receiver_url()}")
        if self._auth.enabled:
            print("The web UI asks for your password")
        print("Press Ctrl+C to quit")

        if config.web_open_browser:
            await asyncio.get_running_loop().run_in_executor(None, webbrowser.open, url)

    @staticmethod
    async def _serve(runner: web.AppRunner) -> None:
        # runs until Traffic Light quits, then closes the server
        try:
            await asyncio.Event().wait()
        finally:
            await runner.cleanup()

    def add_record(self, rpc_id: int, rpc_status: int, protos: list[Proto], rpc_handle: int | None = None) -> None:
        if self._paused:
            return

        record = Record(self._next_id, rpc_id, rpc_status, rpc_handle, protos)
        self._next_id += 1

        self._records[record.id] = record
        while len(self._records) > config.web_max_records:
            self._records.popitem(last=False)

        self._broadcast(_records_message([record.summary]))

    def _broadcast(self, message: str) -> None:
        for client in tuple(self._clients):
            if client.backlog > MAX_CLIENT_BACKLOG:
                # hopelessly behind. dropping it makes the browser reconnect and start over
                self._clients.discard(client)
                client.close()
            else:
                client.send(message)

    def _handle_command(self, data: str) -> None:
        try:
            command = json.loads(data)
        except json.JSONDecodeError:
            return
        if not isinstance(command, dict):
            return

        if command.get("type") == "pause":
            self._paused = bool(command.get("value"))
            self._broadcast(json.dumps({"type": "state", "paused": self._paused}))
        elif command.get("type") == "clear":
            self._records.clear()
            self._broadcast(json.dumps({"type": "clear"}))

    def _hello(self) -> str:
        return json.dumps(
            {
                "type": "hello",
                "session": self._session,
                "paused": self._paused,
                "max_records": config.web_max_records,
                "receiver": _receiver_url(),
                "auth": self._auth.enabled,
            }
        )

    def _history(self) -> Iterable[str]:
        summaries = [record.summary for record in self._records.values()]
        for start in range(0, len(summaries), HISTORY_CHUNK_SIZE):
            yield _records_message(summaries[start : start + HISTORY_CHUNK_SIZE], message_type="history")

    async def _websocket(self, request: web.Request) -> web.WebSocketResponse:
        ws = web.WebSocketResponse(heartbeat=30)
        await ws.prepare(request)

        # nothing is awaited until the client is registered, so it can't miss or double a record
        client = Client(ws)
        client.send(self._hello())
        for chunk in self._history():
            client.send(chunk)
        self._clients.add(client)
        writer = asyncio.create_task(client.write())

        try:
            async for message in ws:
                if message.type == WSMsgType.TEXT:
                    self._handle_command(message.data)
        finally:
            self._clients.discard(client)
            writer.cancel()

        return ws

    async def _proto(self, request: web.Request) -> web.Response:
        try:
            record = self._records[int(request.match_info["record_id"])]
            proto = record.protos[int(request.match_info["proto_index"])]
        except (KeyError, IndexError, ValueError):
            raise web.HTTPNotFound(text="This proto isn't available anymore")

        return web.json_response(proto_detail(proto))

    async def _export(self, _: web.Request) -> web.Response:
        filename = datetime.now().strftime("trafficlight-%Y-%m-%d-%H%M%S.json")
        return web.json_response(
            [record.export() for record in self._records.values()],
            headers={"Content-Disposition": f'attachment; filename="{filename}"'},
        )

    async def _login_page(self, request: web.Request) -> web.Response:
        if not self._auth.enabled or self._auth.valid_session(request.cookies.get(COOKIE_NAME)):
            raise web.HTTPFound("/")

        return web.Response(
            body=(STATIC_DIR / "login.html").read_bytes(), content_type="text/html", charset="utf-8", headers=NO_CACHE
        )

    async def _login(self, request: web.Request) -> web.Response:
        password = (await request.post()).get("password")
        if self._auth.enabled and not (isinstance(password, str) and await self._auth.check_password(password)):
            return web.Response(status=303, headers={"Location": "/login?error"})

        response = web.Response(status=303, headers={"Location": "/"})
        if self._auth.enabled:
            _set_session_cookie(request, response, self._auth.new_session(), SESSION_SECONDS)
        return response

    @staticmethod
    async def _logout(request: web.Request) -> web.Response:
        response = web.Response(status=303, headers={"Location": "/login"})
        _set_session_cookie(request, response, "", 0)
        return response

    @staticmethod
    async def _session_check(_: web.Request) -> web.Response:
        # only reachable with a valid session, see _login_required
        return web.Response(status=204)

    @staticmethod
    async def _static(request: web.Request) -> web.Response:
        name = request.match_info.get("name", request.path.lstrip("/") or "index.html")
        content_type = STATIC_FILES.get(name)
        if content_type is None:
            raise web.HTTPNotFound()

        return web.Response(
            body=(STATIC_DIR / name).read_bytes(),
            content_type=content_type,
            charset="utf-8" if content_type.startswith("text/") else None,
            headers=NO_CACHE,
        )

    @staticmethod
    async def _font(request: web.Request) -> web.Response:
        name = request.match_info["name"]
        if name not in FONT_FILES:
            raise web.HTTPNotFound()

        # fonts never change, unlike the rest of the page
        return web.Response(
            body=(STATIC_DIR / "fonts" / name).read_bytes(),
            content_type="font/woff2",
            headers={"Cache-Control": "max-age=604800"},
        )


def _records_message(summaries: list[str], message_type: str = "records") -> str:
    # summaries are json already
    return '{"type": "' + message_type + '", "records": [' + ", ".join(summaries) + "]}"


def _guard(auth: Auth) -> Callable[..., Awaitable[web.StreamResponse]]:
    """
    The UI shows your game traffic, so other websites must not be able to read it.
    Checking Origin prevents cross-site websockets. Without a password, checking Host prevents DNS rebinding,
    with one, the session cookie does: it never goes to another domain.
    """

    @web.middleware
    async def middleware(
        request: web.Request, handler: Callable[[web.Request], Awaitable[web.StreamResponse]]
    ) -> web.StreamResponse:
        if not auth.enabled and not _is_trusted_host(_hostname(request.host)):
            raise web.HTTPForbidden(
                text="Open Traffic Light through localhost or an IP address, or set a password to use a domain"
            )

        origin = request.headers.get("Origin")
        if origin is not None and urlsplit(origin).netloc.lower() != request.host.lower():
            raise web.HTTPForbidden(text="Forbidden")

        return await handler(request)

    return middleware


def _login_required(auth: Auth) -> Callable[..., Awaitable[web.StreamResponse]]:
    @web.middleware
    async def middleware(
        request: web.Request, handler: Callable[[web.Request], Awaitable[web.StreamResponse]]
    ) -> web.StreamResponse:
        # the static files are in the public repository anyway, the login page needs them
        public = request.path in ("/login", "/favicon.ico") or request.path.startswith("/static/")
        if not auth.enabled or public or auth.valid_session(request.cookies.get(COOKIE_NAME)):
            return await handler(request)

        if request.path == "/":
            raise web.HTTPFound("/login")
        raise web.HTTPUnauthorized(text="Log in first")

    return middleware


def _set_session_cookie(request: web.Request, response: web.Response, session: str, max_age: int) -> None:
    # behind a proxy like Coolify's, the browser talks https to the proxy and the proxy http to us
    https = request.secure or request.headers.get("X-Forwarded-Proto", "").split(",")[0].strip() == "https"
    response.set_cookie(
        COOKIE_NAME, session, max_age=max_age, path="/", httponly=True, samesite="Lax", secure=https or None
    )


def _hostname(netloc: str) -> str | None:
    try:
        return urlsplit(f"//{netloc}").hostname
    except ValueError:
        return None


def _is_trusted_host(host: str | None) -> bool:
    if host is None:
        return False
    if host in ("localhost", config.web_host.lower()):
        return True

    # ip addresses can't be rebound, only domains can
    try:
        ipaddress.ip_address(host)
    except ValueError:
        return False
    return True


def _url(host: str, port: int) -> str:
    return f"http://[{host}]:{port}" if ":" in host else f"http://{host}:{port}"


def _web_url() -> str:
    host = config.web_host
    if host in WILDCARD_HOSTS:
        host = "::1" if host == "::" else "127.0.0.1"
    return _url(host, config.web_port)


def _receiver_url() -> str:
    host = config.host
    if host in WILDCARD_HOSTS:
        # a container only knows its own address, not the one of the computer it runs on
        host = "<your computer's IP>" if _in_container() else _lan_ip()
    return _url(host, config.port)


def _in_container() -> bool:
    # docker and podman create these
    return Path("/.dockerenv").exists() or Path("/run/.containerenv").exists()


def _lan_ip() -> str:
    """The address your phone can reach this computer at"""
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
            # nothing is sent, connecting a udp socket only picks the interface
            s.connect(("10.254.254.254", 1))
            return s.getsockname()[0]
    except OSError:
        return "127.0.0.1"
