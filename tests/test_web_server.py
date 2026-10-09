"""Tests for the web server's record keeping. Run with `uv run python tests/test_web_server.py`.

Plain asserts and no test runner on purpose: Traffic Light has no test dependency and this is the
one place where a bug quietly throws away traffic the user asked to keep.
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path

# config.py reads config.toml from the working directory, so run from the repository root
os.chdir(Path(__file__).resolve().parent.parent)
sys.path.insert(0, str(Path.cwd()))
os.environ.setdefault("TRAFFICLIGHT_WEB_PASSWORD", "")

from trafficlight.config import config  # noqa: E402
from trafficlight.web.server import SAVE_LIMIT, WebServer, _records_message  # noqa: E402

PASSED = 0


def check(name: str, condition: bool, detail: str = "") -> None:
    global PASSED
    if condition:
        PASSED += 1
        print(f"  ok   {name}")
    else:
        print(f"  FAIL {name} {detail}")
        sys.exit(1)


class FakeProto:
    """proto_summary and proto_export only ever touch these"""

    method_name = "FORT_DETAILS"
    method_value = 104
    proxy = None

    class _Message:
        name = "FortDetailsProto"
        size = 0
        payload = None
        blackbox = None

        @staticmethod
        def decode_b64() -> bytes:
            return b""

        @staticmethod
        def to_text(one_line: bool = False) -> str:
            return ""

    request = _Message()
    response = _Message()


def server(max_records: int) -> WebServer:
    config.web_max_records = max_records
    return WebServer()


def add(web: WebServer, count: int = 1) -> None:
    for _ in range(count):
        web.add_record(rpc_id=1, rpc_status=1, protos=[FakeProto()])


def ids(web: WebServer) -> list[int]:
    return list(web._records)


def command(web: WebServer, **payload) -> None:
    web._handle_command(_Collector(), json.dumps(payload))


class _Collector:
    """stands in for a connected browser"""

    def __init__(self) -> None:
        self.messages: list[str] = []
        self.backlog = 0

    def send(self, message: str) -> None:
        self.messages.append(message)

    def close(self) -> None:
        pass


# ---------------------------------------------------------------- eviction


def test_evicts_oldest_when_nothing_is_saved() -> None:
    web = server(3)
    add(web, 5)
    check("keeps the cap", len(web._records) == 3, ids(web))
    check("drops the oldest first", ids(web) == [3, 4, 5], ids(web))


def test_saved_records_survive_eviction() -> None:
    web = server(3)
    add(web, 3)
    command(web, type="save", id=1, value=True)
    add(web, 3)
    check("a saved record is kept past the cap", 1 in web._records, ids(web))
    check("still at the cap", len(web._records) == 3, ids(web))
    check("the oldest unsaved went instead", ids(web) == [1, 5, 6], ids(web))


def test_add_record_reports_what_it_dropped() -> None:
    web = server(2)
    add(web, 2)
    client = _Collector()
    web._clients.add(client)
    add(web, 1)
    message = json.loads(client.messages[-1])
    check("the browser is told what went", message.get("dropped") == [1], message)


def test_saving_never_pushes_the_log_over_the_cap() -> None:
    web = server(2)
    add(web, 2)
    command(web, type="save", id=1, value=True)
    # the second one is refused: at most half of a two record log can be saved
    command(web, type="save", id=2, value=True)
    check("the save limit leaves room to breathe", web._saved == {1}, web._saved)

    add(web, 2)
    check("still at the cap", ids(web) == [1, 4], ids(web))
    command(web, type="save", id=1, value=False)
    add(web, 1)
    check("unsaved, it goes like any other", ids(web) == [4, 5], ids(web))


def test_save_limit() -> None:
    web = server(1000)
    add(web, SAVE_LIMIT + 5)
    for record_id in range(1, SAVE_LIMIT + 2):
        command(web, type="save", id=record_id, value=True)
    check("refuses past the limit", len(web._saved) == SAVE_LIMIT, len(web._saved))

    client = _Collector()
    web._handle_command(client, json.dumps({"type": "save", "id": SAVE_LIMIT + 3, "value": True}))
    notices = [json.loads(m) for m in client.messages if json.loads(m).get("type") == "notice"]
    check("and says why", len(notices) == 1 and "save" in notices[0]["text"].lower(), client.messages)


def test_save_limit_follows_a_small_log() -> None:
    web = server(10)
    add(web, 10)
    for record_id in range(1, 11):
        command(web, type="save", id=record_id, value=True)
    check("never saves more than half a small log", len(web._saved) == 5, len(web._saved))


def test_unknown_save_ids_are_ignored() -> None:
    web = server(5)
    add(web, 2)
    command(web, type="save", id=99, value=True)
    command(web, type="save", id="nonsense", value=True)
    command(web, type="save", value=True)
    check("ignores ids that are not in the log", web._saved == set(), web._saved)


# ---------------------------------------------------------------- clear


def test_clear_keeps_saved_records() -> None:
    web = server(10)
    add(web, 4)
    command(web, type="save", id=2, value=True)
    client = _Collector()
    web._clients.add(client)
    command(web, type="clear")
    check("only the saved one is left", ids(web) == [2], ids(web))

    kinds = [json.loads(m)["type"] for m in client.messages]
    check("the survivors ride on the clear, not on a history after it", kinds == ["clear", "saved"], kinds)
    message = json.loads(client.messages[0])
    check("and they are all there", [r["id"] for r in message["records"]] == [2], message)


def test_clear_with_saved_empties_everything() -> None:
    web = server(10)
    add(web, 3)
    command(web, type="save", id=2, value=True)
    client = _Collector()
    web._clients.add(client)
    command(web, type="clear", saved=True)
    check("nothing is left", ids(web) == [] and web._saved == set(), (ids(web), web._saved))

    pins = [json.loads(m) for m in client.messages if json.loads(m).get("type") == "saved"]
    check("and browsers are told they went", pins and pins[-1]["ids"] == [], client.messages)


def test_record_ids_never_restart() -> None:
    web = server(10)
    add(web, 3)
    command(web, type="clear", saved=True)
    add(web, 1)
    check("ids keep counting, so cached details stay valid", ids(web) == [4], ids(web))


def test_saved_set_is_pruned() -> None:
    web = server(2)
    add(web, 2)
    command(web, type="save", id=1, value=True)
    command(web, type="clear", saved=True)
    check("clearing forgets them", web._saved == set(), web._saved)


# ---------------------------------------------------------------- the rest of the protocol


def test_pause_blocks_records() -> None:
    web = server(10)
    command(web, type="pause", value=True)
    add(web, 3)
    check("paused drops incoming traffic", ids(web) == [], ids(web))
    command(web, type="pause", value=False)
    add(web, 1)
    check("resuming takes it again", len(ids(web)) == 1, ids(web))


def test_hello_carries_the_saved_ids() -> None:
    web = server(10)
    add(web, 2)
    command(web, type="save", id=2, value=True)
    hello = json.loads(web._hello())
    check("hello lists the saved ids", hello["saved"] == [2], hello)


def test_history_is_chunked() -> None:
    web = server(1200)
    add(web, 1200)
    chunks = list(web._history())
    check("history comes in chunks", len(chunks) == 3, len(chunks))
    total = sum(len(json.loads(c)["records"]) for c in chunks)
    check("without losing any", total == 1200, total)


def test_records_message_shape() -> None:
    plain = json.loads(_records_message(['{"id": 1}']))
    check("no dropped key when nothing went", "dropped" not in plain, plain)
    with_dropped = json.loads(_records_message(['{"id": 2}'], dropped=[1]))
    check("dropped ids ride along", with_dropped["dropped"] == [1], with_dropped)
    empty = json.loads(_records_message([], message_type="clear"))
    check("an empty list is still valid json", empty == {"type": "clear", "records": []}, empty)


if __name__ == "__main__":
    for name, test in sorted(globals().items()):
        if name.startswith("test_"):
            print(name)
            test()
    print(f"\n{PASSED} checks passed")
