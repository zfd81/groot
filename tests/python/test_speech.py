"""对外语音合成接口系统测试（POST /audio/speech）。

运行前提：groot 服务已启动（见 conftest 的 server fixture）。
环境变量：
  GROOT_TTS_MODEL  一个已在设置中创建且能合成语音的模型名；不设置时跳过成功用例。

用例点：
- 未携带 API Key（401 unauthorized）
- 非法 JSON（400 invalid_request）
- input 为空白（400 invalid_request）
- 不支持的 response_format（400 invalid_request）
- 显式指定不存在的模型，不回落默认 TTS 模型（400 invalid_model）
- 正常合成 mp3（需 GROOT_TTS_MODEL）
"""
import os

import pytest
import requests

from conftest import BASE_URL

URL = f"{BASE_URL}/audio/speech"
TTS_MODEL = os.environ.get("GROOT_TTS_MODEL", "")


@pytest.fixture
def key_headers(api_key):
    """带 API Key 的 JSON 请求头"""
    return {"X-API-Key": api_key, "Content-Type": "application/json"}


class TestSpeechErrors:
    """错误路径，不依赖真实 TTS 模型"""

    def test_unauthorized(self, server):
        """TC-TTS-001: 未携带 API Key"""
        resp = requests.post(URL, json={"input": "hi"}, timeout=10)
        assert resp.status_code == 401
        assert resp.json()["status"] == "unauthorized"

    def test_bad_json(self, server, key_headers):
        """TC-TTS-002: 请求体不是合法 JSON"""
        resp = requests.post(URL, headers=key_headers, data="{not json", timeout=10)
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_request"

    def test_empty_input(self, server, key_headers):
        """TC-TTS-003: input 裁剪空白后为空"""
        resp = requests.post(URL, headers=key_headers, json={"input": "  "}, timeout=10)
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_request"

    def test_bad_response_format(self, server, key_headers):
        """TC-TTS-004: response_format 不在 mp3/opus/aac/flac/wav/pcm 之内"""
        resp = requests.post(
            URL, headers=key_headers,
            json={"input": "hi", "response_format": "ogg"}, timeout=10,
        )
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_request"

    def test_unknown_model_no_fallback(self, server, key_headers):
        """TC-TTS-005: 显式指定不存在的模型，不回落默认 TTS 模型"""
        resp = requests.post(
            URL, headers=key_headers,
            json={"input": "hi", "model": "__nope__"}, timeout=10,
        )
        assert resp.status_code == 400
        body = resp.json()
        assert body["status"] == "invalid_model"
        assert "__nope__" in body["message"]


@pytest.mark.skipif(not TTS_MODEL, reason="未设置 GROOT_TTS_MODEL")
class TestSpeechSuccess:
    """成功路径，需要真实的 TTS 模型"""

    def test_speech_mp3(self, server, key_headers):
        """TC-TTS-006: 用指定模型合成一段极短文本为 mp3"""
        resp = requests.post(
            URL, headers=key_headers,
            json={"input": "你好", "model": TTS_MODEL, "response_format": "mp3"},
            timeout=120,
        )
        assert resp.status_code == 200, resp.text
        assert resp.headers.get("Content-Type", "").startswith("audio/")
        assert len(resp.content) > 0
        assert resp.headers.get("X-Model-Name") == TTS_MODEL
