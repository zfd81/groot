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
TEST_API_KEY = os.environ.get("GROOT_TEST_API_KEY", "test-api-key-2026")
TEST_HOME = os.environ.get("GROOT_TEST_HOME", "/tmp/groot_test")
GROOT_BIN = os.environ.get("GROOT_BIN", os.path.join(os.path.dirname(os.path.dirname(__file__)), "bin", "groot"))

BASE_URL = f"http://{TEST_HOST}:{TEST_PORT}"


def generate_session_id() -> str:
    """生成测试用的 session_id（符合新格式）"""
    timestamp = datetime.now().strftime("%Y%m%d%H%M%S%f")[:17]  # YYYYMMDDHHMMSSmmm
    random_part = "".join([chr(c) for c in [ord("a") + i % 26 for i in range(4)]])
    return f"{timestamp}_{random_part}"


def generate_chat_id() -> str:
    """生成测试用的 chat_id"""
    timestamp = datetime.now().strftime("%Y%m%d%H%M%S%f")[:17]
    return f"chat_{timestamp}"


def generate_step_id() -> str:
    """生成测试用的 step_id"""
    date_part = datetime.now().strftime("%Y%m%d")
    time_part = datetime.now().strftime("%H%M%S%f")[:9]  # HHMMSSmmm
    random_part = "".join([chr(c) for c in [ord("a") + i % 26 for i in range(6)]])
    return f"{date_part}-{time_part}-{random_part}"


def wait_for_server(timeout: int = 30) -> bool:
    """等待服务器启动"""
    start_time = time.time()
    while time.time() - start_time < timeout:
        try:
            response = requests.get(f"{BASE_URL}/health", timeout=2)
            if response.status_code == 200:
                return True
        except:
            pass
        time.sleep(1)
    return False


class SSEClient:
    """SSE 流式响应客户端"""

    def __init__(self, response):
        self.response = response
        self.events = []
        self._parse_events()

    def _parse_events(self):
        """解析 SSE 事件流"""
        content = self.response.text
        lines = content.split("\n")

        current_event = None
        current_data = None

        for line in lines:
            line = line.strip()
            if not line:
                if current_event and current_data:
                    self.events.append({
                        "event": current_event,
                        "data": json.loads(current_data) if current_data else {}
                    })
                    current_event = None
                    current_data = None
                continue

            if line.startswith("event:"):
                current_event = line[6:].strip()
            elif line.startswith("data:"):
                current_data = line[5:].strip()

        # 处理最后一个事件
        if current_event and current_data:
            self.events.append({
                "event": current_event,
                "data": json.loads(current_data) if current_data else {}
            })

    def get_events_by_type(self, event_type: str) -> List[Dict]:
        """获取指定类型的所有事件"""
        return [e for e in self.events if e["event"] == event_type]

    def get_event_order(self) -> List[str]:
        """获取事件类型顺序"""
        return [e["event"] for e in self.events]

    def verify_event_order(self) -> bool:
        """验证事件顺序是否正确（新事件系统）"""
        order = self.get_event_order()

        # started 必须是第一个（替代旧的intent）
        if not order or order[0] != "started":
            return False

        # completed 必须是最后一个
        if order[-1] != "completed":
            return False

        # thinking_start 和 thinking_end 应成对
        thinking_starts = [i for i, e in enumerate(order) if e == "thinking_start"]
        thinking_ends = [i for i, e in enumerate(order) if e == "thinking_end"]

        if len(thinking_starts) != len(thinking_ends):
            return False

        # thinking_end 必须在对应的 thinking_start 之后
        for i, start_idx in enumerate(thinking_starts):
            if i >= len(thinking_ends) or thinking_ends[i] < start_idx:
                return False

        # tool_call 和 tool_result 应成对
        tool_calls = [i for i, e in enumerate(order) if e == "tool_call"]
        tool_results = [i for i, e in enumerate(order) if e == "tool_result"]

        if len(tool_calls) != len(tool_results):
            return False

        # tool_result 必须在对应的 tool_call 之后
        for i, call_idx in enumerate(tool_calls):
            if i >= len(tool_results) or tool_results[i] < call_idx:
                return False

        return True

    def get_completed_event(self) -> Optional[Dict]:
        """获取 completed 事件"""
        events = self.get_events_by_type("completed")
        return events[0] if events else None

    def get_started_event(self) -> Optional[Dict]:
        """获取 started 事件（替代旧的intent）"""
        events = self.get_events_by_type("started")
        return events[0] if events else None

    def get_intent_event(self) -> Optional[Dict]:
        """获取 intent 事件（兼容旧测试）"""
        # 尝试获取started事件，如果没有则获取intent事件
        started = self.get_started_event()
        if started:
            return started
        events = self.get_events_by_type("intent")
        return events[0] if events else None

    def get_all_steps(self) -> List[Dict]:
        """获取所有 step_start 事件（兼容旧测试）"""
        # 新事件系统中没有step_start，返回thinking_start和tool_call
        thinking_starts = self.get_events_by_type("thinking_start")
        tool_calls = self.get_events_by_type("tool_call")
        return thinking_starts + tool_calls

    def get_thinking_events(self) -> List[Dict]:
        """获取所有 thinking 事件"""
        return self.get_events_by_type("thinking")

    def get_tool_calls(self) -> List[Dict]:
        """获取所有 tool_call 事件"""
        return self.get_events_by_type("tool_call")

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
    os.makedirs(TEST_HOME, exist_ok=True)
    os.makedirs(f"{TEST_HOME}/skills", exist_ok=True)
    os.makedirs(f"{TEST_HOME}/mcp", exist_ok=True)
    os.makedirs(f"{TEST_HOME}/memory", exist_ok=True)
    os.makedirs(f"{TEST_HOME}/logs", exist_ok=True)

    # 写入测试配置（无论服务器是否已运行，确保配置正确）
    config = {
        "agent": {"name": "groot", "version": "1.0.0"},
        "server": {"host": "0.0.0.0", "port": int(TEST_PORT)},
        "llm": {
            "active_model": "mock-model",
            "models": {
                "mock-model": {
                    "base_url": "http://localhost:8888/mock",
                    "api_key": "mock-key",
                    "model": "mock",
                    "max_tokens": 4096,
                    "temperature": 0.7
                }
            }
        },
        "skills": {
            "directory": "skills",
            "hot_reload": {
                "enabled": True,
                "debounce_delay": 2
            }
        },
        "mcp": {
            "directory": "mcp"
        },
        "security": {
            "auth": {
                "enabled": True,
                "type": "api_key",
                "api_key": {
                    "header_name": "X-API-Key",
                    "keys": [
                        {"name": "test_client", "key": TEST_API_KEY, "permissions": ["all"]}
                    ]
                }
            }
        },
        "memory": {
            "directory": "memory",
            "retention_days": 1,
            "cleanup_schedule": "02:00"
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
        # 服务器已运行，需要重启以使用新配置
        # 尝试通过发送信号停止
        try:
            # 通过 API 尝试关闭（如果有 shutdown endpoint）
            # 否则使用 pkill
            subprocess.run(["pkill", "-f", "groot"], check=False, capture_output=True)
            time.sleep(2)
        except Exception:
            pass

        # 再次检查
        if wait_for_server(timeout=5):
            yield BASE_URL
            return

    # 启动 groot 进程
    env = os.environ.copy()
    env["GROOT_HOME"] = TEST_HOME
    env["GROOT_API_KEY"] = TEST_API_KEY

    process = subprocess.Popen(
        [GROOT_BIN, "-H", TEST_HOME, "-p", TEST_PORT],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE
    )

    # 等待服务器启动
    if not wait_for_server(timeout=30):
        process.kill()
        raise RuntimeError("服务器启动失败")

    yield BASE_URL

    # 清理：停止服务器
    process.send_signal(signal.SIGTERM)
    process.wait(timeout=10)


@pytest.fixture
def api_headers():
    """标准 API Headers"""
    return {
        "Content-Type": "application/json",
        "X-API-Key": TEST_API_KEY
    }


@pytest.fixture
def no_auth_headers():
    """无认证 Headers"""
    return {"Content-Type": "application/json"}


@pytest.fixture
def invalid_auth_headers():
    """无效认证 Headers"""
    return {
        "Content-Type": "application/json",
        "X-API-Key": "invalid-key-12345"
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
    """清理测试 memory 目录"""
    memory_dir = f"{TEST_HOME}/memory"

    yield

    # 测试后清理
    if os.path.exists(memory_dir):
        for session_dir in os.listdir(memory_dir):
            session_path = os.path.join(memory_dir, session_dir)
            if os.path.isdir(session_path):
                import shutil
                shutil.rmtree(session_path)


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
    """验证 chat_id 格式"""
    assert chat_id, "chat_id 不能为空"
    assert chat_id.startswith("chat_"), "chat_id 应以 chat_ 开头"
    timestamp_part = chat_id[5:]  # 去掉 "chat_" 前缀
    assert len(timestamp_part) == 17, f"时间戳部分应为17位，实际: {len(timestamp_part)}"
    assert timestamp_part.isdigit(), "时间戳部分应为数字"


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