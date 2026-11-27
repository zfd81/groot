"""测试插件 plugin_ok：返回调用方身份与 ok，并记录启动、停止调用。"""

import os

from fastapi import Depends

from groot_plugin_sdk import GrootIdentity, groot_identity

METHODS = ["POST"]


def _record(event: str) -> None:
    path = os.environ.get("GROOT_TEST_EVENTS")
    if path:
        with open(path, "a", encoding="utf-8") as fh:
            fh.write(event + "\n")


async def startup() -> None:
    _record("startup:plugin_ok")


async def shutdown() -> None:
    _record("shutdown:plugin_ok")


async def handle(identity: GrootIdentity = Depends(groot_identity)) -> dict:
    return {
        "ok": True,
        "caller": identity.caller,
        "tenant_id": identity.tenant_id,
        "user_id": identity.user_id,
        "plugin": identity.plugin,
    }
