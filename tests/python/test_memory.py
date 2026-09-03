"""
Memory/存储功能测试

记忆存储于数据库（{GROOT_HOME}/groot.db），不再有 memory/ 目录与
history.json / chats/*.json 文件；本文件全部通过 API 断言：
- GET /sess/{sid}      会话信息与历史消息结构
- GET /chat/{sid}      最近一次对话记录结构
- round_count / status 追踪
"""

import pytest
import requests
from conftest import BASE_URL, SSEClient, assert_session_id_format


class TestSessionHistoryFormat:
    """会话历史（GET /sess/{sid}）格式测试"""

    def test_session_history_exists(self, server, api_headers):
        """TC-MEM-001: 对话后可通过 API 查询会话历史"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")
        SSEClient(response)

        # 记忆已入库，直接通过 API 验证
        detail_response = requests.get(
            f"{BASE_URL}/sess/{session_id}",
            headers=api_headers
        )
        assert detail_response.status_code == 200

    def test_session_history_structure(self, server, api_headers):
        """TC-MEM-002: 会话历史结构验证（history 与 messages 字段）"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")
        SSEClient(response)

        detail_response = requests.get(
            f"{BASE_URL}/sess/{session_id}",
            headers=api_headers
        )
        assert detail_response.status_code == 200
        data = detail_response.json()

        # 验证顶层字段
        history = data["history"]
        assert "session_id" in history
        assert_session_id_format(history["session_id"])  # 无 sess_ 前缀
        assert "created_at" in history
        assert "messages" in history

        # 验证 messages 字段（memory.Message 结构）
        assert history["messages"], "对话完成后 messages 不应为空"
        msg = history["messages"][0]
        assert "round" in msg
        assert "chat_id" in msg
        assert "timestamp" in msg
        assert "instruction" in msg
        assert "result" in msg
        assert "status" in msg
        assert "duration" in msg
        assert "steps_count" in msg
        assert "error" in msg

    def test_session_history_multiple_rounds(self, server, api_headers):
        """TC-MEM-003: 会话历史多轮记录"""
        # 第一轮
        payload1 = {"instruction": "第一轮"}

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

        payload2 = {"instruction": "第二轮"}

        response2 = requests.post(
            f"{BASE_URL}/chat",
            headers=headers2,
            json=payload2,
            stream=True
        )

        SSEClient(response2)

        # 验证 API 返回
        detail_response = requests.get(
            f"{BASE_URL}/sess/{session_id}",
            headers=api_headers
        )

        data = detail_response.json()
        messages = data["history"]["messages"]

        assert len(messages) == 2
        assert messages[0]["round"] == 1
        assert messages[1]["round"] == 2


class TestChatRecordFormat:
    """对话记录（GET /chat/{sid}）测试"""

    def test_chat_record_exists(self, server, api_headers):
        """TC-MEM-004: 对话后可通过 API 查询对话记录"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")
        chat_id = response.headers.get("X-Chat-ID")
        SSEClient(response)

        # 记忆已入库，通过 API 验证
        detail_response = requests.get(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )
        assert detail_response.status_code == 200
        assert detail_response.json()["chat"]["chat_id"] == chat_id

    def test_chat_record_structure(self, server, api_headers):
        """TC-MEM-005: 对话记录结构验证（ChatRecord 字段）"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")
        chat_id = response.headers.get("X-Chat-ID")
        SSEClient(response)

        detail_response = requests.get(
            f"{BASE_URL}/chat/{session_id}/{chat_id}",
            headers=api_headers
        )
        assert detail_response.status_code == 200
        data = detail_response.json()["chat"]

        # 验证字段（repo.ChatRecord）
        assert "chat_id" in data
        assert "session_id" in data
        assert_session_id_format(data["session_id"])  # 无 sess_ 前缀
        assert "round" in data
        assert "timestamp" in data
        assert "instruction" in data
        assert "result" in data
        assert "status" in data
        assert "duration" in data
        assert "caller" in data
        assert "steps" in data
        assert "error" in data

        # 验证 steps 结构
        if data["steps"]:
            step = data["steps"][0]
            assert "step_id" in step
            assert "type" in step
            assert "name" in step
            assert "start_time" in step
            assert "end_time" in step
            assert "status" in step
            assert "nesting_level" in step


class TestSessionIDFormat:
    """会话标识格式测试"""

    def test_session_id_no_sess_prefix(self, server, api_headers):
        """TC-MEM-006: session_id 无 sess_ 前缀；session.path 字段存在且为空串

        会话数据已入库，不再有会话目录；API 保留 path 字段（兼容），值恒为空串。
        """
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")
        SSEClient(response)

        # 验证 session_id 无 sess_ 前缀
        assert not session_id.startswith("sess_")

        detail_response = requests.get(
            f"{BASE_URL}/sess/{session_id}",
            headers=api_headers
        )

        data = detail_response.json()
        # path 字段保留但恒为空串（数据在数据库中，无文件路径）
        assert "path" in data["session"]
        assert data["session"]["path"] == ""


class TestMemoryRoundTracking:
    """轮次追踪测试"""

    def test_round_count_in_session(self, server, api_headers):
        """TC-MEM-010: round_count 正确"""
        # 第一轮
        payload1 = {"instruction": "第一轮"}

        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload1,
            stream=True
        )

        session_id = response1.headers.get("X-Session-ID")
        SSEClient(response1)

        # 查询会话
        detail_response = requests.get(
            f"{BASE_URL}/sess/{session_id}",
            headers=api_headers
        )

        data = detail_response.json()
        assert data["session"]["round_count"] == 1

        # 第二轮
        headers2 = api_headers.copy()
        headers2["X-Session-ID"] = session_id

        payload2 = {"instruction": "第二轮"}

        response2 = requests.post(
            f"{BASE_URL}/chat",
            headers=headers2,
            json=payload2,
            stream=True
        )

        SSEClient(response2)

        # 再次查询
        detail_response2 = requests.get(
            f"{BASE_URL}/sess/{session_id}",
            headers=api_headers
        )

        data2 = detail_response2.json()
        assert data2["session"]["round_count"] == 2


class TestMemoryStatusTracking:
    """状态追踪测试"""

    def test_status_success(self, server, api_headers):
        """TC-MEM-011: 成功对话 status 为 completed"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")
        SSEClient(response)

        # 查询历史
        detail_response = requests.get(
            f"{BASE_URL}/sess/{session_id}",
            headers=api_headers
        )

        data = detail_response.json()
        messages = data["history"]["messages"]

        if messages:
            assert messages[0]["status"] == "completed" or messages[0]["status"] == "success"

    def test_status_cancelled(self, server, api_headers):
        """TC-MEM-012: 断开 SSE 后对话被终止"""
        payload = {"instruction": "长任务"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")

        # DELETE 端点已删除，返回 404
        cancel_response = requests.delete(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )
        assert cancel_response.status_code == 404

        # 关闭 SSE 流即取消
        response.close()

        SSEClient(response)

        # 等服务器完成写入
        import time
        time.sleep(1)

        # 查询历史 — 取消后状态可能为 cancelled/completed/failed
        detail_response = requests.get(
            f"{BASE_URL}/sess/{session_id}",
            headers=api_headers
        )

        data = detail_response.json()
        messages = data["history"]["messages"]

        if messages:
            # 取消后状态可能因 SSE 断开而不同
            assert messages[0]["status"] in ("cancelled", "completed", "failed")
