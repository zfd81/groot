"""语音设置 API 系统测试（/web/settings/voice）。

识别模型 voice.model 是 Web 界面语音输入使用的模型，空串表示不启用语音输入。
本文件只验证接口契约，不依赖服务端是否已配置默认语音模型。

运行前提：groot 服务已启动，且已完成 Web 用户初始化（POST /web/setup）。
环境变量：
  GROOT_TEST_HOST / GROOT_TEST_PORT  服务地址（默认 localhost:8080，见 conftest）
  GROOT_WEB_USER / GROOT_WEB_PASS    Web 登录凭据

用例点：
- GET 响应体只含 model、auto_send
- PUT 清空 model 保存成功，回读为空串
- PUT 不存在的模型返回 400 invalid_model，消息含模型名
- PUT 非法 JSON 返回 400 invalid_request
- 未登录访问返回 401

副作用：服务端从未保存过语音配置时，清空用例会写入 voice.model 空串行，
之后不再从默认语音模型自动填充，且无法经 API 恢复。
"""
import os
import uuid
import warnings

import pytest
import requests

from conftest import BASE_URL, TEST_WEB_PASS, TEST_WEB_USER

WEB_USER = os.environ.get("GROOT_WEB_USER", TEST_WEB_USER)
WEB_PASS = os.environ.get("GROOT_WEB_PASS", TEST_WEB_PASS)

VOICE_URL = f"{BASE_URL}/web/settings/voice"


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
    """记录测试前的语音配置，测试结束后恢复"""
    resp = web.get(VOICE_URL, timeout=10)
    assert resp.status_code == 200, resp.text
    saved = resp.json()
    yield saved
    resp = web.put(VOICE_URL, json=saved, timeout=10)
    if resp.status_code != 200:
        warnings.warn(f"恢复语音配置失败: {resp.status_code} {resp.text}")


class TestVoiceSettings:
    def test_get_fields(self, web):
        resp = web.get(VOICE_URL, timeout=10)
        assert resp.status_code == 200, resp.text
        body = resp.json()
        assert set(body.keys()) == {"model", "auto_send"}
        assert isinstance(body["model"], str)
        assert isinstance(body["auto_send"], bool)

    def test_clear_model(self, web, original):
        """清空 model 保存成功，回读为空串。

        已知副作用：若服务端从未保存过语音配置，本用例会写入 voice.model 空串行，之后设置默认语音模型也不再自动填充；该状态无法经 API 恢复。
        """
        resp = web.put(VOICE_URL, json={"model": "", "auto_send": False}, timeout=10)
        assert resp.status_code == 200, resp.text

        got = web.get(VOICE_URL, timeout=10).json()
        assert set(got.keys()) == {"model", "auto_send"}
        # 清空后已写入 voice.model 行，不会再被默认语音模型自动填充
        assert got["model"] == ""
        assert got["auto_send"] is False

    def test_unknown_model(self, web):
        name = f"nope-{uuid.uuid4().hex[:8]}"
        resp = web.put(VOICE_URL, json={"model": name, "auto_send": False}, timeout=10)
        assert resp.status_code == 400
        body = resp.json()
        assert body["status"] == "invalid_model"
        assert name in body["message"]

    def test_bad_json(self, web):
        resp = web.put(VOICE_URL, data="{not json",
                       headers={"Content-Type": "application/json"}, timeout=10)
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_request"

    def test_unauthorized(self):
        resp = requests.get(VOICE_URL, timeout=10)
        assert resp.status_code == 401
