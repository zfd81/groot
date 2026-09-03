"""API Key 管理专项测试（/web/apikeys 系列端点，WebSession Cookie 认证）。

API Key 为 JWT（HS256），元数据存数据库（api_keys 表），token 由
secret + 元数据确定性签发（internal/api/handler/apikeys.go），因此
GET /web/apikeys/:id/token 任意次重取结果都应与创建时返回的 token 完全一致。

校验规则（apikeys.go Create）：
- 名称：TrimSpace 后非空、≤64 字节、不得为保留名 "web" → 违反 400 invalid_request
- expires_in：仅 1d/7d/1mo/6mo/1y/10y → 非法 400 invalid_expires_in
- permissions：非空且都在 ValidPermissions（chat/status/detail/history/
  session/schedule/all）内 → 违反 400 invalid_permissions
- 名称重复 → 409 name_exists
- token/delete 目标 id 不存在 → 404 not_found

用例点：
- TC-KEY-001 无 Cookie 访问四端点均 401
- TC-KEY-002 创建成功：响应字段齐全（id 14 位数字、token 三段 JWT、
  permissions/expires_at 回显、expired=false），List 可见
- TC-KEY-003 名称校验：空名 / 纯空白 / 超 64 字节 / 保留名 "web" → 400
- TC-KEY-004 权限校验：非法权限点 / 空权限 → 400
- TC-KEY-005 expires_in 校验：非法枚举值 → 400
- TC-KEY-006 重名 → 409
- TC-KEY-007 token 重取与创建返回完全一致（两次重取也一致）
- TC-KEY-008 token 重取不存在的 id → 404
- TC-KEY-009 删除后 List 不含且 token 立即失效（401）；删不存在的 id → 404
"""
import os
import uuid

import pytest
import requests

from conftest import BASE_URL, TEST_WEB_PASS, TEST_WEB_USER

WEB_USER = os.environ.get("GROOT_WEB_USER", TEST_WEB_USER)
WEB_PASS = os.environ.get("GROOT_WEB_PASS", TEST_WEB_PASS)


@pytest.fixture(scope="module")
def web(server):
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


def _key_name() -> str:
    """生成唯一的测试 Key 名（uuid 前缀，便于识别与清理）"""
    return f"tkey-{uuid.uuid4().hex[:8]}"


def _create_key(web, name, expires_in="1d", permissions=None):
    """创建 API Key，返回 Response（不校验状态码，由用例断言）"""
    return web.post(f"{BASE_URL}/web/apikeys", json={
        "name": name,
        "expires_in": expires_in,
        "permissions": permissions if permissions is not None else ["all"],
    }, timeout=10)


def _delete_key(web, key_id):
    """清理用：删除 Key，忽略结果（Key 可能已被用例删除）"""
    try:
        web.delete(f"{BASE_URL}/web/apikeys/{key_id}", timeout=10)
    except requests.RequestException:
        pass


def _list_ids_by_name(web, name):
    """按名称在列表中查找 Key，返回匹配的 id 列表"""
    keys = web.get(f"{BASE_URL}/web/apikeys", timeout=10).json().get("keys", [])
    return [k["id"] for k in keys if k.get("name") == name]


class TestApiKeysAuth:
    """TC-KEY-001: /web/apikeys 系列端点均受 WebSession 保护，无 Cookie 一律 401"""

    def test_list_without_cookie(self, server):
        r = requests.get(f"{BASE_URL}/web/apikeys", timeout=5)
        assert r.status_code == 401

    def test_create_without_cookie(self, server):
        r = requests.post(f"{BASE_URL}/web/apikeys", json={
            "name": "x", "expires_in": "1d", "permissions": ["all"],
        }, timeout=5)
        assert r.status_code == 401

    def test_token_without_cookie(self, server):
        r = requests.get(f"{BASE_URL}/web/apikeys/20260101000000/token", timeout=5)
        assert r.status_code == 401

    def test_delete_without_cookie(self, server):
        r = requests.delete(f"{BASE_URL}/web/apikeys/20260101000000", timeout=5)
        assert r.status_code == 401


class TestApiKeysCreate:
    """POST /web/apikeys 创建与校验"""

    def test_create_success_fields(self, web):
        """TC-KEY-002: 创建成功响应字段齐全，List 可见，1d 的 Key expired=false"""
        name = _key_name()
        key_id = None
        try:
            resp = _create_key(web, name, expires_in="1d",
                               permissions=["chat", "status"])
            assert resp.status_code == 200, resp.text
            data = resp.json()
            key_id = data["id"]

            # id 为 yyyyMMddHHmmss 秒级时间编号（14 位数字）
            assert len(data["id"]) == 14 and data["id"].isdigit(), \
                f"id 应为 14 位数字时间编号，实际: {data['id']}"
            # token 为三段式 JWT
            assert data["token"].count(".") == 2, "token 应为三段式 JWT"
            assert data["token"].startswith("eyJ"), "JWT header 应为 Base64 JSON"
            # 请求参数回显
            assert data["name"] == name
            assert data["permissions"] == ["chat", "status"]
            # expires_at/created_at 为毫秒时间戳，1d 的 Key 尚未过期
            assert data["expires_at"] > data["created_at"] > 0
            assert data["expired"] is False

            # 创建后出现在列表中，且列表元数据与创建响应一致（列表不含 token）
            keys = web.get(f"{BASE_URL}/web/apikeys", timeout=10).json()["keys"]
            listed = next((k for k in keys if k["id"] == key_id), None)
            assert listed is not None, "新建 Key 应出现在列表中"
            assert listed["name"] == name
            assert listed["permissions"] == ["chat", "status"]
            assert listed["expired"] is False
            assert "token" not in listed, "列表不应回显 token"
        finally:
            if key_id:
                _delete_key(web, key_id)

    def test_create_empty_name(self, web):
        """TC-KEY-003: 空名 400"""
        resp = _create_key(web, "")
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_request"

    def test_create_whitespace_name(self, web):
        """TC-KEY-003: 纯空白名称 TrimSpace 后为空 → 400"""
        resp = _create_key(web, "   ")
        assert resp.status_code == 400

    def test_create_name_too_long(self, web):
        """TC-KEY-003: 名称超 64 字节 400（按字节数校验，与列宽一致）"""
        resp = _create_key(web, "a" * 65)
        assert resp.status_code == 400
        # 恰好 64 字节应可创建（边界值），成功后清理
        name64 = "b" * 56 + uuid.uuid4().hex[:8]
        assert len(name64) == 64
        resp = _create_key(web, name64)
        try:
            assert resp.status_code == 200, resp.text
        finally:
            if resp.status_code == 200:
                _delete_key(web, resp.json()["id"])

    def test_create_reserved_name_web(self, web):
        """TC-KEY-003: 保留名 "web"（Cookie 通道的 caller 名）→ 400"""
        resp = _create_key(web, "web")
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_request"

    def test_create_invalid_permission(self, web):
        """TC-KEY-004: 非法权限点 400 invalid_permissions"""
        resp = _create_key(web, _key_name(), permissions=["chat", "no-such-perm"])
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_permissions"

    def test_create_empty_permissions(self, web):
        """TC-KEY-004: 空权限列表 400"""
        resp = _create_key(web, _key_name(), permissions=[])
        assert resp.status_code == 400
        assert resp.json()["status"] == "invalid_permissions"

    def test_create_invalid_expires_in(self, web):
        """TC-KEY-005: 非法 expires_in 400 invalid_expires_in"""
        for bad in ("2d", "forever", ""):
            resp = _create_key(web, _key_name(), expires_in=bad)
            assert resp.status_code == 400, f"expires_in={bad!r} 应 400"
            assert resp.json()["status"] == "invalid_expires_in"

    def test_create_duplicate_name(self, web):
        """TC-KEY-006: 重名 409 name_exists"""
        name = _key_name()
        key_id = None
        try:
            resp = _create_key(web, name)
            assert resp.status_code == 200, resp.text
            key_id = resp.json()["id"]

            resp2 = _create_key(web, name)
            assert resp2.status_code == 409
            assert resp2.json()["status"] == "name_exists"
        finally:
            if key_id:
                _delete_key(web, key_id)


class TestApiKeyToken:
    """GET /web/apikeys/:id/token 确定性重取"""

    def test_token_deterministic(self, web):
        """TC-KEY-007: 重取 token 与创建返回完全一致，两次重取也相同"""
        name = _key_name()
        key_id = None
        try:
            resp = _create_key(web, name)
            assert resp.status_code == 200, resp.text
            key_id = resp.json()["id"]
            created_token = resp.json()["token"]

            r1 = web.get(f"{BASE_URL}/web/apikeys/{key_id}/token", timeout=10)
            assert r1.status_code == 200
            r2 = web.get(f"{BASE_URL}/web/apikeys/{key_id}/token", timeout=10)
            assert r2.status_code == 200
            # token 由 secret + 元数据确定性还原，任意次重取均逐字符一致
            assert r1.json()["token"] == created_token
            assert r2.json()["token"] == created_token
        finally:
            if key_id:
                _delete_key(web, key_id)

    def test_token_not_found(self, web):
        """TC-KEY-008: 不存在的 id 重取 token → 404 not_found"""
        r = web.get(f"{BASE_URL}/web/apikeys/19700101000000/token", timeout=10)
        assert r.status_code == 404
        assert r.json()["status"] == "not_found"


class TestApiKeyDelete:
    """DELETE /web/apikeys/:id 删除即吊销"""

    def test_delete_removes_and_revokes(self, web):
        """TC-KEY-009: 删除后 List 不含，且原 token 立即失效（401）"""
        name = _key_name()
        key_id = None
        try:
            resp = _create_key(web, name)
            assert resp.status_code == 200, resp.text
            key_id = resp.json()["id"]
            token = resp.json()["token"]

            # 删除前 token 可用（all 权限访问 API 组端点）
            r = requests.get(f"{BASE_URL}/sess/history",
                             headers={"X-API-Key": token}, timeout=10)
            assert r.status_code == 200, f"删除前 token 应可用: {r.text}"

            r = web.delete(f"{BASE_URL}/web/apikeys/{key_id}", timeout=10)
            assert r.status_code == 200
            deleted_id, key_id = key_id, None  # 已删除，finally 无需再清理

            assert _list_ids_by_name(web, name) == [], "删除后列表不应再含该 Key"

            # 删除即吊销：JWT 签名仍有效但元数据已不在库中 → 401
            r = requests.get(f"{BASE_URL}/sess/history",
                             headers={"X-API-Key": token}, timeout=10)
            assert r.status_code == 401, "删除后原 token 应立即失效"
        finally:
            if key_id:
                _delete_key(web, key_id)

    def test_delete_not_found(self, web):
        """TC-KEY-009: 删除不存在的 id → 404 not_found"""
        r = web.delete(f"{BASE_URL}/web/apikeys/19700101000000", timeout=10)
        assert r.status_code == 404
        assert r.json()["status"] == "not_found"
