"""身份依赖：由 Groot 主服务注入的调用方身份。"""

from dataclasses import dataclass

from fastapi import Request


@dataclass(frozen=True)
class GrootIdentity:
    """当前请求的调用方身份；缺失的头以空字符串表示。"""

    caller: str
    tenant_id: str
    user_id: str
    request_id: str
    service: str


async def groot_identity(request: Request) -> GrootIdentity:
    """FastAPI 依赖：从 ``X-Groot-*`` 请求头读取调用方身份。"""
    headers = request.headers
    return GrootIdentity(
        caller=headers.get("x-groot-caller", ""),
        tenant_id=headers.get("x-groot-tenant-id", ""),
        user_id=headers.get("x-groot-user-id", ""),
        request_id=headers.get("x-groot-request-id", ""),
        service=headers.get("x-groot-service", ""),
    )
