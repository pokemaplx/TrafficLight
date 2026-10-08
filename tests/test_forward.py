"""Tests for forwarding. Run with `uv run python tests/test_forward.py`.

Plain asserts and no test runner, like test_web_server.py. Forwarding sits in the receiver's path,
so what matters here is that it passes bodies on untouched and that a bad target stays harmless.
"""

from __future__ import annotations

import asyncio
import os
import sys
from pathlib import Path

from aiohttp import web

# config.py reads config.toml from the working directory, so run from the repository root
os.chdir(Path(__file__).resolve().parent.parent)
sys.path.insert(0, str(Path.cwd()))

from trafficlight.forward import MAX_IN_FLIGHT, Forwarder  # noqa: E402

PASSED = 0


def check(name: str, condition: bool, detail: str = "") -> None:
    global PASSED
    if condition:
        PASSED += 1
        print(f"  ok   {name}")
    else:
        print(f"  FAIL {name} {detail}")
        sys.exit(1)


class Target:
    """A stand in for whatever you forward to, i.e. a Golbat"""

    def __init__(self, status: int = 200) -> None:
        self.status: int = status
        self.bodies: list[bytes] = []
        self.headers: list[dict[str, str]] = []
        self._runner: web.AppRunner | None = None
        self.url: str = ""

    async def __aenter__(self) -> Target:
        app = web.Application()
        app.add_routes([web.post("/raw", self._handle)])
        self._runner = web.AppRunner(app)
        await self._runner.setup()
        site = web.TCPSite(self._runner, host="127.0.0.1", port=0)
        await site.start()
        port = self._runner.addresses[0][1]
        self.url = f"http://127.0.0.1:{port}/raw"
        return self

    async def __aexit__(self, *_) -> None:
        if self._runner is not None:
            await self._runner.cleanup()

    async def _handle(self, request: web.Request) -> web.Response:
        self.bodies.append(await request.read())
        self.headers.append(dict(request.headers))
        return web.Response(status=self.status)


async def drain(forwarder: Forwarder) -> None:
    """Waits for everything in flight, which a receiver never does"""
    while forwarder._tasks:
        await asyncio.gather(*list(forwarder._tasks), return_exceptions=True)


async def test_a_body_is_forwarded_untouched() -> None:
    # golbat's own shape, to make sure nothing re-serializes it into Traffic Light's
    body = b'{"username":"a","contents":[{"type":106,"request":"","payload":""}]}'
    async with Target() as target:
        forwarder = Forwarder(target.url)
        forwarder.send(body)
        await drain(forwarder)
        await forwarder.close()

    check("the body arrives byte for byte", target.bodies == [body], f"got {target.bodies}")
    check("it is sent as json", target.headers[0].get("Content-Type") == "application/json")
    check("no token is sent without one", "Authorization" not in target.headers[0])


async def test_a_token_is_sent_as_a_bearer_token() -> None:
    async with Target() as target:
        forwarder = Forwarder(target.url, "s3cret")
        forwarder.send(b"{}")
        await drain(forwarder)
        await forwarder.close()

    check("the token is a bearer token", target.headers[0].get("Authorization") == "Bearer s3cret")


async def test_nothing_is_forwarded_without_a_url() -> None:
    forwarder = Forwarder("")
    check("an empty url is disabled", not forwarder.enabled)
    forwarder.send(b"{}")
    check("sending does nothing at all", not forwarder._tasks)
    await forwarder.close()


async def test_an_unreachable_target_is_harmless() -> None:
    # port 1 is never anything, so this can only fail to connect
    forwarder = Forwarder("http://127.0.0.1:1/raw")
    forwarder.send(b"{}")
    await drain(forwarder)
    check("a failure is swallowed", forwarder._failing)
    await forwarder.close()


async def test_an_error_status_is_reported_once() -> None:
    async with Target(status=500) as target:
        forwarder = Forwarder(target.url)
        forwarder.send(b"{}")
        await drain(forwarder)
        check("an error status counts as failing", forwarder._failing)

        # recovering says so again, so the log tells you the truth either way
        target.status = 200
        forwarder.send(b"{}")
        await drain(forwarder)
        check("recovering clears the failure", not forwarder._failing)
        await forwarder.close()


async def test_requests_are_dropped_instead_of_piling_up() -> None:
    held = asyncio.Event()

    async def _handle(_: web.Request) -> web.Response:
        await held.wait()
        return web.Response()

    app = web.Application()
    app.add_routes([web.post("/raw", _handle)])
    runner = web.AppRunner(app)
    await runner.setup()
    site = web.TCPSite(runner, host="127.0.0.1", port=0)
    await site.start()
    url = f"http://127.0.0.1:{runner.addresses[0][1]}/raw"

    forwarder = Forwarder(url)
    for _ in range(MAX_IN_FLIGHT + 10):
        forwarder.send(b"{}")

    check("in flight requests are capped", len(forwarder._tasks) == MAX_IN_FLIGHT, f"got {len(forwarder._tasks)}")
    check("the overflow is counted as dropped", forwarder._dropped == 10, f"got {forwarder._dropped}")

    held.set()
    await forwarder.close()
    await runner.cleanup()


async def main() -> None:
    for name, test in sorted(globals().items()):
        if name.startswith("test_"):
            print(name[5:].replace("_", " "))
            await test()
    print(f"\n{PASSED} checks passed")


if __name__ == "__main__":
    asyncio.run(main())
