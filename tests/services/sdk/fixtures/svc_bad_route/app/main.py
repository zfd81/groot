"""测试子服务 svc_bad_route：导入成功，但 handle 签名在 FastAPI 注册路由时报错。"""

METHODS = ["POST"]


class NotAModel:
    """既不是 Pydantic 模型也不是可解析的参数类型。"""


async def handle(payload: NotAModel) -> dict:
    return {"ok": True}
