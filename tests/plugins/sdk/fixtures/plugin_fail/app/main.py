"""测试插件 plugin_fail：startup 抛异常，用于验证失败隔离。"""


async def startup() -> None:
    raise RuntimeError("模型加载失败")


async def handle() -> dict:
    return {"ok": True}
