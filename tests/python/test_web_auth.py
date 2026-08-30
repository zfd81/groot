"""Web 界面与登录端点系统测试。

运行前置：groot 服务已启动（由 conftest 的 server fixture 保证）。
- security.web 关闭时：跳过登录类用例，仅验证 /web/me 与 /ui。
- security.web 开启时：需设置环境变量 GROOT_WEB_USER / GROOT_WEB_PASS 为配置的账号。

用例点：
- TC-WEB-001 /web/me 返回 authenticated 与 auth_required 字段
- TC-WEB-002 /ui/ 返回 HTML 页面
- TC-WEB-003 /ui/ 下不存在的路径回退到 index（SPA history 模式）
- TC-WEB-004 路径穿越请求不泄漏二进制外文件
- TC-WEB-005 错误密码返回 401
- TC-WEB-006 正确登录下发 Cookie，携带 Cookie 可访问受保护端点，登出后令牌失效
- TC-WEB-007 无凭证访问受保护端点返回 401
"""

import os

import pytest
import requests

from conftest import BASE_URL

WEB_USER = os.environ.get("GROOT_WEB_USER", "admin")
WEB_PASS = os.environ.get("GROOT_WEB_PASS", "")


def _me():
    r = requests.get(f"{BASE_URL}/web/me", timeout=5)
    r.raise_for_status()
    return r.json()


def _skip_unless_web_auth():
    """Web 登录认证未开启时跳过（运行期判定，避免收集阶段发请求）"""
    if not _me().get("auth_required", False):
        pytest.skip("security.web 未开启")


class TestWebUI:
    """Web 静态页面托管"""

    def test_me_endpoint(self, server):
        """TC-WEB-001: /web/me 返回登录态字段"""
        data = _me()
        assert "authenticated" in data
        assert "auth_required" in data

    def test_ui_served(self, server):
        """TC-WEB-002: /ui/ 返回 HTML 页面（构建后为 SPA，未构建为提示页）"""
        r = requests.get(f"{BASE_URL}/ui/", timeout=5)
        assert r.status_code == 200
        assert "html" in r.headers.get("Content-Type", "")

    def test_ui_history_fallback(self, server):
        """TC-WEB-003: 前端路由路径回退到 index"""
        r = requests.get(f"{BASE_URL}/ui/dashboard", timeout=5)
        assert r.status_code == 200
        assert "html" in r.headers.get("Content-Type", "")

    @pytest.mark.parametrize(
        "path",
        [
            "/ui/../../etc/passwd",
            "/ui/..%2f..%2fetc%2fpasswd",
            "/ui/assets/../../passwd",
        ],
    )
    def test_ui_no_path_traversal(self, server, path):
        """TC-WEB-004: 路径穿越不泄漏文件内容"""
        r = requests.get(f"{BASE_URL}{path}", timeout=5)
        assert r.status_code in (200, 404)
        assert "root:" not in r.text


class TestWebLogin:
    """Web 登录 / 登出 / 凭证校验"""

    def test_login_wrong_password(self, server):
        """TC-WEB-005: 错误密码返回 401"""
        _skip_unless_web_auth()
        r = requests.post(
            f"{BASE_URL}/web/login",
            json={"username": WEB_USER, "password": "definitely-wrong"},
            timeout=5,
        )
        # 触发限速时返回 429，同样属于拒绝
        assert r.status_code in (401, 429)

    def test_login_and_cookie_access(self, server):
        """TC-WEB-006: 登录下发 Cookie，可访问受保护端点，登出后失效"""
        _skip_unless_web_auth()
        if not WEB_PASS:
            pytest.skip("未设置 GROOT_WEB_PASS")

        s = requests.Session()
        r = s.post(
            f"{BASE_URL}/web/login",
            json={"username": WEB_USER, "password": WEB_PASS},
            timeout=5,
        )
        assert r.status_code == 200, r.text
        assert "groot_web_session" in s.cookies

        r = s.get(f"{BASE_URL}/sess/history", timeout=5)
        assert r.status_code == 200

        r = s.get(f"{BASE_URL}/web/me", timeout=5)
        assert r.json()["authenticated"] is True

        r = s.post(f"{BASE_URL}/web/logout", timeout=5)
        assert r.status_code == 200

        # 登出后同一 Session 的 Cookie 已失效
        r = s.get(f"{BASE_URL}/web/me", timeout=5)
        assert r.json()["authenticated"] is False

    def test_unauthenticated_api_rejected(self, server):
        """TC-WEB-007: 无凭证访问受保护端点返回 401"""
        _skip_unless_web_auth()
        r = requests.get(f"{BASE_URL}/sess/history", timeout=5)
        if r.status_code == 200:
            pytest.skip("API 认证未开启，跳过")
        assert r.status_code == 401
