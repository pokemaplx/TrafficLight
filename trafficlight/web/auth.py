from __future__ import annotations

import asyncio
import hashlib
import hmac
import time

COOKIE_NAME = "trafficlight_session"
SESSION_SECONDS = 30 * 24 * 60 * 60
FAILED_LOGIN_DELAY = 1.0


class Auth:
    """
    The optional password of the web UI. Sessions are signed cookies, so they survive restarts
    and changing the password ends all of them.
    """

    def __init__(self, password: str) -> None:
        self.enabled: bool = bool(password)
        self._password: bytes = password.encode()
        # slow on purpose, so a leaked cookie doesn't make guessing the password cheap
        self._key: bytes = hashlib.pbkdf2_hmac("sha256", self._password, b"trafficlight-session", 200_000)
        # wrong passwords wait in line, so trying many at once is no faster than one after another
        self._failures = asyncio.Lock()

    async def check_password(self, password: str) -> bool:
        if hmac.compare_digest(password.encode(), self._password):
            return True

        async with self._failures:
            await asyncio.sleep(FAILED_LOGIN_DELAY)
        return False

    def new_session(self) -> str:
        expires = int(time.time()) + SESSION_SECONDS
        return f"{expires}.{self._sign(expires)}"

    def valid_session(self, session: str | None) -> bool:
        if not session:
            return False

        expires, _, signature = session.partition(".")
        try:
            expires_at = int(expires)
        except ValueError:
            return False
        if expires_at < time.time():
            return False
        return hmac.compare_digest(signature.encode(), self._sign(expires_at).encode())

    def _sign(self, expires: int) -> str:
        return hmac.new(self._key, str(expires).encode(), hashlib.sha256).hexdigest()
