"""测试插件 plugin_b：具有必填查询参数，可按参数抛出 APIError，并记录启动、停止调用。"""

import os

from fastapi import Query

from groot_plugin_sdk import APIError

from .helper import is_boom

METHODS = ["POST"]


def _record(event: str) -> None:
    path = os.environ.get("GROOT_TEST_EVENTS")
    if path:
        with open(path, "a", encoding="utf-8") as fh:
            fh.write(event + "\n")


async def startup() -> None:
    _record("startup:plugin_b")


async def shutdown() -> None:
    _record("shutdown:plugin_b")


async def handle(kind: str = Query(...)) -> dict:
    if is_boom(kind):
        raise APIError(400, "不支持的音频格式", type="invalid_request_error", code="unsupported_type")
    return {"ok": True}
