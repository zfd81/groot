"""asr-router 音频解码单元测试。

全部用 soundfile 在内存中生成 WAV 字节，不依赖 ffmpeg。
"""

import io

import numpy as np
import pytest

pytest.importorskip("numpy")
pytest.importorskip("scipy")
pytest.importorskip("soundfile")

import soundfile as sf

from groot_plugin_sdk import APIError

from app.audio import MAX_BYTES, decode_to_pcm


def _wav_bytes(seconds: float, sample_rate: int) -> bytes:
    """生成指定时长与采样率的单声道正弦波 WAV 字节。"""
    t = np.arange(int(seconds * sample_rate)) / sample_rate
    mono = (0.5 * np.sin(2 * np.pi * 440.0 * t)).astype(np.float32)
    buf = io.BytesIO()
    sf.write(buf, mono, sample_rate, format="WAV", subtype="PCM_16")
    return buf.getvalue()


def test_decode_wav_16k():
    pcm, sample_rate = decode_to_pcm(_wav_bytes(2.0, 16000))

    assert sample_rate == 16000
    assert pcm.dtype == np.float32
    assert pcm.ndim == 1
    assert abs(pcm.shape[0] - 32000) <= 1


def test_decode_resamples_8k():
    pcm, sample_rate = decode_to_pcm(_wav_bytes(2.0, 8000))

    assert sample_rate == 16000
    assert abs(pcm.shape[0] - 32000) <= 1


def test_rejects_short():
    with pytest.raises(APIError) as exc_info:
        decode_to_pcm(_wav_bytes(0.5, 16000))

    assert exc_info.value.status_code == 400
    assert exc_info.value.code == "invalid_request"
    assert "1.5" in exc_info.value.message
    assert "35" in exc_info.value.message


def test_rejects_long():
    with pytest.raises(APIError) as exc_info:
        decode_to_pcm(_wav_bytes(40.0, 16000))

    assert exc_info.value.status_code == 400
    assert exc_info.value.code == "invalid_request"
    assert "1.5" in exc_info.value.message
    assert "35" in exc_info.value.message


def test_rejects_oversize():
    with pytest.raises(APIError) as exc_info:
        decode_to_pcm(b"x" * (MAX_BYTES + 1))

    assert exc_info.value.status_code == 413
    assert exc_info.value.code == "file_too_large"


def test_invalid_audio():
    with pytest.raises(APIError) as exc_info:
        decode_to_pcm(b"\x00\x01\x02\x03" * 256)

    assert exc_info.value.status_code == 400
    assert exc_info.value.code == "unsupported_type"


def test_rejects_long_without_full_decode(monkeypatch):
    """超长音频应在完整解码之前凭文件头信息拒绝，不做全量解码与重采样。"""
    import app.audio as audio

    calls = []
    original = sf.read

    def _spy_read(*args, **kwargs):
        calls.append(args)
        return original(*args, **kwargs)

    monkeypatch.setattr(audio.sf, "read", _spy_read)

    with pytest.raises(APIError) as exc_info:
        decode_to_pcm(_wav_bytes(40.0, 16000))

    assert exc_info.value.code == "invalid_request"
    assert calls == []


def test_ffmpeg_invoked_with_duration_cap_and_timeout(monkeypatch):
    """ffmpeg 回退路径必须限制解码时长并设置超时，防止长文件或卡死占满资源。"""
    import app.audio as audio

    captured = {}

    class _Proc:
        returncode = 0
        stdout = np.zeros(int(2.0 * 16000), dtype="<f4").tobytes()
        stderr = b""

    def _fake_run(cmd, **kwargs):
        captured["cmd"] = cmd
        captured["kwargs"] = kwargs
        return _Proc()

    monkeypatch.setattr(audio.shutil, "which", lambda _name: "/usr/bin/ffmpeg")
    monkeypatch.setattr(audio.subprocess, "run", _fake_run)

    pcm, _ = decode_to_pcm(b"not-a-wav" * 100)

    assert pcm.shape[0] == 32000
    cmd = captured["cmd"]
    assert "-t" in cmd
    cap = float(cmd[cmd.index("-t") + 1])
    assert audio.MAX_SECONDS < cap <= audio.MAX_SECONDS + 5
    assert captured["kwargs"].get("timeout", 0) > 0


def test_ffmpeg_timeout_maps_to_api_error(monkeypatch):
    """ffmpeg 超时转为 400 unsupported_type 信封，而不是 500。"""
    import subprocess

    import app.audio as audio

    def _timeout(cmd, **kwargs):
        raise subprocess.TimeoutExpired(cmd, kwargs.get("timeout", 0))

    monkeypatch.setattr(audio.shutil, "which", lambda _name: "/usr/bin/ffmpeg")
    monkeypatch.setattr(audio.subprocess, "run", _timeout)

    with pytest.raises(APIError) as exc_info:
        decode_to_pcm(b"not-a-wav" * 100)

    assert exc_info.value.status_code == 400
    assert exc_info.value.code == "unsupported_type"
