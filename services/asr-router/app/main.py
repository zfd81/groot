"""asr-router 子服务入口：语种识别 HTTP 接口。

模块在 ``startup`` 中加载一次后端，之后由 ``handle`` 复用；后端未就绪或加载
失败时所有请求返回 503 信封。后端选择由 :func:`create_backend` 负责，本模块
只依赖 ``LIDBackend`` 协议。
"""

from __future__ import annotations

import asyncio
import logging
from pathlib import Path

from fastapi import Depends, File, UploadFile

from groot_service_sdk import APIError, GrootIdentity, groot_identity

from .audio import SAMPLE_RATE, check_size, decode_to_pcm
from .lid import create_backend

log = logging.getLogger(__name__)

METHODS = ["POST"]

# 支持的音频扩展名；非音频扩展名在读取请求体前直接拒绝，避免无谓的解码开销。
SUPPORTED_SUFFIXES = frozenset({".wav", ".mp3", ".m4a", ".flac", ".ogg", ".webm"})

_backend = None
_ready = False


def _extension_supported(filename: str | None) -> bool:
    """报告文件名是否带受支持的音频扩展名；缺失文件名视为不支持。"""
    if not filename:
        return False
    return Path(filename).suffix.lower() in SUPPORTED_SUFFIXES


async def startup() -> None:
    """加载语种识别后端；只有加载成功后才标记就绪。"""
    global _backend, _ready
    backend = create_backend()
    backend.load()
    _backend = backend
    _ready = True


async def shutdown() -> None:
    """清空并停止后端；关闭失败只记录日志，不影响其他子服务。"""
    global _backend, _ready
    backend = _backend
    _backend = None
    _ready = False
    if backend is None:
        return
    close = getattr(backend, "close", None)
    if close is None:
        return
    try:
        close()
    except Exception:  # noqa: BLE001 - 停止失败只记日志
        log.exception("asr-router 后端关闭失败")


async def handle(
    file: UploadFile = File(...),
    identity: GrootIdentity = Depends(groot_identity),
) -> dict:
    """接收音频文件，返回首要语种、可选方言、置信度与次选候选。

    身份由 SDK 注入，业务不做校验；``identity`` 仅为保留调用方上下文。
    """
    backend = _backend
    if not _ready or backend is None:
        raise APIError(
            503,
            "子服务未就绪",
            type="api_error",
            code="service_unavailable",
        )

    if not _extension_supported(file.filename):
        raise APIError(
            400,
            "不支持的音频格式，支持 wav、mp3、m4a、flac、ogg、webm",
            type="invalid_request_error",
            code="unsupported_type",
        )

    # 先按声明的大小拒绝，避免把超限文件整个读进内存。
    check_size(file.size)
    data = await file.read()
    pcm, _ = decode_to_pcm(data)
    results = await asyncio.to_thread(backend.detect, pcm)

    if results:
        language, dialect, confidence = results[0]
    else:
        language, dialect, confidence = "und", None, 0.0

    response: dict = {"language": language}
    if dialect:
        response["dialect"] = dialect
    response["confidence"] = confidence
    response["alternatives"] = [
        {"language": alt_language, "confidence": alt_confidence}
        for alt_language, _alt_dialect, alt_confidence in results[1:3]
    ]
    response["duration_ms"] = round(len(pcm) / SAMPLE_RATE * 1000)
    return response
