"""
命令行参数和环境变量测试
测试启动参数、环境变量配置等

说明: groot 支持的命令行 flag 为 -p/--port、-h/--help、-v/--version；
健康检查端点为 GET /web/health（免认证）。
"""

import pytest
import subprocess
import os
import time
import requests
import signal

# 获取正确的 groot 二进制路径: tests/python -> tests -> groot -> bin/groot
GROOT_BIN = os.environ.get("GROOT_BIN", os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(__file__))), "bin", "groot"))


def wait_health(port, timeout=15):
    """轮询 /web/health 直到服务就绪，返回是否就绪"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            r = requests.get(f"http://localhost:{port}/web/health", timeout=2)
            if r.status_code == 200:
                return True
        except Exception:
            pass
        time.sleep(0.5)
    return False


class TestCommandLineArgs:
    """命令行参数测试"""

    def test_help_flag(self):
        """TC-CMD-001: --help 参数显示帮助"""
        result = subprocess.run(
            [GROOT_BIN, "--help"],
            capture_output=True,
            text=True
        )

        # 验证输出包含帮助信息
        assert "--help" in result.stdout or "-h" in result.stdout
        assert "--port" in result.stdout or "-p" in result.stdout

    def test_version_flag(self):
        """TC-CMD-002: --version 参数显示版本"""
        result = subprocess.run(
            [GROOT_BIN, "--version"],
            capture_output=True,
            text=True
        )

        # 验证输出包含版本号
        assert "1.0.0" in result.stdout or "version" in result.stdout.lower()

    def test_port_flag(self):
        """TC-CMD-004: -p 参数指定端口"""
        # 启动服务在非默认端口
        process = subprocess.Popen(
            [GROOT_BIN, "-p", "8091"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE
        )

        try:
            # 服务应在指定端口就绪
            assert wait_health(8091), "服务未在指定端口 8091 就绪"
        finally:
            process.send_signal(signal.SIGTERM)
            process.wait(timeout=10)


class TestEnvironmentVariables:
    """环境变量测试"""

    def test_groot_home_env(self):
        """TC-ENV-001: GROOT_HOME 环境变量指定工作目录"""
        import shutil
        test_home = "/tmp/groot_env_test"
        shutil.rmtree(test_home, ignore_errors=True)
        os.makedirs(test_home, exist_ok=True)

        env = os.environ.copy()
        env["GROOT_HOME"] = test_home

        # 无配置文件时服务拒绝启动，需先 init（配置不会自动生成）
        init_result = subprocess.run(
            [GROOT_BIN, "init"], env=env, capture_output=True, text=True
        )
        assert init_result.returncode == 0, init_result.stderr
        assert os.path.exists(f"{test_home}/config.yaml"), \
            "GROOT_HOME 指定的目录下未生成 config.yaml"

        process = subprocess.Popen(
            [GROOT_BIN, "-p", "8092"],
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE
        )

        try:
            assert wait_health(8092), "服务未就绪"
        finally:
            process.send_signal(signal.SIGTERM)
            process.wait(timeout=10)

    def test_auth_always_on(self):
        """TC-ENV-003: 认证始终开启（JWT API Key 机制）

        写入新格式 security.auth 配置（仅 header_name + secret），启动后验证：
        - 不带 Key 访问 POST /chat → 401（认证无法关闭）
        - GET /web/health 免认证 → 200
        """
        test_home = "/tmp/groot_key_test"
        os.makedirs(test_home, exist_ok=True)

        env = os.environ.copy()
        env["GROOT_HOME"] = test_home

        # 新格式配置：认证始终开启，只需 HS256 签名密钥
        config_content = """
security:
  auth:
    header_name: X-API-Key
    secret: cli-args-test-secret-0123456789
"""
        with open(f"{test_home}/config.yaml", "w") as f:
            f.write(config_content)

        process = subprocess.Popen(
            [GROOT_BIN, "-p", "8093"],
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE
        )

        try:
            # 等待服务就绪（/web/health 免认证）
            # 注意：此服务实例模型库为空，但本用例只验证 401/健康检查，
            # 不发起有效 /chat 对话，无需创建模型
            assert wait_health(8093), "服务未在 15 秒内就绪"

            # 不带 API Key → 401（认证始终开启）
            response = requests.post(
                "http://localhost:8093/chat",
                headers={"Content-Type": "application/json"},
                json={"instruction": "test"},
                timeout=10
            )
            assert response.status_code == 401
        finally:
            process.send_signal(signal.SIGTERM)
            process.wait(timeout=10)


class TestConfigPriority:
    """配置优先级测试"""

    def test_cli_overrides_config(self):
        """TC-PRI-001: 命令行参数优先于配置文件"""
        test_home = "/tmp/groot_priority_test"
        os.makedirs(test_home, exist_ok=True)

        # 配置文件设置端口 8080
        config_content = """
server:
  host: 0.0.0.0
  port: 8080
"""
        with open(f"{test_home}/config.yaml", "w") as f:
            f.write(config_content)

        env = os.environ.copy()
        env["GROOT_HOME"] = test_home

        # 命令行指定端口 8094（应覆盖配置）
        process = subprocess.Popen(
            [GROOT_BIN, "-p", "8094"],
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE
        )

        try:
            # 验证使用命令行端口
            assert wait_health(8094), "命令行指定的端口 8094 未生效"
        finally:
            process.send_signal(signal.SIGTERM)
            process.wait(timeout=10)
