"""对外图片解析接口系统测试（POST /image/understanding）。

运行前提：groot 服务已启动（见 conftest 的 server fixture）。
环境变量：
  GROOT_VISION_MODEL  一个已在设置中创建且能解析图片的视觉模型名；不设置时跳过成功用例。
  GROOT_WEB_USER / GROOT_WEB_PASS  Web 登录凭据（改默认视觉模型的用例需要）

用例点：
- 未携带 API Key（401 unauthorized）
- 非法 JSON（400 invalid_request）
- attachments 缺失或超过 1 张（400 invalid_request）
- type 不是 image（400 attachment_invalid_type）
- content 不是合法 Base64（400 attachment_decode_error）
- 纯文本内容改名 .png（400 unsupported_type）
- 显式指定不存在的模型，不回落默认视觉模型（400 invalid_model）
- 仅含 status 权限的 Key 调用（403 forbidden）；仅含 chat 权限的 Key 放行
- 未配置默认视觉模型（400 invalid_model，不回落其它类型默认）
- 正常解析 1x1 PNG（需 GROOT_VISION_MODEL）
"""
import os
import sys
import uuid

import pytest
import requests

from conftest import (
    BASE_URL,
    TEST_USER_ID,
    TEST_WEB_PASS,
    TEST_WEB_USER,
    bootstrap_api_key,
)

URL = f"{BASE_URL}/image/understanding"
VISION_MODEL = os.environ.get("GROOT_VISION_MODEL", "")
WEB_USER = os.environ.get("GROOT_WEB_USER", TEST_WEB_USER)
WEB_PASS = os.environ.get("GROOT_WEB_PASS", TEST_WEB_PASS)
PNG_BASE64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"


@pytest.fixture
def key_headers(api_key):
    """带 API Key 的 JSON 请求头"""
    return {"X-API-Key": api_key, "Content-Type": "application/json"}


@pytest.fixture(scope="module")
def web():
    """已登录的 Web 会话（Cookie 认证）；登录失败时跳过"""
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


class TestImageUnderstandingErrors:
    """错误路径，不依赖真实视觉模型"""

    def test_unauthorized(self, server):
        """TC-IMG-001: 未携带 API Key"""
        resp = requests.post(URL, json={"attachments": []}, timeout=10)
        assert resp.status_code == 401
        assert resp.json()["status"] == "unauthorized"

    def test_bad_json(self, server, key_headers):
        """TC-IMG-002: 请求体不是合法 JSON"""
        resp = requests.post(URL, headers=key_headers, data="{not json", timeout=10)
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_request"

    def test_bad_attachment_count(self, server, key_headers):
        """TC-IMG-003: attachments 缺失或超过 1 张"""
        resp = requests.post(URL, headers=key_headers, json={}, timeout=10)
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_request"

        resp = requests.post(
            URL, headers=key_headers,
            json={"attachments": [
                {"type": "image", "name": "a.png", "content": PNG_BASE64},
                {"type": "image", "name": "b.png", "content": PNG_BASE64},
            ]}, timeout=10,
        )
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_request"

    def test_non_image_type(self, server, key_headers):
        """TC-IMG-004: 附件 type 不是 image"""
        resp = requests.post(
            URL, headers=key_headers,
            json={"attachments": [{"type": "file", "name": "a.png", "content": PNG_BASE64}]},
            timeout=10,
        )
        assert resp.status_code == 400
        assert resp.json()["status"] == "attachment_invalid_type"

    def test_bad_base64(self, server, key_headers):
        """TC-IMG-005: content 不是合法 Base64"""
        resp = requests.post(
            URL, headers=key_headers,
            json={"attachments": [{"type": "image", "name": "a.png", "content": "invalid_base64!!!"}]},
            timeout=10,
        )
        assert resp.status_code == 400
        assert resp.json()["status"] == "attachment_decode_error"

    def test_text_renamed_png(self, server, key_headers):
        """TC-IMG-006: 纯文本内容改名 .png，文件头嗅探出非图片类型"""
        resp = requests.post(
            URL, headers=key_headers,
            json={"attachments": [{"type": "image", "name": "a.png", "content": "aGVsbG8gd29ybGQ="}]},
            timeout=10,
        )
        assert resp.status_code == 400
        assert resp.json()["status"] == "unsupported_type"

    def test_unknown_model_no_fallback(self, server, key_headers):
        """TC-IMG-007: 显式指定不存在的模型，不回落默认视觉模型"""
        resp = requests.post(
            URL,
            headers={**key_headers, "X-Model-Name": "__nope__"},
            json={"attachments": [{"type": "image", "name": "a.png", "content": PNG_BASE64}]},
            timeout=10,
        )
        assert resp.status_code == 400
        body = resp.json()
        assert body["status"] == "invalid_model"
        assert "__nope__" in body["message"]

    def test_forbidden_without_chat_permission(self, server):
        """TC-IMG-009: 仅含 status 权限的 Key 调用 → 403；含 chat 权限的 Key → 不被鉴权拦截"""
        token = bootstrap_api_key(BASE_URL, name="pytest-status-only-img", permissions=["status"])
        headers = {"Content-Type": "application/json", "X-API-Key": token, "X-User-ID": TEST_USER_ID}
        resp = requests.post(
            URL, headers=headers,
            json={"attachments": [{"type": "image", "name": "a.png", "content": PNG_BASE64}]},
            timeout=10,
        )
        assert resp.status_code == 403
        assert "forbidden" in resp.text

        # 正例控制项：仅含 chat 权限的 Key 能通过鉴权（绝不能是 401/403）。
        # 显式指定一个不存在的模型，让请求在模型解析阶段就返回 400，避免真的打到上游视觉模型。
        token = bootstrap_api_key(BASE_URL, name="pytest-chat-only-img", permissions=["chat"])
        headers = {
            "Content-Type": "application/json",
            "X-API-Key": token,
            "X-User-ID": TEST_USER_ID,
            "X-Model-Name": "__nope__",
        }
        resp = requests.post(
            URL, headers=headers,
            json={"attachments": [{"type": "image", "name": "a.png", "content": PNG_BASE64}]},
            timeout=10,
        )
        assert resp.status_code not in (401, 403), resp.text


@pytest.mark.skipif(not VISION_MODEL, reason="未设置 GROOT_VISION_MODEL")
class TestImageUnderstandingSuccess:
    """成功路径，需要真实的视觉模型"""

    def test_understand_image(self, server, key_headers):
        """TC-IMG-008: 用指定视觉模型解析 1x1 PNG"""
        resp = requests.post(
            URL,
            headers={**key_headers, "X-Model-Name": VISION_MODEL},
            json={"attachments": [{"type": "image", "name": "a.png", "content": PNG_BASE64}]},
            timeout=120,
        )
        assert resp.status_code == 200, resp.text
        body = resp.json()
        assert body["text"]
        assert body["model"] == VISION_MODEL


class TestImageUnderstandingNoDefaultVision:
    """未配置默认视觉模型时的错误路径（依赖 Web 会话改默认模型）"""

    def test_no_default_vision_model(self, server, web, api_key):
        """TC-IMG-010: 未配置默认视觉模型 → 400 invalid_model，不回落其它类型默认"""
        orig_vision = web.get(f"{BASE_URL}/web/models").json()["defaults"].get("vision", "")
        name = f"t-img-vision-{uuid.uuid4().hex[:8]}"
        body = {
            "name": name,
            "model": "gpt-4o",
            "base_url": "https://api.openai.com/v1",
            "api_key": "sk-system-test",
            "temperature": 0.7,
            "top_p": 1.0,
            "stop": [],
            "enabled": True,
        }
        try:
            # 先确保确有视觉模型可用，再取消默认，避免「本来就没有」时用例空转
            assert web.post(f"{BASE_URL}/web/models", json=body).status_code == 200, "创建模型失败"
            assert web.put(f"{BASE_URL}/web/models/{name}/default?type=vision").status_code == 200
            assert web.delete(f"{BASE_URL}/web/models/{name}/default?type=vision").status_code == 200
            assert web.get(f"{BASE_URL}/web/models").json()["defaults"]["vision"] == ""

            resp = requests.post(
                URL, headers={"Content-Type": "application/json", "X-API-Key": api_key},
                json={"attachments": [{"type": "image", "name": "a.png", "content": PNG_BASE64}]},
                timeout=10,
            )
            assert resp.status_code == 400, resp.text
            payload = resp.json()
            assert payload["status"] == "invalid_model"
            assert "默认视觉模型" in payload["message"]
        finally:
            # 先摘掉可能残留的视觉默认标记再删模型（Delete 对仍持默认的模型返回 409），
            # 这样即使 try 块中途失败也不会把带默认标记的模型留在库里
            web.delete(f"{BASE_URL}/web/models/{name}/default?type=vision")
            deleted = web.delete(f"{BASE_URL}/web/models/{name}")
            restored = None
            if orig_vision:
                restored = web.put(f"{BASE_URL}/web/models/{orig_vision}/default?type=vision")
            # 仅在用例本身没有异常时才校验清理结果：finally 里抛断言会掩盖真正的失败原因
            if sys.exc_info()[0] is None:
                assert deleted.status_code == 200, f"清理新建模型失败: {deleted.text}"
                if restored is not None:
                    assert restored.status_code == 200, f"恢复原默认视觉模型失败: {restored.text}"
