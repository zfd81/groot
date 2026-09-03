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
- TC-WEB-008 已有用户时 /web/setup 返回 409 already_initialized
- TC-WEB-009 用户已存在时 /web/me 的 needs_setup 为 false
- TC-WEB-010 /web/password 旧密码错误返回 401 wrong_password
- TC-WEB-011 修改密码全流程：改后旧密码登录失败、新密码登录成功（测后改回原密码）
- TC-WEB-012 Web Cookie 通行 API 组端点（/sess/history、/schedule，等同 all 权限）
- TC-WEB-013 修改密码新密码不足 8 位返回 400

说明：/web/setup 的弱密码（<8 位）校验在已有用户时不可达（先返回 409），
该场景在 test_cli_commands.py 的独立空库实例上覆盖。
"""

import os

import pytest
import requests

from conftest import BASE_URL, TEST_WEB_PASS, TEST_WEB_USER, _web_login

WEB_USER = os.environ.get("GROOT_WEB_USER", TEST_WEB_USER)
WEB_PASS = os.environ.get("GROOT_WEB_PASS", TEST_WEB_PASS)


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


class TestWebSetupAndMe:
    """/web/setup 与 /web/me（conftest 已保证用户存在）"""

    def test_setup_already_initialized(self, server):
        """TC-WEB-008: 已有用户时 setup 返回 409 already_initialized"""
        r = requests.post(
            f"{BASE_URL}/web/setup",
            json={"username": "another-admin", "password": "another-password-1"},
            timeout=5,
        )
        assert r.status_code == 409, r.text
        assert r.json()["status"] == "already_initialized"

    def test_me_needs_setup_false(self, server):
        """TC-WEB-009: 用户已存在时 needs_setup 为 false"""
        assert _me()["needs_setup"] is False


class TestWebPassword:
    """POST /web/password 修改密码（需有效会话）"""

    def test_change_password_wrong_old(self, server):
        """TC-WEB-010: 旧密码错误返回 401 wrong_password（会话保留）"""
        s = _web_login(BASE_URL)
        r = s.post(
            f"{BASE_URL}/web/password",
            json={"old_password": "definitely-wrong-old", "new_password": "new-password-abcd"},
            timeout=5,
        )
        assert r.status_code == 401, r.text
        assert r.json()["status"] == "wrong_password"
        # 旧密码错误不影响当前会话
        assert s.get(f"{BASE_URL}/web/me", timeout=5).json()["authenticated"] is True

    def test_change_password_weak_new(self, server):
        """TC-WEB-013: 新密码不足 8 位返回 400（旧密码正确时才走到长度校验）"""
        s = _web_login(BASE_URL)
        r = s.post(
            f"{BASE_URL}/web/password",
            json={"old_password": TEST_WEB_PASS, "new_password": "short"},
            timeout=5,
        )
        assert r.status_code == 400, r.text
        assert r.json()["status"] == "invalid_request"

    def test_change_password_flow(self, server):
        """TC-WEB-011: 修改成功后旧密码登录失败、新密码登录成功；测后必须改回原密码。

        修改密码会踢掉该用户其他会话、保留当前会话（webauth.go ChangePassword），
        因此 finally 优先用原会话改回；会话意外失效时再用新密码重新登录改回，
        尽力恢复共享环境，避免破坏后续测试。
        """
        new_pass = "tmp-changed-pass-2026"
        s = _web_login(BASE_URL)
        changed = False
        try:
            r = s.post(
                f"{BASE_URL}/web/password",
                json={"old_password": TEST_WEB_PASS, "new_password": new_pass},
                timeout=5,
            )
            assert r.status_code == 200, r.text
            changed = True

            # 旧密码登录失败（401；触发限速时 429，同属拒绝）
            r = requests.post(
                f"{BASE_URL}/web/login",
                json={"username": TEST_WEB_USER, "password": TEST_WEB_PASS},
                timeout=5,
            )
            assert r.status_code in (401, 429)

            # 新密码登录成功
            s2 = requests.Session()
            r = s2.post(
                f"{BASE_URL}/web/login",
                json={"username": TEST_WEB_USER, "password": new_pass},
                timeout=5,
            )
            assert r.status_code == 200, r.text
            assert s2.get(f"{BASE_URL}/web/me", timeout=5).json()["authenticated"] is True
        finally:
            if changed:
                # 当前会话在修改后仍保留，直接用它改回
                r = s.post(
                    f"{BASE_URL}/web/password",
                    json={"old_password": new_pass, "new_password": TEST_WEB_PASS},
                    timeout=5,
                )
                if r.status_code != 200:
                    # 兜底：原会话不可用时用新密码重新登录后改回
                    s3 = requests.Session()
                    s3.post(
                        f"{BASE_URL}/web/login",
                        json={"username": TEST_WEB_USER, "password": new_pass},
                        timeout=5,
                    )
                    r = s3.post(
                        f"{BASE_URL}/web/password",
                        json={"old_password": new_pass, "new_password": TEST_WEB_PASS},
                        timeout=5,
                    )
                assert r.status_code == 200, f"恢复原密码失败，共享环境已被破坏: {r.text}"

        # 恢复后原密码可正常登录
        assert _web_login(BASE_URL) is not None


class TestWebCookieAPIAccess:
    """TC-WEB-012: Web Cookie 通行 API 组端点（等同 all 权限）"""

    def test_cookie_access_sess_history(self, server):
        s = _web_login(BASE_URL)
        r = s.get(f"{BASE_URL}/sess/history", timeout=5)
        assert r.status_code == 200, r.text

    def test_cookie_access_schedule(self, server):
        s = _web_login(BASE_URL)
        r = s.get(f"{BASE_URL}/schedule", timeout=5)
        assert r.status_code == 200, r.text
        assert isinstance(r.json(), list)
