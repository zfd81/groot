"""groot_plugin_sdk：Groot Python 插件协议库。"""

from .errors import APIError, error_envelope
from .identity import GrootIdentity, groot_identity

# SDK 版本；随 Groot 二进制发布，就绪行中的 sdk 字段用于宿主核对版本。
SDK_VERSION = "1.0.0"

__all__ = [
    "APIError",
    "error_envelope",
    "GrootIdentity",
    "groot_identity",
    "SDK_VERSION",
]
