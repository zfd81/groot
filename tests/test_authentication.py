"""
认证功能测试
测试 API Key 认证、权限验证、多 Key 配置等
"""

import pytest
import requests
from conftest import BASE_URL, TEST_API_KEY


class TestAuthenticationBasic:
    """基础认证测试"""

    def test_no_api_key(self, server, no_auth_headers):
        """TC-AUTH-001: 无 API Key 返回 401"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=no_auth_headers,
            json={"instruction": "test"}
        )

        assert response.status_code == 401
        data = response.json()
        assert data["status"] == "unauthorized"
        assert "API Key" in data["message"]

    def test_invalid_api_key(self, server, invalid_auth_headers):
        """TC-AUTH-002: 无效 API Key 返回 401"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=invalid_auth_headers,
            json={"instruction": "test"}
        )

        assert response.status_code == 401
        data = response.json()
        assert data["status"] == "unauthorized"

    def test_valid_api_key(self, server, api_headers):
        """TC-AUTH-003: 有效 API Key 成功"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "test"},
            stream=True
        )

        assert response.status_code == 200

    def test_empty_api_key(self, server):
        """TC-AUTH-004: 空 API Key 返回 401"""
        headers = {
            "Content-Type": "application/json",
            "X-API-Key": ""
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=headers,
            json={"instruction": "test"}
        )

        assert response.status_code == 401


class TestAuthenticationAllAPIs:
    """所有 API 认证测试"""

    def test_chat_api_requires_auth(self, server, no_auth_headers):
        """TC-AUTH-005: POST /chat 需要认证"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=no_auth_headers,
            json={"instruction": "test"}
        )

        assert response.status_code == 401

    def test_delete_chat_requires_auth(self, server, no_auth_headers):
        """TC-AUTH-006: DELETE /chat/{sid} 需要认证"""
        response = requests.delete(
            f"{BASE_URL}/chat/test_session",
            headers=no_auth_headers
        )

        assert response.status_code == 401

    def test_chat_status_requires_auth(self, server, no_auth_headers):
        """TC-AUTH-007: GET /chat/status/{sid} 需要认证"""
        response = requests.get(
            f"{BASE_URL}/chat/status/test_session",
            headers=no_auth_headers
        )

        assert response.status_code == 401

    def test_chat_detail_requires_auth(self, server, no_auth_headers):
        """TC-AUTH-008: GET /chat/{sid} 需要认证"""
        response = requests.get(
            f"{BASE_URL}/chat/test_session",
            headers=no_auth_headers
        )

        assert response.status_code == 401

    def test_session_detail_requires_auth(self, server, no_auth_headers):
        """TC-AUTH-009: GET /sess/{sid} 需要认证"""
        response = requests.get(
            f"{BASE_URL}/sess/test_session",
            headers=no_auth_headers
        )

        assert response.status_code == 401

    def test_session_history_requires_auth(self, server, no_auth_headers):
        """TC-AUTH-010: GET /sess/history 需要认证"""
        response = requests.get(
            f"{BASE_URL}/sess/history",
            headers=no_auth_headers
        )

        assert response.status_code == 401


class TestHealthNoAuth:
    """健康检查无需认证"""

    def test_health_no_auth_required(self, server):
        """TC-AUTH-011: GET /health 无需认证"""
        response = requests.get(f"{BASE_URL}/health")

        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "healthy"


class TestPermissionSystem:
    """权限系统测试（需要多 Key 配置）"""

    @pytest.mark.skip(reason="需要配置多 Key 环境")
    def test_permission_chat_only(self, server):
        """TC-PERM-001: chat 权限只能调用 chat API"""
        # 使用仅有 chat 权限的 key
        headers = {
            "Content-Type": "application/json",
            "X-API-Key": "chat-only-key"
        }

        # 可以调用 POST /chat
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=headers,
            json={"instruction": "test"}
        )
        assert response.status_code in [200, 401]

        # 不能调用 GET /sess/history
        response = requests.get(
            f"{BASE_URL}/sess/history",
            headers=headers
        )
        # 如果配置了权限限制，应返回 403
        # 否则返回 401（key 不存在）

    @pytest.mark.skip(reason="需要配置多 Key 环境")
    def test_permission_all_access(self, server, api_headers):
        """TC-PERM-002: all 权限可访问所有 API"""
        # api_headers 使用 all 权限的 key
        endpoints = [
            ("GET", "/sess/history"),
            ("GET", "/skills"),
            ("GET", "/tools"),
            ("GET", "/health"),
        ]

        for method, path in endpoints:
            if method == "GET":
                response = requests.get(f"{BASE_URL}{path}", headers=api_headers)
            assert response.status_code == 200

    @pytest.mark.skip(reason="需要配置多 Key 环境")
    def test_permission_forbidden(self, server):
        """TC-PERM-003: 无权限返回 403"""
        headers = {
            "Content-Type": "application/json",
            "X-API-Key": "status-only-key"  # 仅有 status 权限
        }

        # 尝试调用 POST /chat（需要 chat 权限）
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=headers,
            json={"instruction": "test"}
        )

        # 如果 key 存在但无权限，应返回 403
        # 如果 key 不存在，返回 401
        assert response.status_code in [401, 403]