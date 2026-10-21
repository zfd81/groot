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


class TestSSEClientFlatten:
    """SSEClient.flatten_chunk 单元断言（不依赖服务器）

    验证 OpenAI chat.completion.chunk 外层结构被正确摊平为旧式扁平事件对象，
    且摊平结果能被 _infer_event_type 正确分类。
    """

    @staticmethod
    def _chunk(delta, finish_reason=None):
        return {
            "id": "c",
            "object": "chat.completion.chunk",
            "created": 1,
            "model": "m",
            "choices": [{"index": 0, "delta": delta, "finish_reason": finish_reason}],
        }

    def test_flatten_message(self):
        """TC-SSE-FLAT-001: message chunk 摊平为 delta 本身"""
        raw = self._chunk({"role": "assistant", "content": "hi"})
        assert SSEClient.flatten_chunk(raw) == {"role": "assistant", "content": "hi"}
        assert SSEClient._infer_event_type(SSEClient.flatten_chunk(raw)) == "message"

    def test_flatten_finish(self):
        """TC-SSE-FLAT-002: finish chunk 摊平后含 finish_reason，类型为 finish"""
        raw = self._chunk({}, finish_reason="stop")
        assert SSEClient.flatten_chunk(raw) == {"finish_reason": "stop"}
        assert SSEClient._infer_event_type(SSEClient.flatten_chunk(raw)) == "finish"

    def test_flatten_finish_with_agent(self):
        """TC-SSE-FLAT-003: 带 agent_name 的 finish chunk 同时保留 agent_name 与 finish_reason"""
        raw = self._chunk({"agent_name": "coder"}, finish_reason="tool_calls")
        assert SSEClient.flatten_chunk(raw) == {
            "agent_name": "coder",
            "finish_reason": "tool_calls",
        }

    def test_flatten_error_passthrough(self):
        """TC-SSE-FLAT-004: error chunk 转换为 event=error 扁平对象"""
        raw = {"error": {"message": "boom", "type": "server_error"}}
        flat = SSEClient.flatten_chunk(raw)
        assert flat == {"event": "error", "message": "boom", "error": raw["error"]}
        assert SSEClient._infer_event_type(flat) == "error"

    def test_flatten_tool_result(self):
        """TC-SSE-FLAT-005: role=tool 的 delta 原样返回，类型为 tool_result"""
        delta = {
            "role": "tool",
            "tool_call_id": "call_1",
            "tool_name": "skill",
            "content": "ok",
            "error": False,
        }
        raw = self._chunk(delta)
        flat = SSEClient.flatten_chunk(raw)
        assert flat == delta
        assert flat is not delta  # 应为拷贝
        assert SSEClient._infer_event_type(flat) == "tool_result"

    def test_flatten_no_choices_no_error_passthrough(self):
        """TC-SSE-FLAT-006: 无 choices 且无 error 的对象原样返回"""
        raw = {"foo": "bar"}
        assert SSEClient.flatten_chunk(raw) is raw

    def test_flatten_empty_choices_passthrough(self):
        """TC-SSE-FLAT-008: choices 为空列表时原样返回，不抛 IndexError"""
        raw = {"id": "c", "object": "chat.completion.chunk", "choices": []}
        assert SSEClient.flatten_chunk(raw) is raw

    def test_parse_events_keeps_raw(self):
        """TC-SSE-FLAT-007: _parse_events 的 data 为摊平对象，raw 为原始 chunk"""

        class _FakeResp:
            text = (
                "data: " + json.dumps(self._chunk({"role": "assistant", "content": "hi"})) + "\n\n"
                "data: " + json.dumps(self._chunk({}, finish_reason="stop")) + "\n\n"
                "data: [DONE]\n\n"
            )

        sse = SSEClient(_FakeResp())
        assert sse.get_event_order() == ["message", "finish"]
        assert sse.events[0]["data"] == {"role": "assistant", "content": "hi"}
        assert sse.events[0]["raw"]["object"] == "chat.completion.chunk"
        assert sse.events[1]["data"] == {"finish_reason": "stop"}
        assert "choices" in sse.events[1]["raw"]


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
            # finish chunk 的 delta 为空（子代理场景仅含 agent_name），不断言 role
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

        # finish 事件必填字段（delta 为空，不含 role，只断言 finish_reason）
        assert "finish_reason" in data

        # 验证 finish_reason 可选值
        assert data["finish_reason"] in ["stop", "tool_calls", "length"]

    def test_chunk_envelope_fields(self, server, api_headers):
        """TC-SSE-030: 每条非 error 事件的原始 chunk 含 OpenAI 外层字段"""
        payload = {"instruction": "你好"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200
        chat_id = response.headers.get("X-Chat-ID")
        assert chat_id

        sse = SSEClient(response)
        chunk_events = [e for e in sse.events if "choices" in e["raw"]]
        assert chunk_events, "流中应至少有一条含 choices 的 chunk"

        created_values = set()
        for e in chunk_events:
            raw = e["raw"]
            assert raw["object"] == "chat.completion.chunk"
            assert raw["id"] == chat_id
            assert isinstance(raw["created"], int)
            assert isinstance(raw["model"], str) and raw["model"]
            assert raw["choices"][0]["index"] == 0
            created_values.add(raw["created"])

        # 同一流内所有 chunk 的 created 相同
        assert len(created_values) == 1

    def test_error_is_terminal(self, server, api_headers):
        """TC-SSE-031: error 事件之后不再有含 choices 的事件

        用常规请求难以稳定触发致命错误，因此退而断言：若流中存在 error 事件，
        其索引之后的所有事件均为 error 类型（即不再出现含 choices 的 chunk）。
        流中没有 error 事件时本用例自然通过。
        """
        payload = {"instruction": "你好"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200
        sse = SSEClient(response)

        order = sse.get_event_order()
        if "error" not in order:
            return

        first_error = order.index("error")
        for e in sse.events[first_error:]:
            assert e["event"] == "error"
            assert "choices" not in e["raw"]


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
            # 摊平后的 error 事件：event=error 且含 message
            assert data.get("event") == "error" and "message" in data
            # 原始 chunk 为 {"error": {...}}，不含 choices
            assert "choices" not in error_events[0]["raw"]
            assert "error" in error_events[0]["raw"]
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
        assert "finish_reason" in completed1["data"]
        assert completed1["data"]["finish_reason"] in ["stop", "tool_calls", "length"]

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
        assert "finish_reason" in completed2["data"]
        assert completed2["data"]["finish_reason"] in ["stop", "tool_calls", "length"]

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
        assert "finish_reason" in completed["data"]
        assert completed["data"]["finish_reason"] in ["stop", "tool_calls", "length"]
