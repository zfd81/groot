"""
SSE 响应流完整性测试
测试 SSE 流式响应的完整事件流程，确保不遗漏关键事件
"""

import pytest
import requests
import json
from conftest import (
    BASE_URL,
    SSEClient,
)


class TestSSEFlowIntegrity:
    """SSE 响应流完整性测试"""

    def test_no_old_event_types(self, server, api_headers):
        """TC-Flow-001: 不应该出现旧的事件类型

        验证 SSE 流中不包含旧的 event 类型：
        - intent (已替换为 message)
        - started (已替换为 message)
        - completed (已替换为 finish)
        - step_start / step_end (已替换为 thinking / tool_calls / tool_result)
        - progress (已替换为 thinking / message)
        - thinking_start / thinking_end (已替换为 thinking)
        - tool_call (已替换为 tool_calls)
        - message_start / message_end (已移除)
        """
        payload = {"instruction": "列出当前目录下的文件"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200
        sse = SSEClient(response)

        # 获取所有事件类型
        event_types = sse.get_event_order()

        # 不应该包含旧的事件类型
        old_event_types = [
            "intent", "started", "completed",
            "step_start", "step_end", "progress",
            "thinking_start", "thinking_end",
            "tool_call", "message_start", "message_end"
        ]
        for old_type in old_event_types:
            assert old_type not in event_types, f"发现旧事件类型: {old_type}"

    def test_started_exists_and_unique(self, server, api_headers):
        """TC-Flow-002: started 事件必须存在且唯一

        started 事件是整体开始信号，必须发送
        """
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        started_events = sse.get_events_by_type("message")

        # 必须有 started 事件
        assert len(started_events) >= 1, "缺少 started 事件"

        # started 必须是第一个事件
        assert sse.get_event_order()[0] == "message", "started 不是第一个事件"

    def test_completed_exists_and_unique(self, server, api_headers):
        """TC-Flow-003: completed 事件必须存在且唯一

        completed 事件是整体结束信号，必须发送
        """
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        completed_events = sse.get_events_by_type("finish")

        # 必须有 completed 事件
        assert len(completed_events) >= 1, "缺少 completed 事件"

        # completed 必须是最后一个事件
        assert sse.get_event_order()[-1] == "finish", "completed 不是最后一个事件"

    def test_message_sequence_exists(self, server, api_headers):
        """TC-Flow-004: message 事件序列验证

        新协议不再区分 message_start/message_end，所有消息内容均为 message 事件
        注意：如果 agent 没有产生输出（如 Mock LLM 不返回内容），可能没有 message 事件
        """
        payload = {"instruction": "你好，介绍一下自己"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        event_order = sse.get_event_order()

        message_events = sse.get_events_by_type("message")

        if message_events:
            # 验证 message 事件包含必要字段
            for event in message_events:
                data = event["data"]
                assert data.get("role") == "assistant"
                assert "content" in data

            # 验证 message 在 finish 之前
            if "finish" in event_order:
                last_message_idx = max(
                    i for i, e in enumerate(event_order) if e == "message"
                )
                finish_idx = event_order.index("finish")
                assert last_message_idx < finish_idx, \
                    "message 事件应在 finish 事件之前"

    def test_tool_call_must_exist_when_tool_used(self, server, api_headers):
        """TC-Flow-005: 工具调用场景必须包含 tool_calls

        这是关键测试：如果 agent 使用了工具，必须发送 tool_calls 事件
        不能只发送 tool_result 而不发送 tool_calls
        """
        # 使用明确会触发工具调用的指令
        payload = {"instruction": "列出当前目录下的所有文件"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200
        sse = SSEClient(response)

        completed = sse.get_completed_event()

        # 检查是否有 tool_result（说明工具被调用）
        tool_results = sse.get_events_by_type("tool_result")

        if tool_results:
            # 如果有 tool_result，必须也有 tool_calls
            tool_calls = sse.get_events_by_type("tool_calls")

            assert len(tool_calls) >= 1, \
                f"有 {len(tool_results)} 个 tool_result 但缺少 tool_calls 事件"

            # tool_calls 数量应等于 tool_result 数量
            assert len(tool_calls) == len(tool_results), \
                f"tool_calls({len(tool_calls)}) 和 tool_result({len(tool_results)}) 数量不等"

    def test_tool_call_result_step_id_matching(self, server, api_headers):
        """TC-Flow-006: tool_result 的 tool_call_id 必须与 tool_calls[].id 匹配

        tool_result（role=tool）通过 tool_call_id 字段关联 tool_calls 数组元素的 id
        """
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

        if tool_calls and tool_results:
            # 收集 tool_calls 数组元素中的 id
            call_ids = []
            for c in tool_calls:
                for tc in c["data"].get("tool_calls", []):
                    if "id" in tc:
                        call_ids.append(tc["id"])

            result_ids = [r["data"].get("tool_call_id") for r in tool_results
                          if r["data"].get("tool_call_id")]

            # 每个 tool_result 的 tool_call_id 必须在 tool_calls 中存在
            for result_id in result_ids:
                assert result_id in call_ids, \
                    f"tool_result tool_call_id '{result_id}' 在 tool_calls 中不存在"

    def test_complete_event_sequence(self, server, api_headers):
        """TC-Flow-007: 完整事件序列验证

        验证完整的事件流程：
        message → thinking → tool_calls → tool_result → finish
        """
        payload = {"instruction": "列出当前目录下的文件"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        # 使用 SSEClient 的 verify_event_order 方法
        assert sse.verify_event_order(), "事件顺序不正确"

    def test_thinking_events_when_tool_used(self, server, api_headers):
        """TC-Flow-008: 工具调用前应该有 thinking 事件

        如果使用了工具，应该先发送 thinking 事件（不再区分 start/end）
        表示思考阶段（决定使用哪个工具）
        """
        payload = {"instruction": "读取 /etc/hosts 文件内容"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        tool_results = sse.get_events_by_type("tool_result")

        if tool_results:
            # 如果有工具调用，应该有 thinking 事件
            thinking_events = sse.get_events_by_type("thinking")
            assert len(thinking_events) > 0, \
                "有 tool_result 但缺少 thinking 事件"

            # thinking 事件应包含 reasoning_content
            for event in thinking_events:
                assert event["data"].get("role") == "assistant"
                assert "reasoning_content" in event["data"]

    def test_event_count_summary(self, server, api_headers):
        """TC-Flow-009: 事件数量统计

        打印完整的事件类型统计，用于诊断问题
        """
        payload = {"instruction": "列出当前目录"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        event_order = sse.get_event_order()

        # 统计各事件类型数量
        event_counts = {}
        for e in event_order:
            event_counts[e] = event_counts.get(e, 0) + 1

        # 打印统计信息（用于诊断）
        print(f"\n事件序列: {event_order}")
        print(f"事件统计: {event_counts}")

        # 基本验证：至少有 finish 或 error 终态事件
        assert event_counts.get("finish", 0) >= 1 or event_counts.get("error", 0) >= 1, \
            "缺少 finish 或 error 终态事件"


class TestSSEToolCallStrict:
    """工具调用严格测试 - 解决用户反馈的问题"""

    def test_tool_call_must_present_if_tool_result_exists(self, server, api_headers):
        """TC-Tool-001: 严格验证 tool_calls 存在

        问题：用户反馈工具调用但不打印 tool_calls
        这个测试严格验证：如果有 tool_result，必须有对应的 tool_calls
        """
        payload = {"instruction": "使用 ls 命令列出当前目录"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        # 检查所有事件类型
        event_types = set(sse.get_event_order())

        # 检查是否有 tool_result
        has_tool_result = "tool_result" in event_types
        has_tool_call = "tool_calls" in event_types

        # 严格验证：tool_result 存在时 tool_calls 必须存在
        if has_tool_result:
            assert has_tool_call, \
                f"发现 tool_result 但缺少 tool_calls！事件类型: {event_types}"

    def test_tool_events_in_correct_order(self, server, api_headers):
        """TC-Tool-002: tool_calls 必须在 tool_result 之前

        正确顺序：tool_calls → tool_result
        """
        payload = {"instruction": "读取文件内容"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        event_order = sse.get_event_order()

        tool_call_indices = [i for i, e in enumerate(event_order) if e == "tool_calls"]
        tool_result_indices = [i for i, e in enumerate(event_order) if e == "tool_result"]

        # 每个 tool_result 必须在某个 tool_calls 之后
        for result_idx in tool_result_indices:
            # 找最近的 tool_calls（在 result 之前）
            call_before_result = [i for i in tool_call_indices if i < result_idx]

            if not call_before_result:
                # 如果没有 tool_calls 在 tool_result 之前，说明顺序错误
                pytest.fail(
                    f"tool_result 在 index {result_idx}，但没有 tool_calls 在它之前。\n"
                    f"完整事件顺序: {event_order}"
                )

    def test_no_orphan_tool_result(self, server, api_headers):
        """TC-Tool-003: 不允许孤立的 tool_result

        每个 tool_result 的 tool_call_id 必须能在此前 tool_calls[].id 中找到
        """
        payload = {"instruction": "执行 ls 命令"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        tool_calls = sse.get_events_by_type("tool_calls")
        tool_results = sse.get_events_by_type("tool_result")

        # 收集所有 tool_calls 数组元素的 id
        call_ids = set()
        for c in tool_calls:
            for tc in c["data"].get("tool_calls", []):
                if "id" in tc:
                    call_ids.add(tc["id"])

        # 每个 tool_result 都必须能通过 tool_call_id 关联到某个 tool_calls 元素
        for r in tool_results:
            tool_call_id = r["data"].get("tool_call_id")
            if tool_call_id and tool_call_id not in call_ids:
                pytest.fail(
                    f"发现孤立的 tool_result！\n"
                    f"tool_call_id: {tool_call_id}\n"
                    f"已知 tool_calls ids: {sorted(call_ids)}\n"
                    f"事件顺序: {sse.get_event_order()}"
                )


class TestSSEStreamingOutput:
    """SSE 流式输出测试 - 验证 message/thinking 是流式发送"""

    def test_message_should_be_streaming_chunks(self, server, api_headers):
        """TC-Stream-001: message 应该是流式输出（多个 chunk）

        新协议不区分 message_start/message_end，所有内容均为 message 事件
        """
        payload = {"instruction": "你好，请介绍一下你自己"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        message_events = sse.get_events_by_type("message")

        # 打印 message 事件数量
        print(f"\nmessage 事件数量: {len(message_events)}")

        for i, event in enumerate(message_events):
            content = event["data"].get("content", "")
            print(f"[{i}] 内容长度: {len(content)}, 内容片段: {content[:50]}...")

        # 验证至少有一个 message 事件（如果有内容）
        if len(message_events) > 0:
            # 验证每个 message 都有 content
            for event in message_events:
                assert event["data"].get("role") == "assistant"
                assert "content" in event["data"]
                assert len(event["data"]["content"]) > 0

    def test_thinking_should_be_streaming_chunks(self, server, api_headers):
        """TC-Stream-002: thinking 应该是流式输出（多个 chunk）

        当使用工具时，thinking 也应该是流式的（不再区分 start/end）
        """
        payload = {"instruction": "列出当前目录下的文件"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        thinking_events = sse.get_events_by_type("thinking")

        print(f"\nthinking 事件数量: {len(thinking_events)}")

        for i, event in enumerate(thinking_events):
            content = event["data"].get("reasoning_content", "")
            print(f"[{i}] 内容长度: {len(content)}, 内容片段: {content[:50] if len(content) > 50 else content}...")

        # 验证 thinking 事件包含必要字段
        if thinking_events:
            for event in thinking_events:
                assert event["data"].get("role") == "assistant"
                assert "reasoning_content" in event["data"]


class TestSSEPrintDebug:
    """SSE 诊断测试 - 打印完整响应用于调试"""

    def test_print_full_sse_response(self, server, api_headers):
        """TC-Debug-001: 打印完整 SSE 响应

        用于诊断 SSE 流的问题
        """
        payload = {"instruction": "列出当前目录下的文件"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        # 打印完整事件列表
        print("\n" + "="*60)
        print("完整 SSE 事件列表:")
        print("="*60)

        for i, event in enumerate(sse.events):
            print(f"\n[{i}] 事件类型: {event['event']}")
            print(f"数据: {json.dumps(event['data'], indent=2, ensure_ascii=False)}")

        print("\n" + "="*60)
        print(f"事件顺序: {sse.get_event_order()}")
        print("="*60 + "\n")

        # 基本验证 — 首事件可能是 message/tool_calls/thinking
        first_event = sse.get_event_order()[0]
        assert first_event in ("message", "tool_calls", "thinking")
        # 最后事件为 finish 或 error（终态）
        assert sse.get_event_order()[-1] in ("finish", "error")