"""
Groot API 测试配置文件
pytest fixtures 和公共配置
"""

import pytest
import requests
import json
import os
import time
import base64
import subprocess
import signal
from typing import Optional, Dict, List, Generator
from datetime import datetime


# 测试环境配置
TEST_HOST = os.environ.get("GROOT_TEST_HOST", "localhost")
TEST_PORT = os.environ.get("GROOT_TEST_PORT", "8080")
# JWT API Key 认证：服务端只需 HS256 签名密钥；API Key 通过 Web 端点创建
TEST_AUTH_SECRET = os.environ.get("GROOT_TEST_AUTH_SECRET", "groot-test-secret-0123456789abcdef")
TEST_WEB_USER = os.environ.get("GROOT_WEB_USER", "admin")
TEST_WEB_PASS = os.environ.get("GROOT_WEB_PASS", "test-password-2026")
TEST_HOME = os.environ.get("GROOT_TEST_HOME", "/tmp/groot_test")
# GROOT_BIN: tests/python -> tests -> groot -> dist/groot
GROOT_BIN = os.environ.get("GROOT_BIN", os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(__file__))), "dist", "groot"))

BASE_URL = f"http://{TEST_HOST}:{TEST_PORT}"


def generate_session_id() -> str:
    """生成测试用的 session_id（符合新格式）"""
    timestamp = datetime.now().strftime("%Y%m%d%H%M%S%f")[:17]  # YYYYMMDDHHMMSSmmm
    random_part = "".join([chr(c) for c in [ord("a") + i % 26 for i in range(4)]])
    return f"{timestamp}_{random_part}"


def generate_chat_id() -> str:
    """生成测试用的 chat_id（格式：{YYYYMMDDHHMMSSmmm}，17 位纯数字，无前缀）"""
    return datetime.now().strftime("%Y%m%d%H%M%S%f")[:17]


def generate_step_id() -> str:
    """生成测试用的 step_id"""
    date_part = datetime.now().strftime("%Y%m%d")
    time_part = datetime.now().strftime("%H%M%S%f")[:9]  # HHMMSSmmm
    random_part = "".join([chr(c) for c in [ord("a") + i % 26 for i in range(6)]])
    return f"{date_part}-{time_part}-{random_part}"


def wait_for_server(timeout: int = 30) -> bool:
    """等待服务器启动（健康检查端点为 /web/health，免认证）"""
    start_time = time.time()
    while time.time() - start_time < timeout:
        try:
            response = requests.get(f"{BASE_URL}/web/health", timeout=2)
            if response.status_code == 200:
                return True
        except:
            pass
        time.sleep(1)
    return False


def _web_login(base_url: str, username: str = None, password: str = None) -> requests.Session:
    """确保 Web 用户存在并登录，返回携带 groot_web_session Cookie 的 Session。

    注意：/web/login 有失败锁定（429），此处不做错误密码重试。
    """
    username = username or TEST_WEB_USER
    password = password or TEST_WEB_PASS

    # 用户表为空（needs_setup=true）时先创建初始管理员
    me = requests.get(f"{base_url}/web/me", timeout=10).json()
    if me.get("needs_setup"):
        # 已有用户时会返回 409，此时直接走登录即可，不视为错误
        requests.post(
            f"{base_url}/web/setup",
            json={"username": username, "password": password},
            timeout=10,
        )

    session = requests.Session()
    resp = session.post(
        f"{base_url}/web/login",
        json={"username": username, "password": password},
        timeout=10,
    )
    if resp.status_code != 200:
        raise RuntimeError(f"Web 登录失败 ({resp.status_code}): {resp.text}")
    return session


def ensure_default_model(base_url, username=None, password=None):
    """确保模型库中存在默认模型（模型只存数据库，空库时 /chat 会报 400 invalid_model）。

    登录 Web 后检查 GET /web/models：已有默认模型则直接返回；
    否则创建测试模型（重名 409 视为已存在），并在必要时显式设为默认。
    """
    session = _web_login(base_url, username, password)

    resp = session.get(f"{base_url}/web/models", timeout=10)
    if resp.status_code != 200:
        raise RuntimeError(f"获取模型列表失败 ({resp.status_code}): {resp.text}")
    data = resp.json()
    if data.get("default"):
        return data["default"]

    # 无默认模型：创建测试模型（首个模型会自动成为默认）
    model_name = "qwen-local"
    body = {
        "name": model_name,
        "model": "Qwen3.5-122B-A10B-6bit",
        "base_url": "http://127.0.0.1:8230/v1",
        "api_key": "bonc1q2w3e",
        "max_completion_tokens": 40960,
        "temperature": 0.2,
        "enabled": True,
    }
    resp = session.post(f"{base_url}/web/models", json=body, timeout=10)
    if resp.status_code not in (200, 409):  # 409 = 重名，视为已存在
        raise RuntimeError(f"创建模型失败 ({resp.status_code}): {resp.text}")

    # 若仍无默认（如已有模型但默认为空），显式设为默认
    data = session.get(f"{base_url}/web/models", timeout=10).json()
    if not data.get("default"):
        r = session.put(f"{base_url}/web/models/{model_name}/default", timeout=10)
        if r.status_code != 200:
            raise RuntimeError(f"设置默认模型失败 ({r.status_code}): {r.text}")
        return model_name
    return data["default"]


def bootstrap_api_key(base_url, name="pytest-all", permissions=None, expires_in="1d",
                      username=None, password=None):
    """确保 Web 用户存在并登录，创建（或重名时重取）API Key，返回 JWT token 字符串"""
    session = _web_login(base_url, username, password)

    payload = {
        "name": name,
        "expires_in": expires_in,
        "permissions": permissions if permissions is not None else ["all"],
    }
    resp = session.post(f"{base_url}/web/apikeys", json=payload, timeout=10)
    if resp.status_code == 200:
        return resp.json()["token"]

    if resp.status_code == 409:
        # 重名：按 name 找到已有 Key 的 id，再确定性重取 token
        keys = session.get(f"{base_url}/web/apikeys", timeout=10).json().get("keys", [])
        for k in keys:
            if k.get("name") == name:
                r = session.get(f"{base_url}/web/apikeys/{k['id']}/token", timeout=10)
                if r.status_code == 200:
                    return r.json()["token"]
                raise RuntimeError(f"重取 API Key token 失败 ({r.status_code}): {r.text}")
        raise RuntimeError(f"创建 API Key 返回 409，但列表中未找到重名 Key {name!r}")

    raise RuntimeError(f"创建 API Key 失败 ({resp.status_code}): {resp.text}")


def delete_api_key(base_url, key_id, username=None, password=None):
    """登录后删除指定 API Key（删除即吊销，供权限/吊销测试使用）"""
    session = _web_login(base_url, username, password)
    resp = session.delete(f"{base_url}/web/apikeys/{key_id}", timeout=10)
    if resp.status_code != 200:
        raise RuntimeError(f"删除 API Key 失败 ({resp.status_code}): {resp.text}")


class SSEClient:
    """SSE 流式响应客户端

    解析设计文档 3.6 节定义的 SSE 协议：
    - 格式: data: <JSON>\\n\\n，无 event: 行
    - 事件类型由 JSON 字段推断：thinking / message / tool_calls / finish / tool_result / error
    - [DONE] 为流结束标记
    """

    def __init__(self, response):
        self.response = response
        self.events = []
        self._parse_events()

    def _parse_events(self):
        """解析 SSE 事件流（仅 data: 行格式）"""
        content = self.response.text
        lines = content.split("\n")

        for line in lines:
            line = line.strip()
            if not line:
                continue

            if line.startswith("data:"):
                data_str = line[5:].strip()

                if data_str == "[DONE]":
                    continue  # 流结束标记，不是事件

                try:
                    data = json.loads(data_str)
                except json.JSONDecodeError:
                    continue

                event_type = self._infer_event_type(data)
                self.events.append({
                    "event": event_type,
                    "data": data,
                })

    @staticmethod
    def _infer_event_type(data: dict) -> str:
        """根据 JSON 字段推断 SSE 事件类型（见 API 设计文档 3.6 节）"""
        if data.get("event") == "error":
            return "error"

        role = data.get("role", "")

        if role == "tool":
            return "tool_result"

        if "tool_calls" in data:
            return "tool_calls"

        if "reasoning_content" in data:
            return "thinking"

        if "finish_reason" in data:
            return "finish"

        # content / 其他 assistant 消息
        return "message"

    def get_events_by_type(self, event_type: str) -> List[Dict]:
        """获取指定类型的所有事件"""
        return [e for e in self.events if e["event"] == event_type]

    def get_event_order(self) -> List[str]:
        """获取事件类型顺序"""
        return [e["event"] for e in self.events]

    def verify_event_order(self) -> bool:
        """验证事件顺序是否正确（新协议）"""
        order = self.get_event_order()
        if not order:
            return False

        # 至少有一个有意义的事件（message / tool_calls / thinking / finish / error）
        valid_events = {"message", "tool_calls", "thinking", "finish", "error"}
        if not any(e in order for e in valid_events):
            return False

        # tool_calls 和 tool_result: 如果有 tool_result，前面必须有 tool_calls
        tool_calls_indices = [i for i, e in enumerate(order) if e == "tool_calls"]
        tool_results_indices = [i for i, e in enumerate(order) if e == "tool_result"]

        # 每个 tool_result 前至少有一个 tool_calls（tool_calls 可能比 tool_result 多，比如调用失败）
        for tr_idx in tool_results_indices:
            if not any(tc_idx < tr_idx for tc_idx in tool_calls_indices):
                return False

        # finish 或 error 事件应该存在（终态）
        if "finish" not in order and "error" not in order:
            return False

        # error 事件只能出现在最后（终态）
        error_indices = [i for i, e in enumerate(order) if e == "error"]
        if error_indices and error_indices[-1] != len(order) - 1:
            return False

        return True

    def get_completed_event(self) -> Optional[Dict]:
        """获取 finish 事件（兼容旧名）"""
        events = self.get_events_by_type("finish")
        return events[0] if events else None

    def get_started_event(self) -> Optional[Dict]:
        """获取首个 message 事件（新协议无 started 事件）"""
        events = self.get_events_by_type("message")
        return events[0] if events else None

    def get_intent_event(self) -> Optional[Dict]:
        """获取首个 message 事件（兼容旧测试）"""
        return self.get_started_event()

    def get_all_steps(self) -> List[Dict]:
        """获取所有 thinking 和 tool_calls 事件"""
        return self.get_events_by_type("thinking") + self.get_events_by_type("tool_calls")

    def get_thinking_events(self) -> List[Dict]:
        """获取所有 thinking 事件"""
        return self.get_events_by_type("thinking")

    def get_tool_calls(self) -> List[Dict]:
        """获取所有 tool_calls 事件"""
        return self.get_events_by_type("tool_calls")

    def get_tool_results(self) -> List[Dict]:
        """获取所有 tool_result 事件"""
        return self.get_events_by_type("tool_result")

    def get_message_events(self) -> List[Dict]:
        """获取所有 message 事件"""
        return self.get_events_by_type("message")


@pytest.fixture(scope="session")
def server():
    """启动测试服务器（session级别）"""
    # 创建测试目录（无论服务器是否已运行）
    # 模型/记忆/调度等数据均已入库（{GROOT_HOME}/groot.db），无需创建 memory、schedules 目录
    os.makedirs(TEST_HOME, exist_ok=True)
    # 固定目录：skills, mcp（{home}/api 目录已无代码引用，不再创建）
    os.makedirs(f"{TEST_HOME}/skills", exist_ok=True)
    os.makedirs(f"{TEST_HOME}/mcp", exist_ok=True)
    os.makedirs(f"{TEST_HOME}/logs", exist_ok=True)

    # 写入测试配置（无论服务器是否已运行，确保配置正确）
    # 注意：模型只存数据库（配置文件 llm 节已失效），通过 ensure_default_model 建模型
    config = {
        "agent": {"name": "groot", "version": "1.0.0"},
        "server": {"host": "0.0.0.0", "port": int(TEST_PORT)},
        # skills/mcp/api directory 已移除配置，使用固定位置
        # 认证始终开启：只需配置请求头名与 JWT 签名密钥
        "security": {
            "auth": {
                "header_name": "X-API-Key",
                "secret": TEST_AUTH_SECRET
            }
        },
        "memory": {
            "history_window": 20
        },
        "schedule": {
            "enabled": True,
            "max_concurrent_tasks": 10,
            "sync_interval": "30s"
        },
        "message": {
            "queue_size": 10,
            "workers": 1,
            "senders": {
                "webhook": {"enabled": False, "url": ""},
                "email": {"enabled": False, "smtp_host": "", "smtp_port": 587, "username": "", "password": "", "from": ""}
            }
        },
        "logging": {"level": "debug", "format": "json", "output": ["stdout"]},
        "attachment": {
            "max_size": 50,
            "max_total_size": 100,
            "max_count": 10,
            "allowed_types": ["pdf", "doc", "docx", "txt", "json", "csv", "xml", "yaml", "png", "jpg", "jpeg", "zip"]
        }
    }

    import yaml
    with open(f"{TEST_HOME}/config.yaml", "w") as f:
        yaml.dump(config, f)

    # 检查服务器是否已运行
    if wait_for_server(timeout=5):
        # 服务器已运行，需要重启以使用新配置。
        # 只按"dist/groot -p 测试端口"精确匹配杀进程，
        # 禁止 pkill -f groot（会误杀开发环境或其他端口的实例）
        try:
            subprocess.run(
                ["pkill", "-f", f"dist/groot -p {TEST_PORT}"],
                check=False, capture_output=True,
            )
            time.sleep(2)
        except Exception:
            pass

        # 再次检查
        if wait_for_server(timeout=5):
            # 确保模型库中有默认模型（模型只存数据库）
            ensure_default_model(BASE_URL)
            yield BASE_URL
            return

    # 启动 groot 进程
    env = os.environ.copy()
    env["GROOT_HOME"] = TEST_HOME

    process = subprocess.Popen(
        [GROOT_BIN, "-p", TEST_PORT],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE
    )

    # 等待服务器启动
    if not wait_for_server(timeout=30):
        process.kill()
        raise RuntimeError("服务器启动失败")

    # 确保模型库中有默认模型（模型只存数据库，空库时 /chat 返回 400 invalid_model）
    ensure_default_model(BASE_URL)

    yield BASE_URL

    # 清理：停止服务器
    process.send_signal(signal.SIGTERM)
    process.wait(timeout=10)


@pytest.fixture(scope="session")
def api_key(server):
    """session 级 API Key（all 权限）：通过 Web 端点创建，返回 JWT token 字符串"""
    return bootstrap_api_key(BASE_URL)


@pytest.fixture
def api_headers(api_key):
    """标准 API Headers（携带 JWT API Key）"""
    return {
        "Content-Type": "application/json",
        "X-API-Key": api_key
    }


@pytest.fixture
def no_auth_headers():
    """无认证 Headers"""
    return {"Content-Type": "application/json"}


@pytest.fixture
def invalid_auth_headers():
    """无效认证 Headers：格式像 JWT 但签名无效"""
    return {
        "Content-Type": "application/json",
        "X-API-Key": "eyJhbGciOiJIUzI1NiJ9.invalid.invalid"
    }


@pytest.fixture
def test_file_base64():
    """生成测试文件的 Base64 编码"""
    content = "name,age,city\nAlice,25,Beijing\nBob,30,Shanghai\n"
    return base64.b64encode(content.encode()).decode()


@pytest.fixture
def large_file_base64():
    """生成超过大小限制的文件 Base64"""
    # 生成约 60MB 的内容（超过默认50MB限制）
    content = "x" * (60 * 1024 * 1024)
    return base64.b64encode(content.encode()).decode()


@pytest.fixture
def pdf_file_base64():
    """生成 PDF 文件 Base64"""
    # 模拟 PDF 文件头
    pdf_header = "%PDF-1.4\n%\xe2\xe3\xcf\xd3\n1 0 obj\n<</Type/Catalog/Pages 2 0 R>>\nendobj\n"
    return base64.b64encode(pdf_header.encode()).decode()


@pytest.fixture
def session_id_generator():
    """session_id 生成器"""
    return generate_session_id


@pytest.fixture
def cleanup_memory():
    """历史遗留 fixture：会话记忆已入库（groot.db），不再有 memory/ 目录可清理。

    保留 fixture 名以兼容既有用例引用，实际为 no-op。
    """
    yield


@pytest.fixture
def mock_skill():
    """创建测试 Skill"""
    skill_dir = f"{TEST_HOME}/skills/test_skill"
    os.makedirs(skill_dir, exist_ok=True)

    skill_content = """---
name: test_skill
description: "测试用的Skill"
---

# Test Skill

这是一个测试用的Skill。
"""

    skill_file = f"{skill_dir}/SKILL.md"
    with open(skill_file, "w") as f:
        f.write(skill_content)

    yield skill_file

    # 清理
    if os.path.exists(skill_dir):
        import shutil
        shutil.rmtree(skill_dir)


@pytest.fixture
def mock_mcp_config():
    """创建测试 MCP 配置"""
    mcp_file = f"{TEST_HOME}/mcp/test_mcp.json"

    mcp_config = {
        "name": "test_mcp",
        "type": "stdio",
        "description": "测试MCP",
        "isActive": True,
        "command": "echo",
        "args": ["test"]
    }

    with open(mcp_file, "w") as f:
        json.dump(mcp_config, f)

    yield mcp_file

    # 清理
    if os.path.exists(mcp_file):
        os.remove(mcp_file)


def assert_session_id_format(session_id: str):
    """验证 session_id 格式"""
    assert session_id, "session_id 不能为空"
    # 新格式：{YYYYMMDDHHMMSSmmm}_{random4}（无 sess_ 前缀）
    assert not session_id.startswith("sess_"), "session_id 不应有 sess_ 前缀"
    parts = session_id.split("_")
    assert len(parts) == 2, "session_id 应包含时间戳和随机部分"
    timestamp, random_part = parts
    assert len(timestamp) == 17, f"时间戳应为17位，实际: {len(timestamp)}"
    assert timestamp.isdigit(), "时间戳应为数字"
    assert len(random_part) == 4, f"随机部分应为4位，实际: {len(random_part)}"


def assert_chat_id_format(chat_id: str):
    """验证 chat_id 格式：{YYYYMMDDHHMMSSmmm}，17 位纯数字（见 memory/idgen.go GenerateChatID）"""
    assert chat_id, "chat_id 不能为空"
    assert not chat_id.startswith("chat_"), "chat_id 不应有 chat_ 前缀"
    assert len(chat_id) == 17, f"chat_id 应为17位时间戳，实际: {len(chat_id)}"
    assert chat_id.isdigit(), "chat_id 应为纯数字"


def assert_step_id_format(step_id: str):
    """验证 step_id 格式"""
    assert step_id, "step_id 不能为空"
    # 新格式：{YYYYMMDD}-{HHMMSSmmm}-{random6}
    parts = step_id.split("-")
    assert len(parts) == 3, f"step_id 应有3部分（日期-时间-随机），实际: {len(parts)}"
    date_part, time_part, random_part = parts
    assert len(date_part) == 8, f"日期部分应为8位，实际: {len(date_part)}"
    assert date_part.isdigit(), "日期部分应为数字"
    assert len(time_part) == 9, f"时间部分应为9位，实际: {len(time_part)}"
    assert time_part.isdigit(), "时间部分应为数字"
    assert len(random_part) == 6, f"随机部分应为6位，实际: {len(random_part)}"