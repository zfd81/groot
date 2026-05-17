"""
API 端点测试
测试所有 REST API 接口的功能和响应格式
"""

import pytest
import requests
import json
import time
from conftest import (
    BASE_URL,
    SSEClient,
    assert_session_id_format,
    assert_chat_id_format,
    assert_step_id_format,
    generate_session_id
)


class TestChatAPI:
    """POST /chat API 测试"""

    def test_new_session_basic(self, server, api_headers):
        """TC-001: 新会话基本对话（无附件）"""
        payload = {"instruction": "帮我写一个Python快速排序函数"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200
        assert response.headers["Content-Type"] == "text/event-stream"

        # 验证响应 Headers
        session_id = response.headers.get("X-Session-ID")
        chat_id = response.headers.get("X-Chat-ID")

        assert_session_id_format(session_id)
        assert_chat_id_format(chat_id)

        # 解析 SSE 事件
        sse = SSEClient(response)

        # 验证事件顺序
        assert sse.verify_event_order()
        assert sse.get_event_order()[0] == "message"
        assert sse.get_event_order()[-1] == "finish"

        # 验证首个 message 事件
        first_msg = sse.get_intent_event()
        assert first_msg is not None
        assert first_msg["data"]["role"] == "assistant"
        assert "content" in first_msg["data"]

        # 验证 finish 事件
        completed = sse.get_completed_event()
        assert completed is not None
        assert completed["data"]["role"] == "assistant"
        assert "finish_reason" in completed["data"]

    def test_new_session_with_attachment(self, server, api_headers, test_file_base64):
        """TC-002: 新会话带附件对话"""
        payload = {
            "instruction": "帮我分析这个CSV数据",
            "attachments": [
                {
                    "type": "file",
                    "name": "test_data.csv",
                    "content": test_file_base64
                }
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200

        session_id = response.headers.get("X-Session-ID")
        assert_session_id_format(session_id)

        sse = SSEClient(response)

        # 验证事件流有内容
        assert len(sse.events) > 0
        assert sse.verify_event_order()

        # 验证 tool_calls 事件格式（如果有工具调用）
        file_read_steps = []
        for s in sse.get_all_steps():
            if s["event"] == "tool_calls" and "tool_calls" in s["data"]:
                for tc in s["data"]["tool_calls"]:
                    if tc.get("function", {}).get("name") == "file_read":
                        file_read_steps.append(s)

        completed = sse.get_completed_event()
        assert completed is not None
        assert completed["data"]["role"] == "assistant"
        assert "finish_reason" in completed["data"]

    def test_multi_attachments(self, server, api_headers, test_file_base64, pdf_file_base64):
        """TC-003: 多附件请求"""
        payload = {
            "instruction": "对比分析这两个文件",
            "attachments": [
                {"type": "file", "name": "file1.csv", "content": test_file_base64},
                {"type": "file", "name": "file2.pdf", "content": pdf_file_base64}
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200

        sse = SSEClient(response)
        completed = sse.get_completed_event()
        assert completed is not None
        assert completed["data"]["role"] == "assistant"
        assert "finish_reason" in completed["data"]

    def test_with_custom_prompt(self, server, api_headers, pdf_file_base64):
        """TC-004: 自定义系统提示词"""
        payload = {
            "instruction": "分析这份报告",
            "prompt": "你是一个财务分析师，重点关注利润增长率和潜在风险点。输出JSON格式。",
            "attachments": [
                {"type": "file", "name": "report.pdf", "content": pdf_file_base64}
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200

        sse = SSEClient(response)
        completed = sse.get_completed_event()
        assert completed is not None
        assert completed["data"]["role"] == "assistant"
        assert "finish_reason" in completed["data"]

    def test_continue_session(self, server, api_headers):
        """TC-005: 多轮对话继续会话"""
        # 第一轮：获取 session_id
        payload1 = {"instruction": "帮我写一个Python快速排序函数"}

        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload1,
            stream=True
        )

        assert response1.status_code == 200
        session_id = response1.headers.get("X-Session-ID")
        chat_id1 = response1.headers.get("X-Chat-ID")

        sse1 = SSEClient(response1)
        assert sse1.get_completed_event() is not None

        # 第二轮：使用相同 session_id 继续
        headers2 = api_headers.copy()
        headers2["X-Session-ID"] = session_id

        payload2 = {"instruction": "根据刚才的函数，添加注释和类型提示"}

        response2 = requests.post(
            f"{BASE_URL}/chat",
            headers=headers2,
            json=payload2,
            stream=True
        )

        assert response2.status_code == 200
        assert response2.headers.get("X-Session-ID") == session_id
        chat_id2 = response2.headers.get("X-Chat-ID")
        assert chat_id2 != chat_id1  # 新的 chat_id

        sse2 = SSEClient(response2)
        assert sse2.get_completed_event() is not None

    def test_invalid_session_id_creates_new(self, server, api_headers):
        """TC-006: 无效 session_id 自动创建新会话"""
        headers = api_headers.copy()
        headers["X-Session-ID"] = "invalid_session_id_12345"

        payload = {"instruction": "测试指令"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200

        # 应返回新的 session_id（不是请求中的 invalid 值）
        session_id = response.headers.get("X-Session-ID")
        assert session_id != "invalid_session_id_12345"
        assert_session_id_format(session_id)

        sse = SSEClient(response)
        assert sse.get_completed_event() is not None

    def test_concurrent_session_conflict(self, server, api_headers):
        """TC-007: 会话并发执行冲突（409）"""
        # 先获取一个 session_id
        payload = {"instruction": "帮我写一个Python快速排序函数"}

        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response1.headers.get("X-Session-ID")

        # 在第一个请求还在执行时，发起第二个请求
        headers2 = api_headers.copy()
        headers2["X-Session-ID"] = session_id

        payload2 = {"instruction": "另一个任务"}

        response2 = requests.post(
            f"{BASE_URL}/chat",
            headers=headers2,
            json=payload2
        )

        # 应返回 409 Conflict
        assert response2.status_code == 409
        data = response2.json()
        assert data["status"] == "chat_limit_exceeded"
        assert "正在执行" in data["message"] or "冲突" in data["message"]

    def test_empty_instruction(self, server, api_headers):
        """TC-008: 空指令返回 400"""
        payload = {"instruction": ""}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400
        data = response.json()
        assert data["status"] == "invalid_request"

    def test_missing_instruction(self, server, api_headers):
        """TC-009: 缺少指令字段返回 400"""
        payload = {}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400
        data = response.json()
        assert data["status"] == "invalid_request"


class TestDeleteChatAPI:
    """DELETE /chat/{sid} API 测试 — 端点已移除"""

    def test_delete_endpoint_removed(self, server, api_headers):
        """TC-010: DELETE /chat/{sid} 端点已移除，返回 404"""
        session_id = generate_session_id()

        response = requests.delete(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )

        assert response.status_code == 404

    def test_delete_nonexistent_returns_404(self, server, api_headers):
        """TC-011: DELETE /chat/{sid} 对不存在的会话也返回 404"""
        session_id = generate_session_id()

        response = requests.delete(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )

        assert response.status_code == 404


class TestChatStatusAPI:
    """GET /chat/status/{sid} API 测试"""

    def test_get_running_status(self, server, api_headers):
        """TC-012: 查询执行中的对话状态"""
        # 启动任务
        payload = {"instruction": "帮我分析数据"}

        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response1.headers.get("X-Session-ID")

        # 查询状态
        response2 = requests.get(
            f"{BASE_URL}/chat/status/{session_id}",
            headers=api_headers
        )

        assert response2.status_code == 200
        data = response2.json()
        assert data["status"] == "success"
        assert data["session_id"] == session_id
        assert data["chat"] is not None
        assert data["chat"]["status"] == "running"
        assert "progress" in data["chat"]
        assert "started_at" in data["chat"]
        assert "elapsed_time" in data["chat"]

    def test_get_no_running_status(self, server, api_headers):
        """TC-013: 查询无执行会话状态"""
        session_id = generate_session_id()

        response = requests.get(
            f"{BASE_URL}/chat/status/{session_id}",
            headers=api_headers
        )

        assert response.status_code == 200
        data = response.json()
        assert data["chat"] is None


class TestChatDetailAPI:
    """GET /chat/{sid} API 测试"""

    def test_get_chat_detail(self, server, api_headers):
        """TC-014: 查询最近对话详情（完整步骤）"""
        # 先完成一次对话
        payload = {"instruction": "帮我写一个函数"}

        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response1.headers.get("X-Session-ID")
        SSEClient(response1)  # 等待完成

        # 查询详情
        response2 = requests.get(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )

        assert response2.status_code == 200
        data = response2.json()
        assert data["status"] == "success"
        assert data["session_id"] == session_id
        assert data["chat"] is not None

        # 验证字段（使用新版名称）
        chat = data["chat"]
        assert "chat_id" in chat
        assert "round" in chat
        assert "instruction" in chat  # 新版字段名
        assert "attachments" in chat
        assert "result" in chat  # 新版字段名（非 user_content/assistant_content）
        assert "status" in chat
        assert "started_at" in chat
        assert "ended_at" in chat
        assert "duration" in chat
        assert "steps" in chat

        # 验证步骤格式
        if chat["steps"]:
            step = chat["steps"][0]
            assert_step_id_format(step["step_id"])
            assert "nesting_level" in step


class TestSessionDetailAPI:
    """GET /sess/{sid} API 测试"""

    def test_get_session_detail(self, server, api_headers):
        """TC-015: 查询会话完整历史"""
        # 先完成多轮对话
        payload1 = {"instruction": "第一轮对话"}

        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload1,
            stream=True
        )

        session_id = response1.headers.get("X-Session-ID")
        SSEClient(response1)

        # 第二轮
        headers2 = api_headers.copy()
        headers2["X-Session-ID"] = session_id
        payload2 = {"instruction": "第二轮对话"}

        response2 = requests.post(
            f"{BASE_URL}/chat",
            headers=headers2,
            json=payload2,
            stream=True
        )
        SSEClient(response2)

        # 查询会话详情
        response3 = requests.get(
            f"{BASE_URL}/sess/{session_id}",
            headers=api_headers
        )

        assert response3.status_code == 200
        data = response3.json()
        assert data["status"] == "success"
        assert data["session_id"] == session_id

        # 验证 session 字段
        session = data["session"]
        assert "created_at" in session
        assert "round_count" in session
        assert session["round_count"] == 2
        assert "path" in session
        assert session_id in session["path"]  # path 中包含 session_id（无 sess_ 前缀）

        # 验证 history.messages 字段（使用新版名称）
        messages = data["history"]["messages"]
        assert len(messages) == 2

        msg = messages[0]
        assert "round" in msg
        assert "chat_id" in msg  # 新增字段
        assert "timestamp" in msg
        assert "instruction" in msg  # 新版字段名
        assert "attachments" in msg
        assert "result" in msg  # 新版字段名
        assert "result_attachments" in msg
        assert "status" in msg
        assert "duration" in msg
        assert "steps_count" in msg
        assert "error" in msg


class TestSessionHistoryAPI:
    """GET /sess/history API 测试"""

    def test_get_session_list(self, server, api_headers):
        """TC-016: 查询会话列表（分页）"""
        # 先创建几个会话
        for i in range(3):
            payload = {"instruction": f"测试对话{i}"}
            response = requests.post(
                f"{BASE_URL}/chat",
                headers=api_headers,
                json=payload,
                stream=True
            )
            SSEClient(response)

        # 查询列表
        response = requests.get(
            f"{BASE_URL}/sess/history?limit=10&offset=0",
            headers=api_headers
        )

        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "success"
        assert "total" in data
        assert "limit" in data
        assert "offset" in data
        assert "sessions" in data

        if data["sessions"]:
            session = data["sessions"][0]
            assert "session_id" in session
            assert_session_id_format(session["session_id"])  # 无 sess_ 前缀
            assert "created_at" in session
            assert "round_count" in session
            assert "last_active_at" in session

    def test_session_list_pagination(self, server, api_headers):
        """TC-017: 分页参数生效"""
        response = requests.get(
            f"{BASE_URL}/sess/history?limit=5&offset=10",
            headers=api_headers
        )

        assert response.status_code == 200
        data = response.json()
        assert data["limit"] == 5
        assert data["offset"] == 10

    def test_session_list_empty(self, server, api_headers, cleanup_memory):
        """TC-018: 无会话时返回空列表"""
        response = requests.get(
            f"{BASE_URL}/sess/history?limit=10",
            headers=api_headers
        )

        assert response.status_code == 200
        data = response.json()
        assert isinstance(data["sessions"], list)


class TestHealthAPI:
    """GET /health API 测试"""

    def test_health_check(self, server):
        """TC-019: 健康检查接口"""
        response = requests.get(f"{BASE_URL}/health")

        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "healthy"
        assert "version" in data
        assert "uptime" in data
        assert "checks" in data

        # 验证各项检查
        checks = data["checks"]
        assert "llm" in checks
        assert "mcp_servers" in checks
        assert "skills" in checks
        assert "memory" in checks

        # 验证 metrics
        metrics = data.get("metrics", {})
        assert "chats_running" in metrics


class TestSkillsAPI:
    """GET /skills API 测试"""

    def test_list_skills(self, server, api_headers):
        """TC-020: 列出 Skills"""
        response = requests.get(
            f"{BASE_URL}/skills",
            headers=api_headers
        )

        assert response.status_code == 200
        data = response.json()
        assert "skills" in data
        assert "total" in data
        assert isinstance(data["skills"], list)

    def test_skills_after_add(self, server, api_headers, mock_skill):
        """TC-021: 添加 Skill 后列表更新"""
        # 等待热插拔生效
        time.sleep(3)

        response = requests.get(
            f"{BASE_URL}/skills",
            headers=api_headers
        )

        assert response.status_code == 200
        data = response.json()

        # 应包含新添加的 skill
        skill_names = [s["name"] for s in data["skills"]]
        assert "test_skill" in skill_names


class TestToolsAPI:
    """GET /tools API 测试"""

    def test_list_tools(self, server, api_headers):
        """TC-022: 列出 MCP 工具 (按 MCP 分组)"""
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        assert response.status_code == 200
        data = response.json()

        # 验证新格式：按 MCP 分组
        # data 应为 {"filesystem": {"tools": [...], "total": N}, ...}
        assert isinstance(data, dict)

        # 验证每个 MCP 分组结构
        for mcp_name, group in data.items():
            assert "tools" in group
            assert "total" in group
            assert isinstance(group["tools"], list)
            assert group["total"] == len(group["tools"])

            # 验证工具字段（不包含冗余的 mcp 字段）
            for tool in group["tools"]:
                assert "name" in tool
                assert "description" in tool
                # mcp 字段不应存在
                assert "mcp" not in tool

    def test_tools_include_builtin(self, server, api_headers):
        """TC-023: 工具列表包含 MCP 工具（验证分组格式）"""
        response = requests.get(
            f"{BASE_URL}/tools",
            headers=api_headers
        )

        assert response.status_code == 200
        data = response.json()

        # 验证新格式：按 MCP 分组
        assert isinstance(data, dict)

        # 遍历所有 MCP 分组收集工具名称
        tool_names = []
        for mcp_name, group in data.items():
            # 验证每个分组结构
            assert "tools" in group
            assert "total" in group
            assert isinstance(group["tools"], list)
            tool_names.extend([t["name"] for t in group["tools"]])

        # 验证：如果有 MCP 工具，检查结构正确
        # 注意：内置 MCP (file_operations) 已移除，此测试主要验证分组格式
        if tool_names:
            # 至少有工具返回，验证工具有 name 和 description
            for mcp_name, group in data.items():
                for tool in group["tools"]:
                    assert "name" in tool
                    assert "description" in tool


class TestAPIResponseFormat:
    """API 响应格式验证测试"""

    def test_success_response_format(self, server, api_headers):
        """TC-024: 成功响应统一格式"""
        response = requests.get(
            f"{BASE_URL}/sess/history",
            headers=api_headers
        )

        assert response.status_code == 200
        data = response.json()

        # 成功响应应包含 status 字段
        assert "status" in data
        assert data["status"] == "success"

    def test_error_response_format(self, server, no_auth_headers):
        """TC-025: 错误响应统一格式"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=no_auth_headers,
            json={"instruction": "test"}
        )

        assert response.status_code == 401
        data = response.json()

        # 错误响应应包含 status 和 message
        assert "status" in data
        assert "message" in data
        assert data["status"] == "unauthorized"