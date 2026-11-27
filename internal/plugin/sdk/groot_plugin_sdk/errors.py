"""错误信封：与 Groot 宿主一致的 OpenAI 风格错误返回。"""

from __future__ import annotations


class APIError(Exception):
    """带状态码与 OpenAI 信封元数据的业务异常。"""

    def __init__(
        self,
        status_code: int,
        message: str,
        type: str = "api_error",
        param: str | None = None,
        code: str | None = None,
    ) -> None:
        super().__init__(message)
        self.status_code = status_code
        self.message = message
        self.type = type
        self.param = param
        self.code = code


def error_envelope(exc: APIError) -> dict:
    """将 :class:`APIError` 转换为 OpenAI 风格的错误信封。"""
    return {
        "error": {
            "message": exc.message,
            "type": exc.type,
            "param": exc.param,
            "code": exc.code,
        }
    }
