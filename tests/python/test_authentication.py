"""
认证功能测试（JWT API Key）

新认证机制：
- 认证始终开启，服务端配置只有 security.auth.header_name（默认 X-API-Key）
  与 security.auth.secret（HS256 签名密钥）
- API Key 本身是 JWT，只能通过 Web 端点（POST /web/apikeys）创建，保存在数据库；
  请求时放在 X-API-Key 请求头
- 无 Key / 无效 Key / 已删除的 Key → 401，响应体 status=unauthorized
- Key 有效但权限不足 → 403，响应体 status=forbidden
- 权限点：chat / status / detail / history / session / schedule / all
- /web/health 免认证
"""

import time

import pytest
import requests

from conftest import BASE_URL, _web_login, bootstrap_api_key, delete_api_key


class TestAuthenticationBasic:
    """基础认证测试（认证始终开启）"""

    def test_no_api_key_returns_401(self, server, no_auth_headers):
        """TC-AUTH-001: 无 API Key → 401 unauthorized"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=no_auth_headers,
            json={"instruction": "test"},
            stream=True,
            timeout=10
        )

        assert response.status_code == 401
        data = response.json()
        assert data["status"] == "unauthorized"
        assert "API Key" in data["message"]

    def test_invalid_api_key_returns_401(self, server, invalid_auth_headers):
        """TC-AUTH-002: 无效 API Key（签名不合法的 JWT）→ 401"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=invalid_auth_headers,
            json={"instruction": "test"},
            stream=True,
            timeout=10
        )

        assert response.status_code == 401
        data = response.json()
        assert data["status"] == "unauthorized"

    def test_valid_api_key_success(self, server, api_headers):
        """TC-AUTH-003: 有效 API Key 不被认证/权限拦截"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "test"},
            stream=True,
            timeout=10
        )

        assert response.status_code not in (401, 403)
        response.close()

    def test_empty_api_key_returns_401(self, server):
        """TC-AUTH-004: 空 API Key → 401"""
        headers = {
            "Content-Type": "application/json",
            "X-API-Key": ""
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=headers,
            json={"instruction": "test"},
            stream=True,
            timeout=10
        )

        assert response.status_code == 401


class TestAuthenticationAllAPIs:
    """所有业务 API 强制认证：无 Key 一律 401"""

    def test_chat_api_requires_auth(self, server, no_auth_headers):
        """TC-AUTH-005: POST /chat 无 Key → 401"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=no_auth_headers,
            json={"instruction": "test"},
            stream=True,
            timeout=10
        )
        assert response.status_code == 401

    def test_delete_chat_endpoint_removed(self, server, no_auth_headers):
        """TC-AUTH-006: DELETE /chat/{sid} 端点已删除 → 401/404"""
        response = requests.delete(
            f"{BASE_URL}/chat/test_session",
            headers=no_auth_headers
        )
        # 端点已删除；认证中间件先行时返回 401，路由未注册时返回 404
        assert response.status_code in (401, 404)

    def test_chat_status_requires_auth(self, server, no_auth_headers):
        """TC-AUTH-007: GET /chat/status/{sid} 无 Key → 401"""
        response = requests.get(
            f"{BASE_URL}/chat/status/test_session",
            headers=no_auth_headers
        )
        assert response.status_code == 401

    def test_chat_detail_requires_auth(self, server, no_auth_headers):
        """TC-AUTH-008: GET /chat/{sid} 无 Key → 401"""
        response = requests.get(
            f"{BASE_URL}/chat/test_session",
            headers=no_auth_headers
        )
        assert response.status_code == 401

    def test_session_detail_requires_auth(self, server, no_auth_headers):
        """TC-AUTH-009: GET /sess/{sid} 无 Key → 401"""
        response = requests.get(
            f"{BASE_URL}/sess/test_session",
            headers=no_auth_headers
        )
        assert response.status_code == 401

    def test_session_history_requires_auth(self, server, no_auth_headers):
        """TC-AUTH-010: GET /sess/history 无 Key → 401"""
        response = requests.get(
            f"{BASE_URL}/sess/history",
            headers=no_auth_headers
        )
        assert response.status_code == 401


class TestHealthNoAuth:
    """健康检查无需认证"""

    def test_health_no_auth_required(self, server):
        """TC-AUTH-011: GET /web/health 无需认证"""
        response = requests.get(f"{BASE_URL}/web/health")

        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "healthy"


class TestPermissionSystem:
    """权限系统测试：用 Web 端点创建受限权限的 API Key"""

    def test_permission_chat_only(self, server):
        """TC-PERM-001: chat 权限只能调用 chat API，其余端点 403"""
        token = bootstrap_api_key(BASE_URL, name="pytest-chat-only", permissions=["chat"])
        headers = {
            "Content-Type": "application/json",
            "X-API-Key": token
        }

        # 可以调用 POST /chat（不被认证/权限拦截）
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=headers,
            json={"instruction": "test"},
            stream=True,
            timeout=10
        )
        assert response.status_code not in (401, 403)
        response.close()

        # 不能调用 GET /sess/history（需要 history 权限）→ 403
        response = requests.get(
            f"{BASE_URL}/sess/history",
            headers=headers,
            timeout=10
        )
        assert response.status_code == 403
        assert "forbidden" in response.text

    def test_permission_status_only_forbidden_chat(self, server):
        """TC-PERM-003: 仅 status 权限调用 POST /chat → 403"""
        token = bootstrap_api_key(BASE_URL, name="pytest-status-only", permissions=["status"])
        headers = {
            "Content-Type": "application/json",
            "X-API-Key": token
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=headers,
            json={"instruction": "test"},
            timeout=10
        )
        assert response.status_code == 403
        assert "forbidden" in response.text

        # status 权限本身可用
        response = requests.get(
            f"{BASE_URL}/chat/status/never-existed",
            headers=headers,
            timeout=10
        )
        assert response.status_code not in (401, 403)

    def test_permission_all_access(self, server, api_headers):
        """TC-PERM-002: all 权限可访问所有 API"""
        response = requests.get(f"{BASE_URL}/sess/history", headers=api_headers, timeout=10)
        assert response.status_code == 200

        response = requests.get(
            f"{BASE_URL}/chat/status/never-existed", headers=api_headers, timeout=10
        )
        assert response.status_code == 200

        response = requests.get(f"{BASE_URL}/web/health", timeout=10)
        assert response.status_code == 200


class TestKeyRevocation:
    """API Key 删除即吊销"""

    def test_deleted_key_immediately_revoked(self, server):
        """TC-AUTH-012: 删除 API Key 后，原 token 立即失效 → 401"""
        # 唯一 name，避免与历史运行残留的 Key 重名
        name = f"pytest-revoke-{int(time.time() * 1000)}"
        token = bootstrap_api_key(BASE_URL, name=name)
        headers = {
            "Content-Type": "application/json",
            "X-API-Key": token
        }

        # 删除前：token 有效
        response = requests.get(f"{BASE_URL}/sess/history", headers=headers, timeout=10)
        assert response.status_code == 200

        # 按 name 找到 id 并删除
        session = _web_login(BASE_URL)
        keys = session.get(f"{BASE_URL}/web/apikeys", timeout=10).json()["keys"]
        key_id = next(k["id"] for k in keys if k["name"] == name)
        delete_api_key(BASE_URL, key_id)

        # 删除后：同一 token 立即失效
        response = requests.get(f"{BASE_URL}/sess/history", headers=headers, timeout=10)
        assert response.status_code == 401
