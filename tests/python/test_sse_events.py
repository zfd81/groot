"""
SSE 事件测试
测试 SSE 流式响应的事件顺序、字段完整性等
"""

import pytest
import requests
import json
from conftest import (
    BASE_URL,
    SSEClient,
)


class TestSSEEventOrder:
    """SSE 事件顺序测试"""

    def test_event_order_basic(self, server, api_headers):
        """TC-SSE-001: 事件顺序正确性"""
        payload = {"instruction": "帮我写一个函数"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200
        sse = SSEClient(response)

        # 验证事件顺序
        order = sse.get_event_order()

        # started 必须是第一个事件（替代旧的intent）
        assert order[0] == "started"

        # completed 必须是最后一个事件
        assert order[-1] == "completed"

    def test_started_is_first_event(self, server, api_headers):
        """TC-SSE-002: started 是首个事件"""
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        # started 事件必须在最前
        events = sse.events
        assert events[0]["event"] == "started"

        # started 事件只有一个
        started_events = sse.get_events_by_type("started")
        assert len(started_events) == 1

    def test_completed_is_last_event(self, server, api_headers):
        """TC-SSE-003: completed 是最后一个事件"""
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        # completed 事件必须在最后
        events = sse.events
        assert events[-1]["event"] == "completed"

        # completed 事件只有一个
        completed_events = sse.get_events_by_type("completed")
        assert len(completed_events) == 1

    def test_thinking_start_end_pairing(self, server, api_headers):
        """TC-SSE-004: thinking_start 和 thinking_end 成对"""
        payload = {"instruction": "帮我分析数据"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        thinking_starts = sse.get_events_by_type("thinking_start")
        thinking_ends = sse.get_events_by_type("thinking_end")

        # 数量应该相等
        assert len(thinking_starts) == len(thinking_ends)

        # 每个 thinking_end 的 step_id 应对应 thinking_start
        start_ids = [s["data"]["step_id"] for s in thinking_starts]
        end_ids = [e["data"]["step_id"] for e in thinking_ends]

        for end_id in end_ids:
            assert end_id in start_ids

    def test_tool_call_result_pairing(self, server, api_headers):
        """TC-SSE-005: tool_call 和 tool_result 成对"""
        payload = {"instruction": "读取文件 /tmp/test.txt"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        tool_calls = sse.get_events_by_type("tool_call")
        tool_results = sse.get_events_by_type("tool_result")

        # 数量应该相等
        assert len(tool_calls) == len(tool_results)

        # 每个 tool_result 的 step_id 应对应 tool_call
        call_ids = [c["data"]["step_id"] for c in tool_calls]
        result_ids = [r["data"]["step_id"] for r in tool_results]

        for result_id in result_ids:
            assert result_id in call_ids


class TestSSEEventFields:
    """SSE 事件字段完整性测试"""

    def test_started_event_fields(self, server, api_headers):
        """TC-SSE-006: started 事件字段验证"""
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        started = sse.get_started_event()

        assert started is not None
        data = started["data"]

        # 必须包含 session_id, chat_id, timestamp
        assert "session_id" in data
        assert "chat_id" in data
        assert "timestamp" in data

        # timestamp 应为 ISO 格式
        timestamp = data["timestamp"]
        assert "T" in timestamp  # ISO 格式包含 T

    def test_thinking_start_event_fields(self, server, api_headers):
        """TC-SSE-007: thinking_start 事件字段验证"""
        payload = {"instruction": "帮我分析数据"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        thinking_starts = sse.get_events_by_type("thinking_start")

        if thinking_starts:
            step = thinking_starts[0]["data"]

            # 必填字段
            assert "step_id" in step
            assert "timestamp" in step

    def test_thinking_end_event_fields(self, server, api_headers):
        """TC-SSE-008: thinking_end 事件字段验证"""
        payload = {"instruction": "帮我分析数据"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        thinking_ends = sse.get_events_by_type("thinking_end")

        if thinking_ends:
            step = thinking_ends[0]["data"]

            # 必填字段
            assert "step_id" in step
            assert "timestamp" in step
            assert "status" in step

            # 验证 status 可选值
            assert step["status"] in ["success", "failed"]

    def test_thinking_event_fields(self, server, api_headers):
        """TC-SSE-009: thinking 事件字段验证"""
        payload = {"instruction": "帮我分析数据"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        thinking_events = sse.get_thinking_events()

        if thinking_events:
            thinking = thinking_events[0]["data"]

            # 必填字段
            assert "content" in thinking
            assert "timestamp" in thinking

    def test_tool_call_event_fields(self, server, api_headers):
        """TC-SSE-010: tool_call 事件字段验证"""
        payload = {"instruction": "读取文件 /tmp/test.txt"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        tool_calls = sse.get_tool_calls()

        if tool_calls:
            call = tool_calls[0]["data"]

            # 必填字段
            assert "step_id" in call
            assert "name" in call
            assert "arguments" in call
            assert "timestamp" in call

    def test_tool_result_event_fields(self, server, api_headers):
        """TC-SSE-011: tool_result 事件字段验证"""
        payload = {"instruction": "读取文件 /tmp/test.txt"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        tool_results = sse.get_tool_results()

        if tool_results:
            result = tool_results[0]["data"]

            # 必填字段
            assert "step_id" in result
            assert "timestamp" in result

            # 可选字段：output 或 error
            # 成功时有 output，失败时有 error
            assert "output" in result or "error" in result

    def test_message_start_event_fields(self, server, api_headers):
        """TC-SSE-012: message_start 事件字段验证"""
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        message_starts = sse.get_events_by_type("message_start")

        if message_starts:
            event = message_starts[0]["data"]
            assert "timestamp" in event

    def test_message_event_fields(self, server, api_headers):
        """TC-SSE-013: message 事件字段验证"""
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        message_events = sse.get_message_events()

        if message_events:
            event = message_events[0]["data"]
            assert "content" in event
            assert "timestamp" in event

    def test_message_end_event_fields(self, server, api_headers):
        """TC-SSE-014: message_end 事件字段验证"""
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        message_ends = sse.get_events_by_type("message_end")

        if message_ends:
            event = message_ends[0]["data"]
            assert "timestamp" in event

    def test_completed_event_fields(self, server, api_headers):
        """TC-SSE-015: completed 事件字段验证"""
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        completed = sse.get_completed_event()

        assert completed is not None
        data = completed["data"]

        # 必填字段
        assert "status" in data
        assert "timestamp" in data
        assert "duration" in data
        assert "round" in data
        assert "chat_id" in data  # 新增字段

        # 验证 status 可选值
        assert data["status"] in ["success", "failed", "cancelled"]

        # 验证 round 为整数
        assert isinstance(data["round"], int)

        # 成功时应包含 result
        if data["status"] == "success":
            assert "result" in data

        # 失败时应包含 error
        if data["status"] == "failed":
            assert "error" in data

        # 取消时应包含 message
        if data["status"] == "cancelled":
            assert "message" in data


class TestSSECancelledEvent:
    """取消对话的 SSE 事件测试"""

    def test_cancelled_completed_event(self, server, api_headers):
        """TC-SSE-016: 取消对话 completed 事件验证"""
        # 启动长任务
        payload = {"instruction": "帮我分析大数据文件"}

        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response1.headers.get("X-Session-ID")

        # 等待一会儿后取消
        import time
        time.sleep(1)

        # 发送取消请求
        requests.delete(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )

        # 解析 SSE
        sse = SSEClient(response1)
        completed = sse.get_completed_event()

        if completed:
            data = completed["data"]

            # 验证取消状态
            assert data["status"] == "cancelled"

            # 验证取消消息
            assert "message" in data
            assert "取消" in data["message"] or "cancel" in data["message"].lower()

            # 验证 round 存在
            assert "round" in data


class TestSSEMultipleRounds:
    """多轮对话 SSE 测试"""

    def test_round_field_increment(self, server, api_headers):
        """TC-SSE-017: 多轮对话 round 递增"""
        # 第一轮
        payload1 = {"instruction": "第一轮对话"}

        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload1,
            stream=True
        )

        session_id = response1.headers.get("X-Session-ID")
        sse1 = SSEClient(response1)

        completed1 = sse1.get_completed_event()
        assert completed1["data"]["round"] == 1

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

        sse2 = SSEClient(response2)

        completed2 = sse2.get_completed_event()
        assert completed2["data"]["round"] == 2

    def test_round_field_after_invalid_session(self, server, api_headers):
        """TC-SSE-018: 无效 session_id round 为 1"""
        headers = api_headers.copy()
        headers["X-Session-ID"] = "invalid_session"

        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        completed = sse.get_completed_event()

        assert completed["data"]["round"] == 1