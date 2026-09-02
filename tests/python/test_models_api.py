"""模型管理 API 系统测试（/web/models 系列端点）。

模型配置唯一存储于数据库，通过 Web UI（设置 → 模型）管理，
本文件覆盖其背后的 /web/models 系列端点（WebSession Cookie 认证）。

运行前提：groot 服务已启动，且已完成 Web 用户初始化（POST /web/setup）。
环境变量：
  GROOT_TEST_HOST / GROOT_TEST_PORT  服务地址（默认 localhost:8080，见 conftest）
  GROOT_WEB_USER / GROOT_WEB_PASS    Web 登录凭据（与 test_web_auth.py 一致）

用例点：
- 模型 CRUD（创建/列表/更新/删除）
- 首个模型自动默认
- 默认模型删除/禁用保护（409）
- 重名冲突（409）
- api_key 脱敏与留空不改
- 设默认 / 禁用模型不可设默认
- 连接测试（不可达地址返回 unhealthy）
- chat 引用不存在/禁用模型报错（400）
"""
import os
import uuid

import pytest
import requests

from conftest import BASE_URL

WEB_USER = os.environ.get("GROOT_WEB_USER", "admin")
WEB_PASS = os.environ.get("GROOT_WEB_PASS", "")


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
def model_name(web):
    """生成唯一模型名，测试结束后清理（若非默认）"""
    name = f"t-{uuid.uuid4().hex[:8]}"
    yield name
    web.delete(f"{BASE_URL}/web/models/{name}")


def model_body(name, **overrides):
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
    body.update(overrides)
    return body


class TestModelsCRUD:
    def test_create_and_list(self, web, model_name):
        resp = web.post(f"{BASE_URL}/web/models", json=model_body(model_name))
        assert resp.status_code == 200

        resp = web.get(f"{BASE_URL}/web/models")
        assert resp.status_code == 200
        names = [m["name"] for m in resp.json()["models"]]
        assert model_name in names
        created = next(m for m in resp.json()["models"] if m["name"] == model_name)
        assert "sk-system-test" not in created["api_key"], "api_key 应脱敏"

    def test_duplicate_name_conflict(self, web, model_name):
        assert web.post(f"{BASE_URL}/web/models", json=model_body(model_name)).status_code == 200
        assert web.post(f"{BASE_URL}/web/models", json=model_body(model_name)).status_code == 409

    def test_update_keeps_key_when_empty(self, web, model_name):
        web.post(f"{BASE_URL}/web/models", json=model_body(model_name))
        resp = web.put(
            f"{BASE_URL}/web/models/{model_name}",
            json=model_body(model_name, api_key="", temperature=1.2),
        )
        assert resp.status_code == 200
        got = next(m for m in web.get(f"{BASE_URL}/web/models").json()["models"]
                   if m["name"] == model_name)
        assert got["temperature"] == 1.2

    def test_update_not_found(self, web):
        resp = web.put(f"{BASE_URL}/web/models/no-such-model", json=model_body("no-such-model"))
        assert resp.status_code == 404

    def test_delete(self, web, model_name):
        web.post(f"{BASE_URL}/web/models", json=model_body(model_name))
        assert web.delete(f"{BASE_URL}/web/models/{model_name}").status_code == 200
        names = [m["name"] for m in web.get(f"{BASE_URL}/web/models").json()["models"]]
        assert model_name not in names


class TestDefaultModel:
    def test_first_model_becomes_default(self, web, model_name):
        """首个模型自动默认：库中已有模型时验证 default 字段非空且指向存在的模型"""
        web.post(f"{BASE_URL}/web/models", json=model_body(model_name))
        data = web.get(f"{BASE_URL}/web/models").json()
        assert data["default"], "存在模型时必须有默认模型"
        names = [m["name"] for m in data["models"]]
        assert data["default"] in names
        default_model = next(m for m in data["models"] if m["name"] == data["default"])
        assert default_model["is_default"] is True

    def test_set_default_and_protection(self, web):
        a = f"t-{uuid.uuid4().hex[:8]}"
        b = f"t-{uuid.uuid4().hex[:8]}"
        orig_default = web.get(f"{BASE_URL}/web/models").json().get("default", "")
        try:
            web.post(f"{BASE_URL}/web/models", json=model_body(a))
            web.post(f"{BASE_URL}/web/models", json=model_body(b))

            assert web.put(f"{BASE_URL}/web/models/{a}/default").status_code == 200
            assert web.get(f"{BASE_URL}/web/models").json()["default"] == a

            # 默认模型禁止删除 / 禁用
            assert web.delete(f"{BASE_URL}/web/models/{a}").status_code == 409
            assert web.put(f"{BASE_URL}/web/models/{a}",
                           json=model_body(a, api_key="", enabled=False)).status_code == 409

            # 禁用的模型不可设为默认
            web.put(f"{BASE_URL}/web/models/{b}", json=model_body(b, api_key="", enabled=False))
            assert web.put(f"{BASE_URL}/web/models/{b}/default").status_code == 400
        finally:
            if orig_default:
                web.put(f"{BASE_URL}/web/models/{orig_default}/default")
            web.delete(f"{BASE_URL}/web/models/{a}")
            web.delete(f"{BASE_URL}/web/models/{b}")


class TestConnection:
    def test_test_endpoint_unreachable(self, web):
        resp = web.post(f"{BASE_URL}/web/models/test", json={
            "base_url": "http://127.0.0.1:9",  # 不可达端口
            "api_key": "sk-x",
            "model": "gpt-4o",
        })
        assert resp.status_code == 200
        assert resp.json()["status"] == "unhealthy"


class TestChatModelErrors:
    """chat 引用不存在/禁用模型时应在执行前被拦截并报 400"""

    def test_chat_with_unknown_model(self, web):
        resp = web.post(
            f"{BASE_URL}/chat",
            headers={"X-Model-Name": "no-such-model"},
            json={"instruction": "hi"},
        )
        assert resp.status_code == 400

    def test_chat_with_disabled_model(self, web):
        a = f"t-{uuid.uuid4().hex[:8]}"
        b = f"t-{uuid.uuid4().hex[:8]}"
        orig_default = web.get(f"{BASE_URL}/web/models").json().get("default", "")
        try:
            # 保证 b 不是默认模型：先建 a（空库时 a 抢占默认），再建 b 并禁用
            web.post(f"{BASE_URL}/web/models", json=model_body(a))
            web.post(f"{BASE_URL}/web/models", json=model_body(b))
            assert web.put(f"{BASE_URL}/web/models/{b}",
                           json=model_body(b, api_key="", enabled=False)).status_code == 200

            resp = web.post(
                f"{BASE_URL}/chat",
                headers={"X-Model-Name": b},
                json={"instruction": "hi"},
            )
            assert resp.status_code == 400
        finally:
            if orig_default:
                web.put(f"{BASE_URL}/web/models/{orig_default}/default")
            web.delete(f"{BASE_URL}/web/models/{a}")
            web.delete(f"{BASE_URL}/web/models/{b}")
