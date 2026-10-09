"""Passes every request the receiver gets on to another endpoint.

That way Traffic Light can sit in front of a scanner instead of taking its place: point your mitm
at Traffic Light and Traffic Light at the scanner's own endpoint, i.e. a Golbat's /raw.

Forwarding is fire and forget. The receiver answers the mitm without ever waiting on the other end,
nothing is retried, and a target that is down or slow only costs the forwarded requests themselves.
"""

from __future__ import annotations

import asyncio

import aiohttp

# a target that can't keep up would otherwise pile up requests until it runs us out of memory
MAX_IN_FLIGHT = 100
TIMEOUT = aiohttp.ClientTimeout(total=10)


class Forwarder:
    def __init__(self, url: str, token: str = "") -> None:
        self.url: str = url

        self._headers: dict[str, str] = {"Content-Type": "application/json"}
        if token:
            self._headers["Authorization"] = f"Bearer {token}"

        self._session: aiohttp.ClientSession | None = None
        self._tasks: set[asyncio.Task] = set()
        self._closed: bool = False

        # a dead target would print on every single request, so only changes are worth saying
        self._failing: bool = False
        self._dropped: int = 0

    @property
    def enabled(self) -> bool:
        return bool(self.url)

    def send(self, body: bytes) -> None:
        """Hands a body off to be forwarded. Never raises and never blocks the receiver"""
        if not self.enabled or self._closed:
            return

        if len(self._tasks) >= MAX_IN_FLIGHT:
            self._dropped += 1
            self._mark_failing("it can't keep up")
            return

        task = asyncio.create_task(self._post(body))
        # the loop only keeps a weak reference, so without this they'd be collected mid flight
        self._tasks.add(task)
        task.add_done_callback(self._tasks.discard)

    async def _post(self, body: bytes) -> None:
        # a request queued before the shutdown would open a session nobody is left to close
        if self._closed:
            return

        if self._session is None or self._session.closed:
            self._session = aiohttp.ClientSession(timeout=TIMEOUT)

        try:
            async with self._session.post(self.url, data=body, headers=self._headers) as response:
                if response.status >= 400:
                    self._mark_failing(f"it answered {response.status}")
                    return
        except (aiohttp.ClientError, asyncio.TimeoutError) as e:
            self._mark_failing(str(e) or type(e).__name__)
            return

        self._mark_working()

    def _mark_failing(self, reason: str) -> None:
        if self._failing or self._closed:
            return
        self._failing = True
        print(f"Forwarding to {self.url} failed: {reason}. Nothing is retried, Traffic Light keeps running")

    def _mark_working(self) -> None:
        if not self._failing or self._closed:
            return
        self._failing = False
        dropped, self._dropped = self._dropped, 0
        missed = f", {dropped} request(s) were dropped in between" if dropped else ""
        print(f"Forwarding to {self.url} works again{missed}")

    async def close(self) -> None:
        self._closed = True
        if self._session is not None and not self._session.closed:
            await self._session.close()
