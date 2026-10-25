"""语音合成设置 API 系统测试（/web/settings/speech）。

speech.voice / speech.instructions 是 Web 界面朗读回复时
透传给 TTS 模型的参数，空串表示不下发、由模型取默认行为。
取值由各 TTS 模型自行定义，服务端只裁剪空白并限制长度（64 / 500），不校验是否存在。

运行前提：groot 服务已启动，且已完成 Web 用户初始化（POST /web/setup）。
环境变量：
  GROOT_TEST_HOST / GROOT_TEST_PORT  服务地址（默认 localhost:8080，见 conftest）
  GROOT_WEB_USER / GROOT_WEB_PASS    Web 登录凭据

用例点：
- GET 响应体恰为 voice / instructions 两个键
- PUT 保存后回读，首尾空白被裁剪
- PUT voice 超过 64 字符返回 400 invalid_request
- PUT instructions 超过 500 字符返回 400 invalid_request
- PUT 带已废弃的 speaker / language / instruct 字段被忽略，GET 不返回这些键
- PUT 非法 JSON 返回 400 invalid_request
- 未登录访问返回 401
"""
import os
import warnings

import pytest
import requests

from conftest import BASE_URL, TEST_WEB_PASS, TEST_WEB_USER

WEB_USER = os.environ.get("GROOT_WEB_USER", TEST_WEB_USER)
WEB_PASS = os.environ.get("GROOT_WEB_PASS", TEST_WEB_PASS)

SPEECH_URL = f"{BASE_URL}/web/settings/speech"
SPEECH_KEYS = {"voice", "instructions"}


@pytest.fixture(scope="module")
def web():
    """已登录的 Web 会话（Cookie 认证）；登录失败时跳过整个模块"""
    s = requests.Session()
    try:
        resp = s.post(f"{BASE_URL}/web/login", json={
            "username": WEB_USER,
            "password": WEB_PASS,
        }, timeout=10)
    except requests.RequestException as e:
        pytest.skip(f"groot 服务不可达: {e}")
    if resp.status_code != 200:
        pytest.skip(f"Web 登录失败（请设置 GROOT_WEB_USER / GROOT_WEB_PASS）: {resp.text}")
    yield s


@pytest.fixture()
def original(web):
    """记录测试前的语音合成配置，测试结束后恢复"""
    resp = web.get(SPEECH_URL, timeout=10)
    assert resp.status_code == 200, resp.text
    saved = resp.json()
    yield saved
    resp = web.put(SPEECH_URL, json=saved, timeout=10)
    if resp.status_code != 200:
        warnings.warn(f"恢复语音合成配置失败: {resp.status_code} {resp.text}")


class TestSpeechSettings:
    def test_get_fields(self, web):
        resp = web.get(SPEECH_URL, timeout=10)
        assert resp.status_code == 200, resp.text
        body = resp.json()
        assert set(body.keys()) == SPEECH_KEYS
        for k in SPEECH_KEYS:
            assert isinstance(body[k], str)

    def test_put_trims_fields(self, web, original):
        """保存时裁剪首尾空白，回读为裁剪后的值"""
        resp = web.put(SPEECH_URL, json={
            "voice": "  alloy  ",
            "instructions": " 用轻快的语气朗读 ",
        }, timeout=10)
        assert resp.status_code == 200, resp.text
        assert resp.json()["status"] == "ok"

        got = web.get(SPEECH_URL, timeout=10).json()
        assert set(got.keys()) == SPEECH_KEYS
        assert got["voice"] == "alloy"
        assert got["instructions"] == "用轻快的语气朗读"

    def test_voice_too_long(self, web, original):
        """超过 64 字符拒绝，且不写入"""
        resp = web.put(SPEECH_URL, json={"voice": "a" * 65}, timeout=10)
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_request"

        got = web.get(SPEECH_URL, timeout=10).json()
        assert got["voice"] == original["voice"]

    def test_instructions_too_long(self, web, original):
        """instructions 超过 500 字符拒绝，且不写入"""
        resp = web.put(SPEECH_URL, json={"instructions": "a" * 501}, timeout=10)
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_request"

        got = web.get(SPEECH_URL, timeout=10).json()
        assert got["instructions"] == original["instructions"]

    def test_legacy_fields_ignored(self, web, original):
        """已废弃的 speaker / language / instruct 字段被静默忽略，GET 不回显这些键"""
        resp = web.put(SPEECH_URL, json={
            "voice": "alloy",
            "speaker": "vivian",
            "language": "zh",
            "instruct": "旧",
            "instructions": "",
        }, timeout=10)
        assert resp.status_code == 200, resp.text

        got = web.get(SPEECH_URL, timeout=10).json()
        assert set(got.keys()) == SPEECH_KEYS
        assert got["voice"] == "alloy"

    def test_bad_json(self, web):
        resp = web.put(SPEECH_URL, data="{not json",
                       headers={"Content-Type": "application/json"}, timeout=10)
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_request"

    def test_unauthorized(self):
        resp = requests.get(SPEECH_URL, timeout=10)
        assert resp.status_code == 401
