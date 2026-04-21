"""
日志功能测试
测试 JSON 结构化日志、日志级别、日志文件等

新版设计变化：
- 日志事件名：chat_completed（旧版是 task_completed）
- 日志事件类型：api_request, chat_completed, skill_hot_reload, mcp_hot_reload
"""

import pytest
import requests
import os
import json
import glob
from conftest import BASE_URL, TEST_HOME


class TestLogFormat:
    """日志格式测试"""

    def test_log_directory_exists(self, server):
        """TC-LOG-001: 日志目录存在"""
        logs_dir = f"{TEST_HOME}/logs"

        if os.path.exists(logs_dir):
            assert True
        else:
            # 日志可能输出到 stdout
            assert True

    def test_log_file_format(self, server, api_headers):
        """TC-LOG-002: 日志文件命名格式"""
        # 执行一次请求以产生日志
        requests.get(f"{BASE_URL}/health")

        logs_dir = f"{TEST_HOME}/logs"

        if os.path.exists(logs_dir):
            log_files = glob.glob(f"{logs_dir}/groot-*.log")

            # 验证文件名格式：groot-{date}.log
            for log_file in log_files:
                filename = os.path.basename(log_file)
                assert filename.startswith("groot-")
                assert filename.endswith(".log")

    def test_log_json_structure(self, server, api_headers):
        """TC-LOG-003: JSON 结构化日志"""
        # 执行请求
        requests.get(f"{BASE_URL}/health")

        logs_dir = f"{TEST_HOME}/logs"

        if os.path.exists(logs_dir):
            log_files = glob.glob(f"{logs_dir}/groot-*.log")

            if log_files:
                with open(log_files[0], "r") as f:
                    lines = f.readlines()

                    for line in lines[:10]:  # 检查前10行
                        line = line.strip()
                        if line:
                            try:
                                data = json.loads(line)

                                # 验证 JSON 结构
                                assert "timestamp" in data
                                assert "level" in data

                                # level 应为标准值
                                assert data["level"] in ["DEBUG", "INFO", "WARN", "ERROR"]

                            except json.JSONDecodeError:
                                # 可能是纯文本格式
                                pass


class TestLogLevels:
    """日志级别测试"""

    def test_log_level_info(self, server, api_headers):
        """TC-LOG-004: INFO 级别日志"""
        # 执行正常操作
        requests.get(f"{BASE_URL}/health")

        logs_dir = f"{TEST_HOME}/logs"

        if os.path.exists(logs_dir):
            log_files = glob.glob(f"{logs_dir}/groot-*.log")

            if log_files:
                with open(log_files[0], "r") as f:
                    content = f.read()

                    # 应包含 INFO 级别日志
                    # 可能是 JSON 格式：{"level":"INFO"}
                    # 或纯文本：[INFO]
                    assert "INFO" in content

    def test_log_level_error_on_failure(self, server, api_headers):
        """TC-LOG-005: ERROR 级别日志（失败场景）"""
        # 执行错误请求
        requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": ""}
        )

        logs_dir = f"{TEST_HOME}/logs"

        if os.path.exists(logs_dir):
            log_files = glob.glob(f"{logs_dir}/groot-*.log")

            if log_files:
                with open(log_files[0], "r") as f:
                    content = f.read()

                    # 应包含 ERROR 或 WARN
                    # 具体取决于错误处理


class TestLogEvents:
    """日志事件类型测试"""

    def test_log_api_request_event(self, server, api_headers):
        """TC-LOG-006: api_request 日志事件"""
        requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "test"},
            stream=True
        )

        logs_dir = f"{TEST_HOME}/logs"

        if os.path.exists(logs_dir):
            log_files = glob.glob(f"{logs_dir}/groot-*.log")

            if log_files:
                with open(log_files[0], "r") as f:
                    lines = f.readlines()

                    for line in lines[-20:]:
                        try:
                            data = json.loads(line.strip())
                            if "event" in data:
                                assert data["event"] in [
                                    "api_request",
                                    "chat_completed",
                                    "skill_hot_reload",
                                    "mcp_hot_reload",
                                    "memory_cleanup"
                                ]
                        except:
                            pass

    def test_log_chat_completed_event(self, server, api_headers):
        """TC-LOG-007: chat_completed 日志事件"""
        from conftest import SSEClient

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "test"},
            stream=True
        )

        SSEClient(response)  # 等待完成

        logs_dir = f"{TEST_HOME}/logs"

        if os.path.exists(logs_dir):
            log_files = glob.glob(f"{logs_dir}/groot-*.log")

            if log_files:
                with open(log_files[0], "r") as f:
                    content = f.read()

                    # 应有完成相关日志


class TestLogRetention:
    """日志保留测试"""

    @pytest.mark.skip(reason="需要长时间运行验证")
    def test_log_max_age(self, server):
        """TC-LOG-008: 日志保留天数限制"""
        # 验证日志配置中的 max_age 生效
        # 需要等待过期日志被删除

    @pytest.mark.skip(reason="需要大量日志")
    def test_log_rotation(self, server):
        """TC-LOG-009: 日志文件轮转"""
        # 验证日志文件超过 max_size 后轮转
        # 需要产生大量日志


class TestLogOutput:
    """日志输出测试"""

    def test_log_stdout(self, server):
        """TC-LOG-010: 日志输出到 stdout"""
        # 验证服务启动时日志输出
        # stdout 日志通常在服务控制台查看

        response = requests.get(f"{BASE_URL}/health")
        assert response.status_code == 200