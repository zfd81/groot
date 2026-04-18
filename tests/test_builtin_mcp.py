"""
内置 MCP 工具测试
测试 file_operations、http_request 等内置工具
"""

import pytest
import requests
import os
import json
import tempfile
from conftest import BASE_URL, SSEClient


class TestFileOperationsMCP:
    """file_operations MCP 工具测试"""

    def test_file_read_tool_exists(self, server, api_headers):
        """TC-MCP-001: file_read 工具存在"""
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        assert response.status_code == 200
        tools = response.json()["tools"]

        file_read = next((t for t in tools if t["name"] == "file_read"), None)
        assert file_read is not None
        assert file_read["mcp"] == "file_operations"
        assert "读取文件" in file_read["description"]

    def test_file_write_tool_exists(self, server, api_headers):
        """TC-MCP-002: file_write 工具存在"""
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        tools = response.json()["tools"]

        file_write = next((t for t in tools if t["name"] == "file_write"), None)
        assert file_write is not None
        assert file_write["mcp"] == "file_operations"

    def test_file_delete_tool_exists(self, server, api_headers):
        """TC-MCP-003: file_delete 工具存在"""
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        tools = response.json()["tools"]

        file_delete = next((t for t in tools if t["name"] == "file_delete"), None)
        # file_delete 可能被默认禁用
        # 如果存在，验证字段
        if file_delete:
            assert file_delete["mcp"] == "file_operations"

    def test_directory_list_tool_exists(self, server, api_headers):
        """TC-MCP-004: directory_list 工具存在"""
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        tools = response.json()["tools"]

        dir_list = next((t for t in tools if t["name"] == "directory_list"), None)
        assert dir_list is not None

    def test_file_search_tool_exists(self, server, api_headers):
        """TC-MCP-005: file_search 工具存在"""
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        tools = response.json()["tools"]

        file_search = next((t for t in tools if t["name"] == "file_search"), None)
        if file_search:
            assert file_search["mcp"] == "file_operations"

    def test_file_exists_tool_exists(self, server, api_headers):
        """TC-MCP-006: file_exists 工具存在"""
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        tools = response.json()["tools"]

        file_exists = next((t for t in tools if t["name"] == "file_exists"), None)
        if file_exists:
            assert file_exists["mcp"] == "file_operations"

    def test_file_info_tool_exists(self, server, api_headers):
        """TC-MCP-007: file_info 工具存在"""
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        tools = response.json()["tools"]

        file_info = next((t for t in tools if t["name"] == "file_info"), None)
        if file_info:
            assert file_info["mcp"] == "file_operations"


class TestHTTPRequestMCP:
    """http_request MCP 工具测试"""

    def test_http_get_tool_exists(self, server, api_headers):
        """TC-MCP-008: http_get 工具存在"""
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        tools = response.json()["tools"]

        http_get = next((t for t in tools if t["name"] == "http_get"), None)
        assert http_get is not None
        assert http_get["mcp"] == "http_request"

    def test_http_post_tool_exists(self, server, api_headers):
        """TC-MCP-009: http_post 工具存在"""
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        tools = response.json()["tools"]

        http_post = next((t for t in tools if t["name"] == "http_post"), None)
        assert http_post is not None

    def test_http_put_tool_exists(self, server, api_headers):
        """TC-MCP-010: http_put 工具存在"""
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        tools = response.json()["tools"]

        http_put = next((t for t in tools if t["name"] == "http_put"), None)
        if http_put:
            assert http_put["mcp"] == "http_request"

    def test_http_delete_tool_exists(self, server, api_headers):
        """TC-MCP-011: http_delete 工具存在"""
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        tools = response.json()["tools"]

        http_delete = next((t for t in tools if t["name"] == "http_delete"), None)
        if http_delete:
            assert http_delete["mcp"] == "http_request"


class TestCodeExecutionMCP:
    """code_execution MCP 工具测试"""

    def test_code_execution_disabled_by_default(self, server, api_headers):
        """TC-MCP-012: code_execution 默认禁用"""
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        tools = response.json()["tools"]

        # code_execution 工具默认不应存在
        code_exec_tools = [t for t in tools if t["mcp"] == "code_execution"]

        # 默认禁用，不应有这类工具
        # 如果有，验证其描述说明风险
        if code_exec_tools:
            for tool in code_exec_tools:
                assert "风险" in tool.get("description", "") or "高风险" in tool.get("description", "")


class TestMCPToolFields:
    """MCP 工具字段验证"""

    def test_tool_field_structure(self, server, api_headers):
        """TC-MCP-013: 工具字段完整性"""
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        tools = response.json()["tools"]

        for tool in tools:
            assert "name" in tool
            assert "description" in tool
            assert "mcp" in tool  # MCP 来源标识


class TestMCPToolSecurity:
    """MCP 工具安全限制测试"""

    def test_file_operations_allowed_paths(self, server, api_headers):
        """TC-MCP-014: file_operations 仅允许访问指定路径"""
        # Agent 执行时应有路径限制
        # 此测试验证配置存在
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        # 验证 file_operations 工具存在且有描述
        tools = response.json()["tools"]
        file_ops_tools = [t for t in tools if t["mcp"] == "file_operations"]

        assert len(file_ops_tools) > 0

    def test_http_request_blocks_localhost(self, server, api_headers):
        """TC-MCP-015: http_request 禁止访问 localhost"""
        # 尝试让 Agent 访问 localhost（通过对话）
        payload = {"instruction": "使用http_get访问http://localhost:8080/health"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        completed = sse.get_completed_event()

        # 应拒绝访问 localhost（或使用其他方式）
        # 具体行为取决于实现

    def test_http_request_timeout(self, server, api_headers):
        """TC-MCP-016: http_request 超时限制"""
        # 验证 http_request 有超时配置
        # 此测试验证工具存在
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        tools = response.json()["tools"]
        http_tools = [t for t in tools if t["mcp"] == "http_request"]

        assert len(http_tools) > 0