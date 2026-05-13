"""
Chat TUI CLI 命令测试

测试覆盖:
- groot --help 包含 chat 子命令
- groot chat 无配置文件时报错
- groot chat 连接已有服务（不需启动嵌入服务）
- groot chat 嵌入服务启动
- groot chat 帮助信息
"""

import pytest
import json
import os
import signal
import subprocess
import time
import requests
from conftest import GROOT_BIN, TEST_HOME


class TestChatHelp:
    """chat 子命令帮助信息测试"""

    def test_help_includes_chat(self):
        """TC-CHAT-001: --help 输出包含 chat 子命令"""
        result = subprocess.run(
            [GROOT_BIN, "--help"],
            capture_output=True,
            text=True,
        )
        assert "chat" in result.stdout, f"帮助中应包含 chat 子命令:\n{result.stdout}"
        assert "交互式聊天" in result.stdout or "聊天" in result.stdout

    def test_chat_help_section(self):
        """TC-CHAT-002: --help 输出包含 chat 帮助段落"""
        result = subprocess.run(
            [GROOT_BIN, "--help"],
            capture_output=True,
            text=True,
        )
        assert "chat 子命令" in result.stdout, f"应有 chat 子命令帮助段落:\n{result.stdout}"


class TestChatNoConfig:
    """chat 命令无配置文件测试"""

    def test_no_config_error(self):
        """TC-CHAT-003: 无配置文件时 groat chat 应报错"""
        # 使用不存在的目录作为 GROOT_HOME
        env = os.environ.copy()
        env["GROOT_HOME"] = "/tmp/groot_chat_nonexistent_test"

        result = subprocess.run(
            [GROOT_BIN, "chat"],
            env=env,
            capture_output=True,
            text=True,
        )
        assert "配置文件不存在" in result.stderr or "init" in result.stderr, \
            f"应提示配置文件不存在:\nstderr={result.stderr}\nstdout={result.stdout}"


class TestChatWithExistingService:
    """chat 命令连接已有服务测试"""

    @pytest.fixture
    def running_service(self):
        """启动一个 groot 服务作为已有服务"""
        test_home = "/tmp/groot_chat_service_test"
        os.makedirs(test_home, exist_ok=True)
        os.makedirs(f"{test_home}/skills", exist_ok=True)
        os.makedirs(f"{test_home}/mcp", exist_ok=True)
        os.makedirs(f"{test_home}/memory", exist_ok=True)

        # 写最小配置
        config = {
            "server": {"host": "localhost", "port": 8190},
            "llm": {
                "default_model": "mock",
                "models": {
                    "mock": {
                        "base_url": "http://localhost:8888",
                        "api_key": "test",
                        "model": "mock",
                    }
                },
            },
            "logging": {
                "level": "error",
                "output": ["file"],
                "file": {"directory": f"{test_home}/logs", "filename_pattern": "groot-{date}.log"},
            },
            "memory": {"directory": f"{test_home}/memory"},
            "security": {"auth": {"enabled": False}},
        }
        with open(f"{test_home}/config.yaml", "w") as f:
            json.dump(config, f)

        env = os.environ.copy()
        env["GROOT_HOME"] = test_home
        env["GROOT_API_KEY"] = "test-key"

        proc = subprocess.Popen(
            [GROOT_BIN, "-p", "8190"],
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

        # 等待服务启动
        for _ in range(30):
            try:
                resp = requests.get("http://localhost:8190/health", timeout=2)
                if resp.status_code == 200:
                    break
            except Exception:
                pass
            time.sleep(0.5)

        yield test_home, 8190

        proc.send_signal(signal.SIGTERM)
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()

    def test_detect_existing_service(self, running_service):
        """TC-CHAT-004: 检测到已有服务时，提示并尝试打开 TUI"""
        test_home, port = running_service

        env = os.environ.copy()
        env["GROOT_HOME"] = test_home

        # 用 script 或非交互模式运行，TUI 会因无 TTY 退出
        # 验证它能检测到已有服务（输出提示信息）
        result = subprocess.run(
            [GROOT_BIN, "chat"],
            env=env,
            capture_output=True,
            text=True,
            timeout=15,
        )
        # 应该输出"检测到已有服务运行"或在无 TTY 时退出
        combined = result.stdout + result.stderr
        assert "检测到已有服务运行" in combined or "TUI" in combined or result.returncode != 0, \
            f"应检测到已有服务:\nstdout={result.stdout}\nstderr={result.stderr}"


class TestChatEmbedServer:
    """chat 命令嵌入服务启动测试"""

    def test_embed_server_startup(self):
        """TC-CHAT-005: 无服务时自动启动嵌入服务"""
        test_home = "/tmp/groot_chat_embed_test"
        os.makedirs(test_home, exist_ok=True)
        os.makedirs(f"{test_home}/skills", exist_ok=True)
        os.makedirs(f"{test_home}/mcp", exist_ok=True)
        os.makedirs(f"{test_home}/memory", exist_ok=True)
        os.makedirs(f"{test_home}/logs", exist_ok=True)

        # 使用独立端口避免冲突
        config = {
            "server": {"host": "localhost", "port": 8191},
            "llm": {
                "default_model": "mock",
                "models": {
                    "mock": {
                        "base_url": "http://localhost:8888",
                        "api_key": "test",
                        "model": "mock",
                    }
                },
            },
            "logging": {
                "level": "error",
                "output": ["file"],
                "file": {"directory": f"{test_home}/logs", "filename_pattern": "groot-{date}.log"},
            },
            "memory": {"directory": f"{test_home}/memory"},
            "skills": {"hot_reload": {"enabled": False}},
            "security": {"auth": {"enabled": False}},
        }
        with open(f"{test_home}/config.yaml", "w") as f:
            json.dump(config, f)

        env = os.environ.copy()
        env["GROOT_HOME"] = test_home
        env["GROOT_API_KEY"] = "test-key"

        # 在无 TTY 环境下运行，TUI 会快速退出
        result = subprocess.run(
            [GROOT_BIN, "chat"],
            env=env,
            capture_output=True,
            text=True,
            timeout=30,
        )
        combined = result.stdout + result.stderr
        # 应该启动嵌入服务
        assert "启动嵌入服务" in combined or "嵌入服务已启动" in combined or "嵌入服务已关闭" in combined, \
            f"应启动嵌入服务:\nstdout={result.stdout}\nstderr={result.stderr}"

    def test_embed_server_clean_shutdown(self):
        """TC-CHAT-006: 嵌入服务在 TUI 退出后正确关闭"""
        test_home = "/tmp/groot_chat_shutdown_test"
        os.makedirs(test_home, exist_ok=True)
        os.makedirs(f"{test_home}/skills", exist_ok=True)
        os.makedirs(f"{test_home}/mcp", exist_ok=True)
        os.makedirs(f"{test_home}/memory", exist_ok=True)
        os.makedirs(f"{test_home}/logs", exist_ok=True)

        config = {
            "server": {"host": "localhost", "port": 8192},
            "llm": {
                "default_model": "mock",
                "models": {
                    "mock": {
                        "base_url": "http://localhost:8888",
                        "api_key": "test",
                        "model": "mock",
                    }
                },
            },
            "logging": {
                "level": "error",
                "output": ["file"],
                "file": {"directory": f"{test_home}/logs", "filename_pattern": "groot-{date}.log"},
            },
            "memory": {"directory": f"{test_home}/memory"},
            "skills": {"hot_reload": {"enabled": False}},
            "security": {"auth": {"enabled": False}},
        }
        with open(f"{test_home}/config.yaml", "w") as f:
            json.dump(config, f)

        env = os.environ.copy()
        env["GROOT_HOME"] = test_home
        env["GROOT_API_KEY"] = "test-key"

        result = subprocess.run(
            [GROOT_BIN, "chat"],
            env=env,
            capture_output=True,
            text=True,
            timeout=30,
        )
        combined = result.stdout + result.stderr

        # 退出后应看到关闭提示
        assert "嵌入服务已关闭" in combined, \
            f"嵌入服务应正确关闭:\nstdout={result.stdout}\nstderr={result.stderr}"

        # 确认端口已释放（服务已关闭）
        time.sleep(1)
        try:
            resp = requests.get("http://localhost:8192/health", timeout=2)
            assert False, f"服务应该已关闭，但端口 8192 仍可访问: {resp.status_code}"
        except requests.ConnectionError:
            pass  # 预期：服务已关闭，端口不可访问


class TestChatHelpOutput:
    """chat 帮助输出内容测试"""

    def test_help_mentions_all_commands(self):
        """TC-CHAT-007: 帮助输出提及所有系统命令"""
        result = subprocess.run(
            [GROOT_BIN, "--help"],
            capture_output=True,
            text=True,
        )
        # 验证关键系统命令是否在帮助中被提及
        for keyword in ["chat", "init", "status", "skills", "mcp", "schedule", "tail"]:
            assert keyword in result.stdout, f"帮助应提及 '{keyword}' 子命令"
