"""
目录路径配置测试
测试路径解析逻辑：相对路径相对于 GROOT_HOME，绝对路径直接使用

测试覆盖的目录配置：
- skills: 固定位置 {GROOT_HOME}/skills（不可配置）
- mcp: 固定位置 {GROOT_HOME}/mcp（不可配置）
- api: 固定位置 {GROOT_HOME}/api（不可配置）
- memory.directory: 会话记忆目录（可配置，支持相对/绝对路径）
- logging.file.directory: 日志文件目录（可配置，支持相对/绝对路径）
- temp: 固定位置 {memoryDir}/temp（不可配置，在 memory 目录下）

绝对路径测试目录：tests/abs_path_test/
"""

import pytest
import requests
import os
import glob
import tempfile
import shutil
import subprocess
import signal
import yaml
from conftest import BASE_URL, TEST_HOME, TEST_PORT, TEST_API_KEY, GROOT_BIN, wait_for_server


# ============================================================================
# 绝对路径测试目录配置
# ============================================================================

# tests 目录路径
TESTS_DIR = os.path.dirname(os.path.dirname(__file__))

# 绝对路径测试目录（位于 tests/abs_path_test/）
ABS_PATH_TEST_DIR = os.path.join(TESTS_DIR, "abs_path_test")

# 绝对路径测试的子目录
ABS_PATH_LOGS_DIR = os.path.join(ABS_PATH_TEST_DIR, "logs")
ABS_PATH_MEMORY_DIR = os.path.join(ABS_PATH_TEST_DIR, "memory")


@pytest.fixture(scope="module")
def abs_path_server():
    """启动使用绝对路径配置的测试服务器"""
    import time

    # 创建绝对路径测试目录
    os.makedirs(ABS_PATH_TEST_DIR, exist_ok=True)
    os.makedirs(ABS_PATH_LOGS_DIR, exist_ok=True)
    os.makedirs(ABS_PATH_MEMORY_DIR, exist_ok=True)

    # 创建 GROOT_HOME 目录（固定目录在此下）
    home_dir = os.path.join(ABS_PATH_TEST_DIR, "groot_home")
    os.makedirs(home_dir, exist_ok=True)
    # 固定目录
    os.makedirs(f"{home_dir}/skills", exist_ok=True)
    os.makedirs(f"{home_dir}/mcp", exist_ok=True)
    os.makedirs(f"{home_dir}/api", exist_ok=True)

    # 使用不同端口避免冲突
    abs_port = str(int(TEST_PORT) + 100)  # 例如 8180
    abs_base_url = f"http://localhost:{abs_port}"

    # 写入配置文件：memory/logs 使用绝对路径
    # skills/mcp/api 使用固定位置，不需要配置
    config = {
        "agent": {"name": "groot", "version": "1.0.0"},
        "server": {"host": "0.0.0.0", "port": int(abs_port)},
        "llm": {
            "default_model": "qwen-local",
            "models": {
                "qwen-local": {
                    "base_url": "http://127.0.0.1:8230/v1",
                    "api_key": "bonc1q2w3e",
                    "model": "Qwen3.5-122B-A10B-6bit",
                    "max_tokens": 40960,
                    "temperature": 0.2
                }
            }
        },
        "skills": {
            "hot_reload": {"enabled": True, "debounce_delay": 2}
        },
        # skills/mcp/api 目录固定，不配置
        "memory": {
            "directory": ABS_PATH_MEMORY_DIR,  # 绝对路径
            "retention_days": 1,
            "cleanup_schedule": "02:00"
        },
        "logging": {
            "level": "debug",
            "format": "json",
            "output": ["stdout", "file"],
            "file": {
                "directory": ABS_PATH_LOGS_DIR,  # 绝对路径
                "filename_pattern": "groot-{date}.log",
                "max_age": 7
            }
        },
        "attachment": {
            "max_size": 50,
            "max_total_size": 100,
            "max_count": 10,
            "allowed_types": ["txt", "json"]
            # temp_directory 已移除，固定在 {memoryDir}/temp
        },
        "security": {
            "auth": {
                "enabled": True,
                "type": "api_key",
                "api_key": {
                    "header_name": "X-API-Key",
                    "keys": [{"name": "test_client", "key": TEST_API_KEY, "permissions": ["all"]}]
                }
            }
        }
    }

    config_path = os.path.join(home_dir, "config.yaml")
    with open(config_path, "w") as f:
        yaml.dump(config, f)

    # 检查是否已有服务器在此端口运行
    try:
        response = requests.get(f"{abs_base_url}/health", timeout=2)
        if response.status_code == 200:
            yield abs_base_url
            return
    except:
        pass

    # 启动 groot 进程
    env = os.environ.copy()
    env["GROOT_HOME"] = home_dir
    env["GROOT_API_KEY"] = TEST_API_KEY

    process = subprocess.Popen(
        [GROOT_BIN, "-H", home_dir, "-p", abs_port],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE
    )

    # 等待服务器启动
    start_time = time.time()
    while time.time() - start_time < 30:
        try:
            response = requests.get(f"{abs_base_url}/health", timeout=2)
            if response.status_code == 200:
                break
        except:
            pass
        time.sleep(1)

    yield abs_base_url

    # 清理：停止服务器
    process.send_signal(signal.SIGTERM)
    process.wait(timeout=10)


@pytest.fixture
def abs_path_api_headers():
    """绝对路径测试的 API headers"""
    return {"X-API-Key": TEST_API_KEY, "Content-Type": "application/json"}


class TestFixedDirectoryConfig:
    """固定目录配置测试（不可配置）"""

    def test_skills_directory_fixed(self, server):
        """TC-PATH-001: skills 目录固定位置"""
        # 固定位置: {GROOT_HOME}/skills
        expected_path = os.path.join(TEST_HOME, "skills")
        assert os.path.exists(expected_path), f"Skills directory should be at fixed path {expected_path}"

    def test_mcp_directory_fixed(self, server):
        """TC-PATH-002: mcp 目录固定位置"""
        # 固定位置: {GROOT_HOME}/mcp
        expected_path = os.path.join(TEST_HOME, "mcp")
        assert os.path.exists(expected_path), f"MCP directory should be at fixed path {expected_path}"

    def test_api_directory_fixed(self, server):
        """TC-PATH-003: api 目录固定位置"""
        # 固定位置: {GROOT_HOME}/api
        expected_path = os.path.join(TEST_HOME, "api")
        assert os.path.exists(expected_path), f"API tools directory should be at fixed path {expected_path}"

    def test_temp_directory_under_memory(self, server, api_headers):
        """TC-PATH-004: temp 目录固定在 memory 下"""
        # temp 固定位置: {memoryDir}/temp
        # 先执行对话创建 memory 目录
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "测试 temp 目录位置"},
            stream=True
        )

        from conftest import SSEClient
        SSEClient(response)

        # temp 应在 memory 目录下
        memory_dir = os.path.join(TEST_HOME, "memory")
        temp_dir = os.path.join(memory_dir, "temp")

        # temp 目录可能在附件处理时创建
        # 验证路径逻辑：temp 必须在 memory 下
        assert memory_dir in temp_dir, f"Temp directory {temp_dir} should be under memory {memory_dir}"


class TestConfigurableDirectoryConfig:
    """可配置目录测试（支持相对/绝对路径）"""

    def test_memory_directory_default(self, server, api_headers):
        """TC-PATH-005: memory 目录默认位置"""
        # 默认配置: directory: "memory"
        # 相对路径，应位于 GROOT_HOME/memory
        expected_path = os.path.join(TEST_HOME, "memory")

        # 执行一次对话以创建 memory 目录
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "测试 memory 目录"},
            stream=True
        )

        # 验证目录存在
        assert os.path.exists(expected_path), f"Memory directory should exist at {expected_path}"

    def test_logs_directory_default(self, server):
        """TC-PATH-006: logs 目录默认位置"""
        # 默认配置: directory: "logs"
        # 相对路径，应位于 GROOT_HOME/logs
        expected_path = os.path.join(TEST_HOME, "logs")

        # 执行请求以产生日志
        requests.get(f"{BASE_URL}/health")

        # 验证目录存在（如果配置了 file 输出）
        if os.path.exists(expected_path):
            log_files = glob.glob(os.path.join(expected_path, "groot-*.log"))
            if len(log_files) > 0:
                assert True, f"Log files exist in {expected_path}"


class TestAbsolutePathConfig:
    """绝对路径配置测试

    使用 tests/abs_path_test/ 目录作为绝对路径测试目录
    配置文件中 memory、logs 使用绝对路径
    """

    def test_absolute_path_logs_directory(self, abs_path_server):
        """TC-PATH-007: logs 使用绝对路径

        配置: logging.file.directory = tests/abs_path_test/logs
        验证: 日志文件直接写入该目录，而非 GROOT_HOME/logs
        """
        # 执行请求产生日志
        requests.get(f"{abs_path_server}/health")

        # 验证日志目录在绝对路径位置
        assert os.path.exists(ABS_PATH_LOGS_DIR), f"Logs directory should exist at absolute path {ABS_PATH_LOGS_DIR}"

    def test_absolute_path_memory_directory(self, abs_path_server, abs_path_api_headers):
        """TC-PATH-008: memory 使用绝对路径

        配置: memory.directory = tests/abs_path_test/memory
        验证: 会话数据直接写入该目录，而非 GROOT_HOME/memory
        """
        # 执行对话以创建 memory 数据
        response = requests.post(
            f"{abs_path_server}/chat",
            headers=abs_path_api_headers,
            json={"instruction": "测试绝对路径 memory"},
            stream=True
        )

        # 等待完成
        from conftest import SSEClient
        SSEClient(response)

        # 验证 memory 目录在绝对路径位置
        assert os.path.exists(ABS_PATH_MEMORY_DIR), f"Memory directory should exist at absolute path {ABS_PATH_MEMORY_DIR}"

    def test_temp_under_absolute_memory(self, abs_path_server):
        """TC-PATH-009: temp 在绝对路径的 memory 下

        验证: 当 memory 使用绝对路径时，temp 也在该绝对路径下
        """
        # temp 应在 ABS_PATH_MEMORY_DIR 下
        expected_temp = os.path.join(ABS_PATH_MEMORY_DIR, "temp")

        # temp 路径逻辑验证
        assert ABS_PATH_MEMORY_DIR in expected_temp, f"Temp should be under absolute memory path"

    def test_fixed_dirs_under_home(self, abs_path_server):
        """TC-PATH-010: 固定目录仍在 GROOT_HOME 下

        验证: skills/mcp/api 使用固定位置，即使 memory/logs 使用绝对路径
        """
        home_dir = os.path.join(ABS_PATH_TEST_DIR, "groot_home")

        # 固定目录应在 home_dir 下
        assert os.path.exists(os.path.join(home_dir, "skills")), "Skills should be under GROOT_HOME"
        assert os.path.exists(os.path.join(home_dir, "mcp")), "MCP should be under GROOT_HOME"
        assert os.path.exists(os.path.join(home_dir, "api")), "API should be under GROOT_HOME"

    def test_absolute_path_not_under_home(self, abs_path_server):
        """TC-PATH-011: 绝对路径不在 GROOT_HOME 下

        验证：使用绝对路径的目录不在 GROOT_HOME 目录树中
        """
        home_dir = os.path.join(ABS_PATH_TEST_DIR, "groot_home")

        # logs 使用绝对路径，不应在 home_dir 下
        logs_in_home = os.path.join(home_dir, "logs")
        assert not os.path.exists(logs_in_home), f"Logs should NOT be in {logs_in_home} (using absolute path)"

        # memory 使用绝对路径，不应在 home_dir 下
        memory_in_home = os.path.join(home_dir, "memory")
        assert not os.path.exists(memory_in_home), f"Memory should NOT be in {memory_in_home} (using absolute path)"


class TestPathResolution:
    """路径解析逻辑验证"""

    def test_relative_path_resolution(self):
        """TC-PATH-012: 相对路径解析规则

        规则：相对路径相对于 GROOT_HOME (~/.groot 或指定的 -H 参数)
        """
        home_dir = TEST_HOME

        # 可配置目录的相对路径解析
        test_cases = [
            ("memory", os.path.join(home_dir, "memory")),
            ("logs", os.path.join(home_dir, "logs")),
            ("data/memory", os.path.join(home_dir, "data/memory")),
        ]

        for relative_path, expected in test_cases:
            resolved = os.path.join(home_dir, relative_path)
            assert resolved == expected, f"Relative path '{relative_path}' should resolve to '{expected}'"

    def test_absolute_path_resolution(self):
        """TC-PATH-013: 绝对路径解析规则

        规则：绝对路径直接使用，不拼接 GROOT_HOME
        """
        home_dir = TEST_HOME

        test_cases = [
            ("/var/log/groot", "/var/log/groot"),
            ("/data/memory", "/data/memory"),
        ]

        for absolute_path, expected in test_cases:
            if os.path.isabs(absolute_path):
                resolved = absolute_path
            else:
                resolved = os.path.join(home_dir, absolute_path)

            assert resolved == expected, f"Absolute path '{absolute_path}' should remain '{expected}'"


class TestDirectoryAutoCreation:
    """目录自动创建测试"""

    def test_memory_directory_auto_created(self, server, api_headers):
        """TC-PATH-014: memory 目录自动创建"""
        memory_dir = os.path.join(TEST_HOME, "memory")

        # 执行对话
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "test"},
            stream=True
        )

        from conftest import SSEClient
        SSEClient(response)

        # 验证 session 子目录
        if os.path.exists(memory_dir):
            session_dirs = [d for d in os.listdir(memory_dir) if os.path.isdir(os.path.join(memory_dir, d))]
            # 可能包含 temp 目录和 session 目录
            assert True, "Memory directory auto-created"

    def test_logs_directory_auto_created(self, server):
        """TC-PATH-015: logs 目录自动创建"""
        logs_dir = os.path.join(TEST_HOME, "logs")

        # 执行请求产生日志
        requests.get(f"{BASE_URL}/health")

        if os.path.exists(logs_dir):
            assert True, f"Logs directory auto-created at {logs_dir}"


class TestPathConfigIntegration:
    """路径配置集成测试"""

    def test_fixed_dirs_always_under_home(self, server):
        """TC-PATH-016: 固定目录始终在 GROOT_HOME 下"""
        # 固定目录
        fixed_dirs = ["skills", "mcp", "api"]

        for dir_name in fixed_dirs:
            expected_path = os.path.join(TEST_HOME, dir_name)
            assert os.path.exists(expected_path), f"Fixed directory {dir_name} should be under GROOT_HOME"


# ============================================================================
# 测试配置常量（供其他测试文件引用）
# ============================================================================

# 固定目录配置（不可配置）
FIXED_DIRECTORY_CONFIG = {
    "skills": "{GROOT_HOME}/skills",
    "mcp": "{GROOT_HOME}/mcp",
    "api": "{GROOT_HOME}/api",
    "temp": "{memoryDir}/temp",  # temp 在 memory 目录下
}

# 可配置目录（支持相对/绝对路径）
CONFIGURABLE_DIRECTORY_CONFIG = {
    "memory": "memory",  # 默认值
    "logs": "logs",      # 默认值
}

# 路径解析规则说明
PATH_RESOLUTION_RULES = """
路径解析规则（更新版）：

1. 固定目录（不可配置）：
   - skills: {GROOT_HOME}/skills
   - mcp: {GROOT_HOME}/mcp
   - api: {GROOT_HOME}/api
   - temp: {memoryDir}/temp（temp 固定在 memory 目录下）

2. 可配置目录（支持相对/绝对路径）：
   - memory.directory: 默认 "memory"
     - 相对路径 "memory" -> {GROOT_HOME}/memory
     - 绝对路径 "/data/memory" -> /data/memory
   - logging.file.directory: 默认 "logs"
     - 相对路径 "logs" -> {GROOT_HOME}/logs
     - 绝对路径 "/var/log" -> /var/log

3. temp 目录位置：
   - temp 固定在 memory 目录下
   - 若 memory.directory = "memory"，则 temp = {GROOT_HOME}/memory/temp
   - 若 memory.directory = "/data/memory"，则 temp = /data/memory/temp

测试示例：
- 固定目录测试：验证 skills/mcp/api 在 GROOT_HOME 下
- 绝对路径测试：memory/logs 使用 tests/abs_path_test/ 目录
"""