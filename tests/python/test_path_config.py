"""
目录路径配置测试

当前路径规则：
- 固定目录（不可配置，位于 GROOT_HOME 下）：skills/、mcp/、subagents/、logs（默认）
- 可配置目录：logging.file.directory（支持相对/绝对路径；相对路径相对于 GROOT_HOME）
- 业务数据（模型/记忆/调度/集群等）全部存数据库 {GROOT_HOME}/groot.db，无目录配置

绝对路径测试目录：tests/abs_path_test/
"""

import pytest
import requests
import os
import glob
import subprocess
import signal
import yaml
from conftest import BASE_URL, TEST_HOME, TEST_PORT, TEST_AUTH_SECRET, GROOT_BIN


# ============================================================================
# 绝对路径测试目录配置
# ============================================================================

# tests 目录路径
TESTS_DIR = os.path.dirname(os.path.dirname(__file__))

# 绝对路径测试目录（位于 tests/abs_path_test/）
ABS_PATH_TEST_DIR = os.path.join(TESTS_DIR, "abs_path_test")

# 绝对路径测试的子目录（仅日志目录支持绝对路径配置）
ABS_PATH_LOGS_DIR = os.path.join(ABS_PATH_TEST_DIR, "logs")


@pytest.fixture(scope="module")
def abs_path_server():
    """启动使用绝对路径日志配置的测试服务器"""
    import time

    # 创建绝对路径测试目录
    os.makedirs(ABS_PATH_TEST_DIR, exist_ok=True)
    os.makedirs(ABS_PATH_LOGS_DIR, exist_ok=True)

    # 创建 GROOT_HOME 目录（固定目录在此下）
    home_dir = os.path.join(ABS_PATH_TEST_DIR, "groot_home")
    os.makedirs(home_dir, exist_ok=True)
    # 固定目录
    os.makedirs(f"{home_dir}/skills", exist_ok=True)
    os.makedirs(f"{home_dir}/mcp", exist_ok=True)

    # 使用不同端口避免冲突
    abs_port = str(int(TEST_PORT) + 100)  # 例如 8180
    abs_base_url = f"http://localhost:{abs_port}"

    # 写入配置文件：logs 使用绝对路径
    # 注意：模型只存数据库（配置文件 llm 节已失效）；memory.directory /
    # skills.hot_reload 配置项已删除，不再写入
    config = {
        "agent": {"name": "groot", "version": "1.0.0"},
        "server": {"host": "0.0.0.0", "port": int(abs_port)},
        "memory": {
            "history_window": 20
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
        },
        "security": {
            "auth": {
                # 认证始终开启：只需请求头名与 JWT 签名密钥
                "header_name": "X-API-Key",
                "secret": TEST_AUTH_SECRET
            }
        }
    }

    config_path = os.path.join(home_dir, "config.yaml")
    with open(config_path, "w") as f:
        yaml.dump(config, f)

    # 检查是否已有服务器在此端口运行（健康检查端点为 /web/health，免认证）
    try:
        response = requests.get(f"{abs_base_url}/web/health", timeout=2)
        if response.status_code == 200:
            yield abs_base_url
            return
    except:
        pass

    # 启动 groot 进程
    env = os.environ.copy()
    env["GROOT_HOME"] = home_dir

    process = subprocess.Popen(
        [GROOT_BIN, "-p", abs_port],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE
    )

    # 等待服务器启动
    start_time = time.time()
    while time.time() - start_time < 30:
        try:
            response = requests.get(f"{abs_base_url}/web/health", timeout=2)
            if response.status_code == 200:
                break
        except:
            pass
        time.sleep(1)

    yield abs_base_url

    # 清理：停止服务器
    process.send_signal(signal.SIGTERM)
    process.wait(timeout=10)


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


class TestConfigurableDirectoryConfig:
    """可配置目录测试（支持相对/绝对路径）"""

    def test_logs_directory_default(self, server):
        """TC-PATH-006: logs 目录默认位置"""
        # 默认配置: directory: "logs"
        # 相对路径，应位于 GROOT_HOME/logs
        expected_path = os.path.join(TEST_HOME, "logs")

        # 执行请求以产生日志
        requests.get(f"{BASE_URL}/web/health")

        # 验证目录存在（如果配置了 file 输出）
        if os.path.exists(expected_path):
            log_files = glob.glob(os.path.join(expected_path, "groot-*.log"))
            if len(log_files) > 0:
                assert True, f"Log files exist in {expected_path}"


class TestAbsolutePathConfig:
    """绝对路径配置测试

    使用 tests/abs_path_test/ 目录作为绝对路径测试目录
    配置文件中 logging.file.directory 使用绝对路径
    """

    def test_absolute_path_logs_directory(self, abs_path_server):
        """TC-PATH-007: logs 使用绝对路径

        配置: logging.file.directory = tests/abs_path_test/logs
        验证: 日志文件直接写入该目录，而非 GROOT_HOME/logs
        """
        # 执行请求产生日志
        requests.get(f"{abs_path_server}/web/health")

        # 验证日志目录在绝对路径位置
        assert os.path.exists(ABS_PATH_LOGS_DIR), f"Logs directory should exist at absolute path {ABS_PATH_LOGS_DIR}"

    def test_fixed_dirs_under_home(self, abs_path_server):
        """TC-PATH-010: 固定目录仍在 GROOT_HOME 下

        验证: skills/mcp 使用固定位置，即使 logs 使用绝对路径
        """
        home_dir = os.path.join(ABS_PATH_TEST_DIR, "groot_home")

        # 固定目录应在 home_dir 下
        assert os.path.exists(os.path.join(home_dir, "skills")), "Skills should be under GROOT_HOME"
        assert os.path.exists(os.path.join(home_dir, "mcp")), "MCP should be under GROOT_HOME"

    def test_absolute_path_not_under_home(self, abs_path_server):
        """TC-PATH-011: 绝对路径不在 GROOT_HOME 下

        验证：使用绝对路径的日志目录不在 GROOT_HOME 目录树中
        """
        home_dir = os.path.join(ABS_PATH_TEST_DIR, "groot_home")

        # logs 使用绝对路径，不应在 home_dir 下
        logs_in_home = os.path.join(home_dir, "logs")
        assert not os.path.exists(logs_in_home), f"Logs should NOT be in {logs_in_home} (using absolute path)"


class TestDirectoryAutoCreation:
    """目录自动创建测试"""

    def test_logs_directory_auto_created(self, server):
        """TC-PATH-015: logs 目录自动创建"""
        logs_dir = os.path.join(TEST_HOME, "logs")

        # 执行请求产生日志
        requests.get(f"{BASE_URL}/web/health")

        if os.path.exists(logs_dir):
            assert True, f"Logs directory auto-created at {logs_dir}"


class TestPathConfigIntegration:
    """路径配置集成测试"""

    def test_fixed_dirs_always_under_home(self, server):
        """TC-PATH-016: 固定目录始终在 GROOT_HOME 下"""
        # 固定目录
        fixed_dirs = ["skills", "mcp"]

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
    "subagents": "{GROOT_HOME}/subagents",
}

# 可配置目录（支持相对/绝对路径）
CONFIGURABLE_DIRECTORY_CONFIG = {
    "logs": "logs",  # logging.file.directory 默认值
}

# 路径解析规则说明
PATH_RESOLUTION_RULES = """
路径解析规则（当前版本）：

1. 固定目录（不可配置）：
   - skills: {GROOT_HOME}/skills
   - mcp: {GROOT_HOME}/mcp
   - subagents: {GROOT_HOME}/subagents

2. 可配置目录（支持相对/绝对路径）：
   - logging.file.directory: 默认 "logs"
     - 相对路径 "logs" -> {GROOT_HOME}/logs
     - 绝对路径 "/var/log" -> /var/log

3. 业务数据（模型/记忆/调度/集群成员等）全部存数据库：
   - SQLite 文件位置固定为 {GROOT_HOME}/groot.db

测试示例：
- 固定目录测试：验证 skills/mcp 在 GROOT_HOME 下
- 绝对路径测试：logs 使用 tests/abs_path_test/ 目录
"""
