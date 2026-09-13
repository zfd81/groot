# tests/python/test_files_api.py
"""
文件面板 API（/web/files/*）系统测试

覆盖：认证要求、路径安全、隐藏/只读规则、白名单 CRUD、scaffold。
运行方式（用户执行）：cd tests/python && pytest test_files_api.py -v

注意：scaffoldNameRe 与 validName 均要求名称首字符为字母/数字
（internal/webfiles/scaffold.go、service.go），因此测试沙箱名称
不能以下划线开头，统一使用 pytest- 前缀。
"""

import os
import requests
import pytest

from conftest import BASE_URL, TEST_WEB_USER, TEST_WEB_PASS

WEB_USER = os.environ.get("GROOT_WEB_USER", TEST_WEB_USER)
WEB_PASS = os.environ.get("GROOT_WEB_PASS", TEST_WEB_PASS)

FILES = f"{BASE_URL}/web/files"


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
        pytest.skip(f"Web 登录失败 ({resp.status_code}): {resp.text}")
    yield s


class TestFilesAuth:
    """未登录访问一律 401"""

    def test_list_without_cookie(self, server):
        r = requests.get(f"{FILES}/list", params={"path": ""}, timeout=10)
        assert r.status_code == 401

    def test_save_without_cookie(self, server):
        r = requests.put(f"{FILES}/content",
                         json={"path": "GROOT.md", "content": "x"}, timeout=10)
        assert r.status_code == 401


class TestFilesSecurity:
    """路径安全与可见性规则"""

    def test_list_root(self, web):
        r = web.get(f"{FILES}/list", params={"path": ""}, timeout=10)
        assert r.status_code == 200
        body = r.json()
        assert body["status"] == "success"
        assert body["home"]

    def test_groot_db_hidden_in_list(self, web):
        r = web.get(f"{FILES}/list", params={"path": ""}, timeout=10)
        names = [e["name"] for e in r.json()["entries"]]
        assert not any(n.startswith("groot.db") for n in names)
        assert ".DS_Store" not in names

    def test_groot_db_direct_access_404(self, web):
        for path in ["groot.db", "groot.db-wal", "groot.db-shm"]:
            r = web.get(f"{FILES}/content", params={"path": path}, timeout=10)
            assert r.status_code == 404, f"{path} 应 404, got {r.status_code}"

    def test_traversal_normalized(self, web):
        # "../" 被锚定归一化到根，不产生越界（归一化为 etc/，通常不存在 → 404）
        r = web.get(f"{FILES}/list", params={"path": "../../etc"}, timeout=10)
        assert r.status_code in (200, 404)

    def test_config_readonly(self, web):
        r = web.get(f"{FILES}/content", params={"path": "config.yaml"}, timeout=10)
        assert r.status_code == 200
        assert r.json()["readonly"] is True
        r = web.put(f"{FILES}/content",
                    json={"path": "config.yaml", "content": "x"}, timeout=10)
        assert r.status_code == 403
        r = web.delete(f"{FILES}", params={"path": "config.yaml"}, timeout=10)
        assert r.status_code == 403


class TestFilesCrud:
    """白名单内的创建/编辑/改名/删除全流程（skills/pytest-sandbox-skill 沙箱）"""

    SKILL = "pytest-sandbox-skill"

    def test_full_lifecycle(self, web):
        # 0. 清理可能的上次残留（忽略结果）
        for p in [f"skills/{self.SKILL}/note2.md", f"skills/{self.SKILL}/note.md",
                  f"skills/{self.SKILL}/SKILL.md", f"skills/{self.SKILL}"]:
            web.delete(f"{FILES}", params={"path": p}, timeout=10)

        # 1. scaffold 创建 skill
        r = web.post(f"{FILES}/scaffold",
                     json={"kind": "skill", "name": self.SKILL}, timeout=10)
        assert r.status_code == 200, r.text
        skill_md = r.json()["path"]
        assert skill_md == f"skills/{self.SKILL}/SKILL.md"

        # 2. 重名 scaffold → 409
        r = web.post(f"{FILES}/scaffold",
                     json={"kind": "skill", "name": self.SKILL}, timeout=10)
        assert r.status_code == 409

        # 3. skill 目录内新建文件 + 保存内容
        note = f"skills/{self.SKILL}/note.md"
        r = web.post(f"{FILES}/create", json={"path": note}, timeout=10)
        assert r.status_code == 200, r.text
        r = web.put(f"{FILES}/content",
                    json={"path": note, "content": "# note\n"}, timeout=10)
        assert r.status_code == 200, r.text
        r = web.get(f"{FILES}/content", params={"path": note}, timeout=10)
        assert r.json()["content"] == "# note\n"

        # 4. 白名单外新建 → 403
        r = web.post(f"{FILES}/create", json={"path": "logs/hack.txt"}, timeout=10)
        assert r.status_code == 403

        # 5. 改名
        note2 = f"skills/{self.SKILL}/note2.md"
        r = web.post(f"{FILES}/rename", json={"from": note, "to": note2}, timeout=10)
        assert r.status_code == 200, r.text

        # 6. 删除非空目录 → 409
        r = web.delete(f"{FILES}", params={"path": f"skills/{self.SKILL}"}, timeout=10)
        assert r.status_code == 409

        # 7. 清理：先删文件再删目录
        for p in [note2, skill_md]:
            r = web.delete(f"{FILES}", params={"path": p}, timeout=10)
            assert r.status_code == 200, f"删除 {p} 失败: {r.text}"
        r = web.delete(f"{FILES}", params={"path": f"skills/{self.SKILL}"}, timeout=10)
        assert r.status_code == 200, r.text

    def test_upload_to_mcp(self, web):
        # 清理残留
        web.delete(f"{FILES}", params={"path": "mcp/pytest-upload.json"}, timeout=10)
        r = web.post(f"{FILES}/upload",
                     data={"path": "mcp"},
                     files={"file": ("pytest-upload.json", b'{"name":"t"}')},
                     timeout=10)
        assert r.status_code == 200, r.text
        # 清理
        r = web.delete(f"{FILES}", params={"path": "mcp/pytest-upload.json"}, timeout=10)
        assert r.status_code == 200

    def test_upload_to_logs_forbidden(self, web):
        r = web.post(f"{FILES}/upload",
                     data={"path": "logs"},
                     files={"file": ("x.txt", b"data")},
                     timeout=10)
        assert r.status_code == 403
