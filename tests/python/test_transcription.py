"""对外音频转录接口系统测试（POST /audio/transcriptions）。

运行前提：groot 服务已启动（见 conftest 的 server fixture）。
环境变量：
  GROOT_VOICE_MODEL  一个已在设置中创建且能转录的模型名；不设置时跳过成功用例。

用例点：
- 缺少 file 字段（400 invalid_request）
- 不支持的扩展名（400 unsupported_type）
- 指定不存在的模型（400 invalid_model）
- 超过附件单文件上限（400 file_too_large）
- 未携带 API Key（401 unauthorized）
- 正常转录（需 GROOT_VOICE_MODEL）
"""
import os

import pytest
import requests

from conftest import BASE_URL

URL = f"{BASE_URL}/audio/transcriptions"
VOICE_MODEL = os.environ.get("GROOT_VOICE_MODEL", "")


@pytest.fixture
def key_headers(api_key):
    """只带 API Key，不带 Content-Type：multipart 边界由 requests 自动生成"""
    return {"X-API-Key": api_key}


def _audio(name="rec.webm", size=16):
    return {"file": (name, b"\x00" * size, "audio/webm")}


class TestTranscriptionErrors:
    """错误路径，不依赖真实语音模型"""

    def test_missing_file(self, server, key_headers):
        """TC-ASR-001: 缺少 file 字段"""
        resp = requests.post(URL, headers=key_headers, data={"model": "x"}, timeout=10)
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_request"

    def test_unsupported_extension(self, server, key_headers):
        """TC-ASR-002: 扩展名不在音频白名单"""
        resp = requests.post(URL, headers=key_headers, files=_audio("notes.txt"), timeout=10)
        assert resp.status_code == 400
        assert resp.json()["status"] == "unsupported_type"

    def test_unknown_model(self, server, key_headers):
        """TC-ASR-003: 指定不存在的模型"""
        resp = requests.post(
            URL, headers=key_headers,
            files=_audio(), data={"model": "__no_such_model__"}, timeout=10,
        )
        assert resp.status_code == 400
        body = resp.json()
        assert body["status"] == "invalid_model"
        assert "__no_such_model__" in body["message"]

    def test_file_too_large(self, server, key_headers):
        """TC-ASR-004: 超过附件单文件上限（大小校验先于模型校验）"""
        big = {"file": ("big.webm", b"\x00" * (51 * 1024 * 1024), "audio/webm")}
        resp = requests.post(
            URL, headers=key_headers,
            files=big, data={"model": "__no_such_model__"}, timeout=60,
        )
        # 大小校验先于模型校验，因此这里应命中 file_too_large 而非 invalid_model
        assert resp.status_code == 400
        assert resp.json()["status"] == "file_too_large"

    def test_no_auth(self, server):
        """TC-ASR-005: 未携带 API Key"""
        resp = requests.post(URL, files=_audio(), timeout=10)
        assert resp.status_code == 401
        assert resp.json()["status"] == "unauthorized"


@pytest.mark.skipif(not VOICE_MODEL, reason="未设置 GROOT_VOICE_MODEL")
class TestTranscriptionSuccess:
    """成功路径，需要真实的语音模型"""

    def test_transcribe_wav(self, server, key_headers):
        """TC-ASR-006: 用指定模型转录一段极短的静音 wav"""
        # 44 字节的 PCM WAV 头 + 少量静音采样，足以让接口走通
        header = (
            b"RIFF" + (36 + 320).to_bytes(4, "little") + b"WAVE"
            b"fmt " + (16).to_bytes(4, "little") + (1).to_bytes(2, "little")
            + (1).to_bytes(2, "little") + (16000).to_bytes(4, "little")
            + (32000).to_bytes(4, "little") + (2).to_bytes(2, "little")
            + (16).to_bytes(2, "little")
            + b"data" + (320).to_bytes(4, "little")
        )
        wav = header + b"\x00" * 320
        resp = requests.post(
            URL, headers=key_headers,
            files={"file": ("silence.wav", wav, "audio/wav")},
            data={"model": VOICE_MODEL},
            timeout=120,
        )
        # 静音可能被上游判为无内容（400）或返回空串/噪声文本（200），两者都算接口打通
        assert resp.status_code in (200, 400), resp.text
        if resp.status_code == 200:
            body = resp.json()
            assert "text" in body
            assert body["model"] == VOICE_MODEL
