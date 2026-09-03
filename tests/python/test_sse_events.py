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

        # message 必须是第一个事件（替代旧的started/intent）
        assert order[0] == "message"

        # finish 必须是最后一个事件（替代旧的completed）
        assert order[-1] == "finish"

    def test_started_is_first_event(self, server, api_headers):
        """TC-SSE-002: message 是首个事件"""
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        # message 事件必须在最前
        events = sse.events
        assert events[0]["event"] == "message"

        # message 事件至少有一个（流式多条）
        message_events = sse.get_events_by_type("message")
        assert len(message_events) >= 1

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
        assert events[-1]["event"] == "finish"

        # completed 事件只有一个
        completed_events = sse.get_events_by_type("finish")
        assert len(completed_events) == 1

    def test_thinking_events_exist(self, server, api_headers):
        """TC-SSE-004: thinking 事件存在且包含必要字段（不再区分 start/end）"""
        payload = {"instruction": "帮我分析数据"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        thinking_events = sse.get_events_by_type("thinking")

        # thinking 事件应该存在（当需要推理时）
        if thinking_events:
            for event in thinking_events:
                data = event["data"]
                # thinking 事件必须包含 role 和 reasoning_content
                assert data.get("role") == "assistant"
                assert "reasoning_content" in data

    def test_tool_call_result_pairing(self, server, api_headers):
        """TC-SSE-005: tool_calls 和 tool_result 成对"""
        payload = {"instruction": "读取文件 /tmp/test.txt"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        tool_calls = sse.get_events_by_type("tool_calls")
        tool_results = sse.get_events_by_type("tool_result")

        # 验证 tool_calls 和 tool_result 存在对应关系
        # tool_calls 数量应 >= tool_result 数量
        assert len(tool_calls) >= len(tool_results), \
            f"tool_calls({len(tool_calls)}) 应 >= tool_result({len(tool_results)})"

        # 收集 tool_call 中的 id（可能在不同层级）
        call_ids = []
        for c in tool_calls:
            data = c["data"]
            if "id" in data:
                call_ids.append(data["id"])
            elif "tool_calls" in data:
                for tc in data["tool_calls"]:
                    if "id" in tc:
                        call_ids.append(tc["id"])

        result_ids = [r["data"].get("id") for r in tool_results if r["data"].get("id")]

        for result_id in result_ids:
            assert result_id in call_ids, f"tool_result id '{result_id}' 无对应 tool_calls"


class TestSSEEventFields:
    """SSE 事件字段完整性测试"""

    def test_started_event_fields(self, server, api_headers):
        """TC-SSE-006: 首个 message 事件字段验证（替代旧 started）"""
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

        # message 事件必须包含 role 和 content
        assert data.get("role") == "assistant"
        assert "content" in data

    def test_thinking_start_event_fields(self, server, api_headers):
        """TC-SSE-007: thinking 事件字段验证"""
        payload = {"instruction": "帮我分析数据"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        thinking_events = sse.get_events_by_type("thinking")

        if thinking_events:
            event = thinking_events[0]["data"]

            # thinking 事件必填字段
            assert event.get("role") == "assistant"
            assert "reasoning_content" in event

    def test_thinking_end_event_fields(self, server, api_headers):
        """TC-SSE-008: thinking 事件流式内容验证（不再区分 start/end）"""
        payload = {"instruction": "帮我分析数据"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        thinking_events = sse.get_events_by_type("thinking")

        if thinking_events:
            event = thinking_events[0]["data"]

            # thinking 事件必填字段
            assert event.get("role") == "assistant"
            assert "reasoning_content" in event

            # reasoning_content 应有实际内容
            assert isinstance(event["reasoning_content"], str)

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

            # thinking 事件必填字段
            assert thinking.get("role") == "assistant"
            assert "reasoning_content" in thinking

    def test_tool_call_event_fields(self, server, api_headers):
        """TC-SSE-010: tool_calls 事件字段验证"""
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

            # 必填字段：role 和 tool_calls 数组
            assert call.get("role") == "assistant"
            assert "tool_calls" in call
            assert isinstance(call["tool_calls"], list)
            assert len(call["tool_calls"]) > 0

            # 数组内每个元素应包含 id/type/function，name 和 arguments 嵌套在 function 对象里
            for tc in call["tool_calls"]:
                assert "function" in tc
                assert "name" in tc["function"]
                assert "arguments" in tc["function"]

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

            # tool_result 必填字段：role 为 tool
            assert result.get("role") == "tool"

            # 内容字段为 content（tool_result 负载），失败时有 error 标记
            assert "content" in result or "error" in result

    def test_message_start_event_fields(self, server, api_headers):
        """TC-SSE-012: message 事件字段验证（首个 message 替代旧 message_start）"""
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        message_events = sse.get_events_by_type("message")

        if message_events:
            event = message_events[0]["data"]
            assert event.get("role") == "assistant"
            assert "content" in event

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
            assert event.get("role") == "assistant"
            assert "content" in event

    def test_message_end_event_fields(self, server, api_headers):
        """TC-SSE-014: finish 事件字段验证（替代旧 message_end/completed）"""
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        finish_events = sse.get_events_by_type("finish")

        if finish_events:
            event = finish_events[0]["data"]
            assert event.get("role") == "assistant"
            assert "finish_reason" in event
            assert event["finish_reason"] in ["stop", "tool_calls", "length"]

    def test_completed_event_fields(self, server, api_headers):
        """TC-SSE-015: finish 事件字段验证（替代旧 completed）"""
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

        # finish 事件必填字段
        assert data.get("role") == "assistant"
        assert "finish_reason" in data

        # 验证 finish_reason 可选值
        assert data["finish_reason"] in ["stop", "tool_calls", "length"]


class TestSSECancelledEvent:
    """取消对话的 SSE 事件测试"""

    def test_cancelled_completed_event(self, server, api_headers):
        """TC-SSE-016: 取消对话时应有 error 事件（替代旧 completed cancelled）"""
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

        # 取消后应有 error 事件或 finish 事件
        error_events = sse.get_events_by_type("error")
        finish_events = sse.get_events_by_type("finish")

        if error_events:
            data = error_events[0]["data"]
            assert data.get("event") == "error"
            # error 事件应包含错误信息
            assert "message" in data or "error" in data
        elif finish_events:
            data = finish_events[0]["data"]
            # 如果是正常 finish，finish_reason 应为 stop
            assert data.get("finish_reason") in ["stop", "tool_calls", "length"]


class TestSSEMultipleRounds:
    """多轮对话 SSE 测试"""

    def test_round_field_increment(self, server, api_headers):
        """TC-SSE-017: 多轮对话均正常完成（finish 事件不再含 round 字段）"""
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
        assert completed1 is not None
        assert completed1["data"].get("role") == "assistant"
        assert "finish_reason" in completed1["data"]

        # 第二轮（使用相同 session_id）
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
        assert completed2 is not None
        assert completed2["data"].get("role") == "assistant"
        assert "finish_reason" in completed2["data"]

    def test_round_field_after_invalid_session(self, server, api_headers):
        """TC-SSE-018: 无效 session_id 仍可正常响应（finish 不含 round）"""
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

        assert completed is not None
        assert completed["data"].get("role") == "assistant"
        assert "finish_reason" in completed["data"]