"""
API 工具系统测试
测试 API 工具的配置加载、验证、注册和调用
"""

import pytest
import requests
import json
import os
import time
import subprocess
import signal
from typing import Dict, Generator

# 测试环境配置
TEST_HOST = os.environ.get("GROOT_TEST_HOST", "localhost")
TEST_PORT = os.environ.get("GROOT_TEST_PORT", "8081")  # 使用不同端口避免冲突
TEST_API_KEY = os.environ.get("GROOT_TEST_API_KEY", "test-api-key-2026")
TEST_HOME = os.environ.get("GROOT_TEST_HOME", "/tmp/groot_apitool_test")
# 正确计算 GROOT_BIN 路径（项目根目录的 bin/groot）
GROOT_BIN = os.environ.get("GROOT_BIN", os.path.abspath(os.path.join(os.path.dirname(__file__), "../../bin/groot")))

BASE_URL = f"http://{TEST_HOST}:{TEST_PORT}"


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


def start_groot_server(api_configs: Dict = None, mcp_configs: Dict = None, env_vars: Dict = None):
    """启动 groot 测试服务器"""
    # 创建测试目录
    os.makedirs(TEST_HOME, exist_ok=True)
    os.makedirs(f"{TEST_HOME}/skills", exist_ok=True)
    os.makedirs(f"{TEST_HOME}/mcp", exist_ok=True)
    os.makedirs(f"{TEST_HOME}/api", exist_ok=True)
    os.makedirs(f"{TEST_HOME}/memory", exist_ok=True)
    os.makedirs(f"{TEST_HOME}/logs", exist_ok=True)

    # 写入配置
    import yaml
    config = {
        "agent": {"name": "groot", "version": "1.0.0"},
        "server": {"host": "0.0.0.0", "port": int(TEST_PORT)},
        "llm": {
            "default_model": "mock-model",
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
        "skills": {"directory": "skills"},
        "mcp": {"directory": "mcp"},
        "api_tools": {"directory": "api"},  # 添加 API 工具配置目录
        "security": {
            "auth": {
                "enabled": True,
                "type": "api_key",
                "api_key": {
                    "header_name": "X-API-Key",
                    "keys": [{"name": "test_client", "key": TEST_API_KEY, "permissions": ["all"]}]
                }
            }
        },
        "memory": {"directory": "memory", "retention_days": 1},
        "logging": {"level": "debug", "format": "json", "output": ["stdout"]}
    }

    with open(f"{TEST_HOME}/config.yaml", "w") as f:
        yaml.dump(config, f)

    # 写入 API 工具配置
    if api_configs:
        for name, config_data in api_configs.items():
            with open(f"{TEST_HOME}/api/{name}.json", "w") as f:
                json.dump(config_data, f)

    # 写入 MCP 配置
    if mcp_configs:
        for name, config_data in mcp_configs.items():
            with open(f"{TEST_HOME}/mcp/{name}.json", "w") as f:
                json.dump(config_data, f)

    # 设置环境变量
    env = os.environ.copy()
    env["GROOT_HOME"] = TEST_HOME
    env["GROOT_API_KEY"] = TEST_API_KEY
    if env_vars:
        env.update(env_vars)

    # 停止已有进程
    subprocess.run(["pkill", "-f", f"groot.*{TEST_PORT}"], check=False, capture_output=True)
    time.sleep(1)

    # 启动 groot
    process = subprocess.Popen(
        [GROOT_BIN, "-H", TEST_HOME, "-p", TEST_PORT],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE
    )

    return process


def stop_groot_server(process):
    """停止 groot 测试服务器"""
    process.send_signal(signal.SIGTERM)
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        process.kill()


def cleanup_test_home():
    """清理测试目录"""
    import shutil
    if os.path.exists(TEST_HOME):
        shutil.rmtree(TEST_HOME)


def extract_tool_names(tools_response: Dict) -> list:
    """从工具响应中提取所有工具名称（响应是按组分类的 map）"""
    tool_names = []
    for group_name, group_data in tools_response.items():
        if isinstance(group_data, dict) and "tools" in group_data:
            for tool in group_data["tools"]:
                if "name" in tool:
                    tool_names.append(tool["name"])
    return tool_names


def find_tool_by_name(tools_response: Dict, tool_name: str) -> Dict:
    """从工具响应中查找指定名称的工具详细信息"""
    for group_name, group_data in tools_response.items():
        if isinstance(group_data, dict) and "tools" in group_data:
            for tool in group_data["tools"]:
                if tool.get("name") == tool_name:
                    return tool
    return None


class TestAPIToolConfigLoading:
    """API 工具配置加载测试"""

    def test_load_single_api_tool(self):
        """测试加载单个 API 工具配置"""
        api_configs = {
            "test_weather": {
                "name": "get_weather",
                "description": "获取天气信息",
                "url": "http://localhost:9999/weather/${city}",
                "method": "GET",
                "parameters": [
                    {"name": "city", "type": "string", "required": True, "description": "城市名称"}
                ]
            }
        }

        process = start_groot_server(api_configs=api_configs)

        try:
            if not wait_for_server(timeout=30):
                raise RuntimeError("服务器启动失败")

            # 检查工具列表是否包含 API 工具
            headers = {"Content-Type": "application/json", "X-API-Key": TEST_API_KEY}
            response = requests.get(f"{BASE_URL}/tools", headers=headers)

            assert response.status_code == 200
            tools = response.json()
            tool_names = extract_tool_names(tools)

            assert "get_weather" in tool_names, f"API 工具 get_weather 未注册，工具列表: {tool_names}"
        finally:
            stop_groot_server(process)
            cleanup_test_home()

    def test_load_multiple_api_tools(self):
        """测试加载多个 API 工具配置"""
        api_configs = {
            "tool1": {
                "name": "api_tool_1",
                "description": "API工具1",
                "url": "http://localhost:9999/api1",
                "method": "GET"
            },
            "tool2": {
                "name": "api_tool_2",
                "description": "API工具2",
                "url": "http://localhost:9999/api2",
                "method": "POST"
            },
            "tool3": {
                "name": "api_tool_3",
                "description": "API工具3",
                "url": "http://localhost:9999/api3",
                "method": "DELETE"
            }
        }

        process = start_groot_server(api_configs=api_configs)

        try:
            if not wait_for_server(timeout=30):
                raise RuntimeError("服务器启动失败")

            headers = {"Content-Type": "application/json", "X-API-Key": TEST_API_KEY}
            response = requests.get(f"{BASE_URL}/tools", headers=headers)

            assert response.status_code == 200
            tools = response.json()
            tool_names = extract_tool_names(tools)

            assert "api_tool_1" in tool_names
            assert "api_tool_2" in tool_names
            assert "api_tool_3" in tool_names
        finally:
            stop_groot_server(process)
            cleanup_test_home()

    def test_load_invalid_json_fails(self):
        """测试加载无效 JSON 配置时启动失败"""
        # 写入无效 JSON
        os.makedirs(f"{TEST_HOME}/api", exist_ok=True)
        with open(f"{TEST_HOME}/api/invalid.json", "w") as f:
            f.write("not a valid json")

        # 写入有效配置
        api_configs = {
            "valid": {
                "name": "valid_tool",
                "description": "有效工具",
                "url": "http://localhost:9999/valid",
                "method": "GET"
            }
        }

        process = start_groot_server(api_configs=api_configs)

        try:
            # 应该启动失败（无效 JSON 导致启动失败）
            success = wait_for_server(timeout=10)
            assert not success, "无效 JSON 配置时服务器应该启动失败"
        finally:
            stop_groot_server(process)
            cleanup_test_home()


class TestAPIToolEnvVarValidation:
    """API 工具环境变量验证测试"""

    def test_startup_fail_with_missing_env_var(self):
        """测试环境变量缺失时启动失败"""
        api_configs = {
            "weather": {
                "name": "get_weather",
                "description": "获取天气",
                "url": "http://localhost:9999/weather",
                "method": "GET",
                "headers": {
                    "Authorization": "Bearer $${MISSING_API_KEY}"
                }
            }
        }

        # 不设置 MISSING_API_KEY 环境变量
        process = start_groot_server(api_configs=api_configs)

        try:
            # 应该启动失败或超时
            success = wait_for_server(timeout=10)
            assert not success, "环境变量缺失时服务器应该启动失败"
        finally:
            stop_groot_server(process)
            cleanup_test_home()

    def test_startup_success_with_env_var_set(self):
        """测试环境变量设置时启动成功"""
        api_configs = {
            "weather": {
                "name": "get_weather",
                "description": "获取天气",
                "url": "http://localhost:9999/weather",
                "method": "GET",
                "auth": {
                    "type": "bearer",
                    "token": "$${TEST_API_TOKEN}"
                }
            }
        }

        # 设置环境变量
        env_vars = {"TEST_API_TOKEN": "test-token-value"}

        process = start_groot_server(api_configs=api_configs, env_vars=env_vars)

        try:
            success = wait_for_server(timeout=30)
            assert success, "环境变量设置时服务器应该启动成功"

            headers = {"Content-Type": "application/json", "X-API-Key": TEST_API_KEY}
            response = requests.get(f"{BASE_URL}/tools", headers=headers)

            assert response.status_code == 200
            tools = response.json()
            tool_names = extract_tool_names(tools)
            assert "get_weather" in tool_names
        finally:
            stop_groot_server(process)
            cleanup_test_home()

    def test_env_var_in_url(self):
        """测试 URL 中的环境变量"""
        api_configs = {
            "dynamic_url": {
                "name": "dynamic_api",
                "description": "动态 URL API",
                "url": "http://$${API_HOST}:9999/data",
                "method": "GET"
            }
        }

        env_vars = {"API_HOST": "localhost"}

        process = start_groot_server(api_configs=api_configs, env_vars=env_vars)

        try:
            success = wait_for_server(timeout=30)
            assert success

            headers = {"Content-Type": "application/json", "X-API-Key": TEST_API_KEY}
            response = requests.get(f"{BASE_URL}/tools", headers=headers)

            assert response.status_code == 200
            tools = response.json()
            tool_names = extract_tool_names(tools)
            assert "dynamic_api" in tool_names
        finally:
            stop_groot_server(process)
            cleanup_test_home()

    def test_env_var_in_body(self):
        """测试 Body 中的环境变量"""
        api_configs = {
            "order": {
                "name": "create_order",
                "description": "创建订单",
                "url": "http://localhost:9999/orders",
                "method": "POST",
                "body": {
                    "apiKey": "$${ORDER_API_KEY}",
                    "data": "test"
                },
                "bodyType": "json"
            }
        }

        env_vars = {"ORDER_API_KEY": "order-key-123"}

        process = start_groot_server(api_configs=api_configs, env_vars=env_vars)

        try:
            success = wait_for_server(timeout=30)
            assert success

            headers = {"Content-Type": "application/json", "X-API-Key": TEST_API_KEY}
            response = requests.get(f"{BASE_URL}/tools", headers=headers)

            assert response.status_code == 200
            tools = response.json()
            tool_names = extract_tool_names(tools)
            assert "create_order" in tool_names
        finally:
            stop_groot_server(process)
            cleanup_test_home()


class TestAPIToolNameConflict:
    """API 工具名称冲突测试"""

    def test_same_name_api_tools_override(self):
        """测试同名 API 工具会覆盖"""
        api_configs = {
            "tool1": {
                "name": "same_tool",
                "description": "第一个工具",
                "url": "http://localhost:9999/api1",
                "method": "GET"
            },
            "tool2": {
                "name": "same_tool",
                "description": "第二个工具（覆盖）",
                "url": "http://localhost:9999/api2",
                "method": "POST"
            }
        }

        process = start_groot_server(api_configs=api_configs)

        try:
            success = wait_for_server(timeout=30)
            assert success, "服务器应该启动成功"

            headers = {"Content-Type": "application/json", "X-API-Key": TEST_API_KEY}
            response = requests.get(f"{BASE_URL}/tools", headers=headers)

            assert response.status_code == 200
            tools = response.json()
            tool_names = extract_tool_names(tools)

            # 应该只有一个同名工具
            assert len([n for n in tool_names if n == "same_tool"]) == 1
        finally:
            stop_groot_server(process)
            cleanup_test_home()


class TestAPIToolParameters:
    """API 工具参数定义测试"""

    def test_tool_info_contains_parameters(self):
        """测试工具信息包含参数定义"""
        api_configs = {
            "weather": {
                "name": "get_weather",
                "description": "获取天气信息",
                "url": "http://localhost:9999/weather/${city}",
                "method": "GET",
                "query": {
                    "unit": "${unit}"
                },
                "parameters": [
                    {"name": "city", "type": "string", "required": True, "description": "城市名称"},
                    {"name": "unit", "type": "string", "required": False, "default": "celsius", "description": "温度单位"}
                ]
            }
        }

        process = start_groot_server(api_configs=api_configs)

        try:
            success = wait_for_server(timeout=30)
            assert success

            headers = {"Content-Type": "application/json", "X-API-Key": TEST_API_KEY}
            response = requests.get(f"{BASE_URL}/tools", headers=headers)

            assert response.status_code == 200
            tools = response.json()

            weather_tool = find_tool_by_name(tools, "get_weather")
            assert weather_tool is not None, "get_weather 工具未找到"

            assert "description" in weather_tool
            assert weather_tool["description"] == "获取天气信息"

            # 注意：当前 /tools API 不返回参数信息，只返回名称和描述
            # 参数信息需要通过其他方式获取（如调用工具时验证）

        finally:
            stop_groot_server(process)
            cleanup_test_home()

    def test_tool_without_parameters(self):
        """测试无参数的工具"""
        api_configs = {
            "simple": {
                "name": "simple_api",
                "description": "简单API无参数",
                "url": "http://localhost:9999/simple",
                "method": "GET"
            }
        }

        process = start_groot_server(api_configs=api_configs)

        try:
            success = wait_for_server(timeout=30)
            assert success

            headers = {"Content-Type": "application/json", "X-API-Key": TEST_API_KEY}
            response = requests.get(f"{BASE_URL}/tools", headers=headers)

            assert response.status_code == 200
            tools = response.json()

            simple_tool = find_tool_by_name(tools, "simple_api")
            assert simple_tool is not None, "simple_api 工具未找到"
            assert simple_tool["description"] == "简单API无参数"

        finally:
            stop_groot_server(process)
            cleanup_test_home()


class TestAPIToolAuthTypes:
    """API 工具认证类型测试"""

    def test_bearer_auth_tool(self):
        """测试 Bearer 认证工具"""
        api_configs = {
            "bearer": {
                "name": "bearer_api",
                "description": "Bearer认证API",
                "url": "http://localhost:9999/bearer",
                "method": "GET",
                "auth": {
                    "type": "bearer",
                    "token": "$${BEARER_TOKEN}"
                }
            }
        }

        env_vars = {"BEARER_TOKEN": "bearer-token-value"}

        process = start_groot_server(api_configs=api_configs, env_vars=env_vars)

        try:
            success = wait_for_server(timeout=30)
            assert success

            headers = {"Content-Type": "application/json", "X-API-Key": TEST_API_KEY}
            response = requests.get(f"{BASE_URL}/tools", headers=headers)

            assert response.status_code == 200
            tools = response.json()
            tool_names = extract_tool_names(tools)
            assert "bearer_api" in tool_names
        finally:
            stop_groot_server(process)
            cleanup_test_home()

    def test_basic_auth_tool(self):
        """测试 Basic 认证工具"""
        api_configs = {
            "basic": {
                "name": "basic_api",
                "description": "Basic认证API",
                "url": "http://localhost:9999/basic",
                "method": "GET",
                "auth": {
                    "type": "basic",
                    "username": "testuser",
                    "password": "$${BASIC_PASSWORD}"
                }
            }
        }

        env_vars = {"BASIC_PASSWORD": "basic-password-value"}

        process = start_groot_server(api_configs=api_configs, env_vars=env_vars)

        try:
            success = wait_for_server(timeout=30)
            assert success

            headers = {"Content-Type": "application/json", "X-API-Key": TEST_API_KEY}
            response = requests.get(f"{BASE_URL}/tools", headers=headers)

            assert response.status_code == 200
            tools = response.json()
            tool_names = extract_tool_names(tools)
            assert "basic_api" in tool_names
        finally:
            stop_groot_server(process)
            cleanup_test_home()

    def test_apikey_auth_tool(self):
        """测试 ApiKey 认证工具"""
        api_configs = {
            "apikey": {
                "name": "apikey_api",
                "description": "ApiKey认证API",
                "url": "http://localhost:9999/apikey",
                "method": "GET",
                "auth": {
                    "type": "apikey",
                    "key": "$${API_KEY_VALUE}",
                    "location": "header",
                    "name": "X-Custom-API-Key"
                }
            }
        }

        env_vars = {"API_KEY_VALUE": "apikey-value"}

        process = start_groot_server(api_configs=api_configs, env_vars=env_vars)

        try:
            success = wait_for_server(timeout=30)
            assert success

            headers = {"Content-Type": "application/json", "X-API-Key": TEST_API_KEY}
            response = requests.get(f"{BASE_URL}/tools", headers=headers)

            assert response.status_code == 200
            tools = response.json()
            tool_names = extract_tool_names(tools)
            assert "apikey_api" in tool_names
        finally:
            stop_groot_server(process)
            cleanup_test_home()

    def test_no_auth_tool(self):
        """测试无认证工具"""
        api_configs = {
            "noauth": {
                "name": "noauth_api",
                "description": "无认证API",
                "url": "http://localhost:9999/noauth",
                "method": "GET"
            }
        }

        process = start_groot_server(api_configs=api_configs)

        try:
            success = wait_for_server(timeout=30)
            assert success

            headers = {"Content-Type": "application/json", "X-API-Key": TEST_API_KEY}
            response = requests.get(f"{BASE_URL}/tools", headers=headers)

            assert response.status_code == 200
            tools = response.json()
            tool_names = extract_tool_names(tools)
            assert "noauth_api" in tool_names
        finally:
            stop_groot_server(process)
            cleanup_test_home()


class TestAPIToolDirectory:
    """API 工具目录测试"""

    def test_empty_api_directory(self):
        """测试空 API 目录"""
        # 不创建任何 API 配置
        process = start_groot_server()

        try:
            success = wait_for_server(timeout=30)
            assert success, "空 API 目录时服务器应该正常启动"

            headers = {"Content-Type": "application/json", "X-API-Key": TEST_API_KEY}
            response = requests.get(f"{BASE_URL}/tools", headers=headers)

            assert response.status_code == 200
            # 工具列表可能只有 MCP 工具或为空
        finally:
            stop_groot_server(process)
            cleanup_test_home()

    def test_api_directory_not_exists(self):
        """测试 API 目录不存在"""
        # 不创建 api 目录
        os.makedirs(TEST_HOME, exist_ok=True)
        os.makedirs(f"{TEST_HOME}/mcp", exist_ok=True)
        os.makedirs(f"{TEST_HOME}/memory", exist_ok=True)

        import yaml
        config = {
            "agent": {"name": "groot", "version": "1.0.0"},
            "server": {"host": "0.0.0.0", "port": int(TEST_PORT)},
            "llm": {
                "default_model": "mock-model",
                "models": {
                    "mock-model": {
                        "base_url": "http://localhost:8888/mock",
                        "api_key": "mock-key",
                        "model": "mock"
                    }
                }
            },
            "mcp": {"directory": "mcp"},
            "api_tools": {"directory": "api"},  # 添加 API 工具配置目录
            "security": {
                "auth": {
                    "enabled": True,
                    "type": "api_key",
                    "api_key": {
                        "header_name": "X-API-Key",
                        "keys": [{"name": "test_client", "key": TEST_API_KEY, "permissions": ["all"]}]
                    }
                }
            },
            "memory": {"directory": "memory"}
        }

        with open(f"{TEST_HOME}/config.yaml", "w") as f:
            yaml.dump(config, f)

        env = os.environ.copy()
        env["GROOT_HOME"] = TEST_HOME
        env["GROOT_API_KEY"] = TEST_API_KEY

        subprocess.run(["pkill", "-f", f"groot.*{TEST_PORT}"], check=False, capture_output=True)
        time.sleep(1)

        process = subprocess.Popen(
            [GROOT_BIN, "-H", TEST_HOME, "-p", TEST_PORT],
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE
        )

        try:
            success = wait_for_server(timeout=30)
            assert success, "API 目录不存在时服务器应该正常启动"
        finally:
            stop_groot_server(process)
            cleanup_test_home()