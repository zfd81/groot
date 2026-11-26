"""音频解码：输入字节 → 16kHz 单声道 float32 PCM。

优先用 soundfile 解码其支持的格式（wav/flac/ogg），失败时回退到 ffmpeg
（mp3/m4a/webm 等）。全程在内存中完成，不产生临时文件。
"""

from __future__ import annotations

import io
import math
import shutil
import subprocess

import numpy as np
import soundfile as sf
from scipy.signal import resample_poly

from groot_service_sdk import APIError

MAX_BYTES = 50 * 1024 * 1024
MIN_SECONDS = 1.5
MAX_SECONDS = 35.0
SAMPLE_RATE = 16000
# ffmpeg 最多解码到略超上限的位置：足够判定「超长」，又不会为一小时的 mp3 生成几百 MB PCM。
FFMPEG_DECODE_CAP_SECONDS = MAX_SECONDS + 1.0
FFMPEG_TIMEOUT_SECONDS = 30.0

_DECODE_FAILED = "音频解码失败，可能需要 ffmpeg"


def _check_duration(duration: float) -> None:
    if duration < MIN_SECONDS or duration > MAX_SECONDS:
        raise APIError(
            400,
            f"音频时长须在 {MIN_SECONDS} 到 {MAX_SECONDS} 秒之间，当前 {duration:.2f} 秒",
            type="invalid_request_error",
            code="invalid_request",
        )


def _decode_with_soundfile(data: bytes) -> tuple[np.ndarray, int]:
    """用 soundfile 解码，返回 (单声道 float32 数组, 原始采样率)。

    先读文件头取时长，超限时直接拒绝，不做全量解码。
    """
    info = sf.info(io.BytesIO(data))
    if info.frames > 0 and info.samplerate > 0:
        _check_duration(info.frames / info.samplerate)
    pcm, sample_rate = sf.read(io.BytesIO(data), dtype="float32", always_2d=True)
    if pcm.shape[1] > 1:
        pcm = pcm.mean(axis=1)
    else:
        pcm = pcm[:, 0]
    return pcm, int(sample_rate)


def _decode_with_ffmpeg(data: bytes) -> np.ndarray:
    """用 ffmpeg 解码为 16kHz 单声道 float32 PCM。

    只解码前 ``FFMPEG_DECODE_CAP_SECONDS`` 秒，并限制运行时间；超时视为无法解码。
    """
    try:
        proc = subprocess.run(
            [
                "ffmpeg",
                "-hide_banner",
                "-loglevel",
                "error",
                "-i",
                "pipe:0",
                "-t",
                str(FFMPEG_DECODE_CAP_SECONDS),
                "-f",
                "f32le",
                "-ac",
                "1",
                "-ar",
                str(SAMPLE_RATE),
                "pipe:1",
            ],
            input=data,
            capture_output=True,
            timeout=FFMPEG_TIMEOUT_SECONDS,
        )
    except subprocess.TimeoutExpired:  # subprocess.run 超时后已杀死子进程
        raise _unsupported() from None
    if proc.returncode != 0 or not proc.stdout:
        raise _unsupported()
    return np.frombuffer(proc.stdout, dtype="<f4").astype(np.float32)


def _unsupported() -> APIError:
    return APIError(
        400,
        _DECODE_FAILED,
        type="invalid_request_error",
        code="unsupported_type",
    )


def _resample(pcm: np.ndarray, src_rate: int) -> np.ndarray:
    """用 polyphase 滤波把 PCM 重采样到 16kHz。"""
    if src_rate == SAMPLE_RATE:
        return pcm
    divisor = math.gcd(src_rate, SAMPLE_RATE)
    up = SAMPLE_RATE // divisor
    down = src_rate // divisor
    return resample_poly(pcm, up, down).astype(np.float32, copy=False)


def check_size(size: int | None) -> None:
    """文件字节数超过 ``MAX_BYTES`` 时抛 413 ``file_too_large``；``None`` 表示未知，跳过。"""
    if size is not None and size > MAX_BYTES:
        raise APIError(
            413,
            f"文件大小超过上限 {MAX_BYTES // (1024 * 1024)}MB",
            type="invalid_request_error",
            code="file_too_large",
        )


def decode_to_pcm(data: bytes) -> tuple[np.ndarray, int]:
    """把任意受支持格式的音频字节解码为 (float32 单声道 PCM, 16000)。

    文件超过 50MB 抛 413 ``file_too_large``；时长不在 1.5–35 秒抛 400
    ``invalid_request``；无法解码抛 400 ``unsupported_type``。
    """
    check_size(len(data))

    try:
        pcm, sample_rate = _decode_with_soundfile(data)
    except APIError:
        raise
    except Exception:  # soundfile 对不认识的格式抛 RuntimeError/ValueError
        if shutil.which("ffmpeg") is None:
            raise _unsupported()
        pcm = _decode_with_ffmpeg(data)
        sample_rate = SAMPLE_RATE

    pcm = _resample(pcm, sample_rate)
    _check_duration(pcm.shape[0] / SAMPLE_RATE)

    return np.ascontiguousarray(pcm, dtype=np.float32), SAMPLE_RATE
