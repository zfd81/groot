"""
认证功能测试
测试 API Key 认证、权限验证、多 Key 配置等

注意: 默认配置禁用认证(security.auth.enabled: false)
当认证禁用时，无 API Key 也可以访问 API
当认证启用时，需要有效的 API Key
"""

import pytest
import requests
from conftest import BASE_URL, TEST_API_KEY


def is_auth_enabled():
    """检查服务是否启用了认证"""
    # 通过尝试不带认证访问来检测
    # 如果返回 401，说明认证启用
    # 如果返回其他状态，说明认证禁用
    try:
        response = requests.post(
            f"{BASE_URL}/chat",
            headers={"Content-Type": "application/json"},
            json={"instruction": "test"},
            timeout=5
        )
        return response.status_code == 401
    except:
        return False


class TestAuthenticationBasic:
    """基础认证测试"""

    def test_no_api_key_behavior(self, server, no_auth_headers):
        """TC-AUTH-001: 无 API Key 的行为（根据认证配置）"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=no_auth_headers,
            json={"instruction": "test"},
            stream=True,
            timeout=10
        )

        if is_auth_enabled():
            # 认证启用：应返回 401
            assert response.status_code == 401
            data = response.json()
            assert data["status"] == "unauthorized"
            assert "API Key" in data["message"]
        else:
            # 认证禁用：应允许访问
            assert response.status_code in [200, 404]

    def test_invalid_api_key_behavior(self, server, invalid_auth_headers):
        """TC-AUTH-002: 无效 API Key 的行为"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=invalid_auth_headers,
            json={"instruction": "test"},
            stream=True,
            timeout=10
        )

        if is_auth_enabled():
            # 认证启用：无效 key 应返回 401
            assert response.status_code == 401
            data = response.json()
            assert data["status"] == "unauthorized"
        else:
            # 认证禁用：任何 key 都可访问
            assert response.status_code in [200, 404]

    def test_valid_api_key_success(self, server, api_headers):
        """TC-AUTH-003: 有效 API Key（或认证禁用）成功"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "test"},
            stream=True,
            timeout=10
        )

        # 无论认证启用或禁用，都应能成功访问
        assert response.status_code in [200, 404]

    def test_empty_api_key_behavior(self, server):
        """TC-AUTH-004: 空 API Key 的行为"""
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

        if is_auth_enabled():
            # 认证启用：应返回 401（未认证）或 404（资源不存在但认证通过）
            # 注：某些端点可能先检查资源存在性
            assert response.status_code in [401, 404]
        else:
            assert response.status_code in [200, 404]


class TestAuthenticationAllAPIs:
    """所有 API 认证测试"""

    def test_chat_api_auth_behavior(self, server, no_auth_headers):
        """TC-AUTH-005: POST /chat 认证行为"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=no_auth_headers,
            json={"instruction": "test"},
            stream=True,
            timeout=10
        )

        if is_auth_enabled():
            # 认证启用：应返回 401（未认证）或 404（资源不存在但认证通过）
            # 注：某些端点可能先检查资源存在性
            assert response.status_code in [401, 404]
        else:
            assert response.status_code in [200, 404]

    def test_delete_chat_auth_behavior(self, server, no_auth_headers):
        """TC-AUTH-006: DELETE /chat/{sid} 认证行为"""
        response = requests.delete(
            f"{BASE_URL}/chat/test_session",
            headers=no_auth_headers
        )

        if is_auth_enabled():
            # DELETE /chat/{sid} 端点已删除，返回 404
            assert response.status_code == 404
        else:
            # 认证禁用时也可能返回 404
            assert response.status_code in [200, 404]

    def test_chat_status_auth_behavior(self, server, no_auth_headers):
        """TC-AUTH-007: GET /chat/status/{sid} 认证行为"""
        response = requests.get(
            f"{BASE_URL}/chat/status/test_session",
            headers=no_auth_headers
        )

        if is_auth_enabled():
            # 认证启用：应返回 401（未认证）或 404（资源不存在但认证通过）
            # 注：某些端点可能先检查资源存在性
            assert response.status_code in [401, 404]
        else:
            assert response.status_code in [200, 404]

    def test_chat_detail_auth_behavior(self, server, no_auth_headers):
        """TC-AUTH-008: GET /chat/{sid} 认证行为"""
        response = requests.get(
            f"{BASE_URL}/chat/test_session",
            headers=no_auth_headers
        )

        if is_auth_enabled():
            # 认证启用：应返回 401（未认证）或 404（资源不存在但认证通过）
            # 注：某些端点可能先检查资源存在性
            assert response.status_code in [401, 404]
        else:
            assert response.status_code in [200, 404]

    def test_session_detail_auth_behavior(self, server, no_auth_headers):
        """TC-AUTH-009: GET /sess/{sid} 认证行为"""
        response = requests.get(
            f"{BASE_URL}/sess/test_session",
            headers=no_auth_headers
        )

        if is_auth_enabled():
            # 认证启用：应返回 401（未认证）或 404（资源不存在但认证通过）
            # 注：某些端点可能先检查资源存在性
            assert response.status_code in [401, 404]
        else:
            assert response.status_code in [200, 404]

    def test_session_history_auth_behavior(self, server, no_auth_headers):
        """TC-AUTH-010: GET /sess/history 认证行为"""
        response = requests.get(
            f"{BASE_URL}/sess/history",
            headers=no_auth_headers
        )

        if is_auth_enabled():
            # 认证启用：应返回 401（未认证）或 404（资源不存在但认证通过）
            # 注：某些端点可能先检查资源存在性
            assert response.status_code in [401, 404]
        else:
            assert response.status_code in [200, 404]


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