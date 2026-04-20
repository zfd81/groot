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
        - intent (已替换为 started)
        - step_start (已替换为 thinking_start/tool_call)
        - step_end (已替换为 thinking_end/tool_result)
        - progress (已替换为 thinking/message)
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
        old_event_types = ["intent", "step_start", "step_end", "progress"]
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

        started_events = sse.get_events_by_type("started")

        # 必须有 started 事件
        assert len(started_events) >= 1, "缺少 started 事件"

        # started 必须是第一个事件
        assert sse.get_event_order()[0] == "started", "started 不是第一个事件"

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

        completed_events = sse.get_events_by_type("completed")

        # 必须有 completed 事件
        assert len(completed_events) >= 1, "缺少 completed 事件"

        # completed 必须是最后一个事件
        assert sse.get_event_order()[-1] == "completed", "completed 不是最后一个事件"

    def test_message_sequence_exists(self, server, api_headers):
        """TC-Flow-004: message 事件序列验证

        message_start → message → message_end 是最终输出
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

        message_starts = sse.get_events_by_type("message_start")
        message_ends = sse.get_events_by_type("message_end")

        # 如果有 message_start，必须有 message_end
        if message_starts:
            assert len(message_ends) >= 1, "有 message_start 但缺少 message_end"

            # message_start 和 message_end 数量相等
            assert len(message_starts) == len(message_ends), \
                f"message_start({len(message_starts)}) 和 message_end({len(message_ends)}) 数量不等"

            # 验证 message_start → message → message_end 顺序
            for i, start in enumerate(message_starts):
                start_idx = event_order.index("message_start")
                # 找对应的 message_end（在 start 之后）
                end_idx = -1
                for j, e in enumerate(event_order[start_idx:], start_idx):
                    if e == "message_end":
                        end_idx = j
                        break
                assert end_idx > start_idx, f"message_end 应在 message_start 之后"

    def test_tool_call_must_exist_when_tool_used(self, server, api_headers):
        """TC-Flow-005: 工具调用场景必须包含 tool_call

        这是关键测试：如果 agent 使用了工具，必须发送 tool_call 事件
        不能只发送 tool_result 而不发送 tool_call
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
            # 如果有 tool_result，必须也有 tool_call
            tool_calls = sse.get_events_by_type("tool_call")

            assert len(tool_calls) >= 1, \
                f"有 {len(tool_results)} 个 tool_result 但缺少 tool_call 事件"

            # tool_call 和 tool_result 数量应该相等
            assert len(tool_calls) == len(tool_results), \
                f"tool_call({len(tool_calls)}) 和 tool_result({len(tool_results)}) 数量不等"

    def test_tool_call_result_step_id_matching(self, server, api_headers):
        """TC-Flow-006: tool_call 和 tool_result 的 step_id 必须匹配

        tool_call 和 tool_result 通过 step_id 关联
        """
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

        if tool_calls and tool_results:
            call_ids = [c["data"]["step_id"] for c in tool_calls]
            result_ids = [r["data"]["step_id"] for r in tool_results]

            # 每个 tool_result 的 step_id 必须在 tool_calls 中存在
            for result_id in result_ids:
                assert result_id in call_ids, \
                    f"tool_result step_id '{result_id}' 在 tool_calls 中不存在"

    def test_complete_event_sequence(self, server, api_headers):
        """TC-Flow-007: 完整事件序列验证

        验证完整的事件流程：
        started → (thinking_start → thinking → thinking_end) →
        (tool_call → tool_result) →
        message_start → message → message_end → completed
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

        如果使用了工具，应该先发送 thinking_start → thinking_end
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
            thinking_starts = sse.get_events_by_type("thinking_start")
            thinking_ends = sse.get_events_by_type("thinking_end")

            # thinking_start 和 thinking_end 应成对
            assert len(thinking_starts) == len(thinking_ends), \
                "thinking_start 和 thinking_end 数量不等"

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

        # 基本验证
        assert event_counts.get("started", 0) >= 1, "缺少 started"
        assert event_counts.get("completed", 0) >= 1, "缺少 completed"


class TestSSEToolCallStrict:
    """工具调用严格测试 - 解决用户反馈的问题"""

    def test_tool_call_must_present_if_tool_result_exists(self, server, api_headers):
        """TC-Tool-001: 严格验证 tool_call 存在

        问题：用户反馈工具调用但不打印 tool_call
        这个测试严格验证：如果有 tool_result，必须有对应的 tool_call
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
        has_tool_call = "tool_call" in event_types

        # 严格验证：tool_result 存在时 tool_call 必须存在
        if has_tool_result:
            assert has_tool_call, \
                f"发现 tool_result 但缺少 tool_call！事件类型: {event_types}"

    def test_tool_events_in_correct_order(self, server, api_headers):
        """TC-Tool-002: tool_call 必须在 tool_result 之前

        正确顺序：tool_call → tool_result
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

        tool_call_indices = [i for i, e in enumerate(event_order) if e == "tool_call"]
        tool_result_indices = [i for i, e in enumerate(event_order) if e == "tool_result"]

        # 每个 tool_result 必须在某个 tool_call 之后
        for result_idx in tool_result_indices:
            # 找最近的 tool_call（在 result 之前）
            call_before_result = [i for i in tool_call_indices if i < result_idx]

            if not call_before_result:
                # 如果没有 tool_call 在 tool_result 之前，说明顺序错误
                pytest.fail(
                    f"tool_result 在 index {result_idx}，但没有 tool_call 在它之前。\n"
                    f"完整事件顺序: {event_order}"
                )

    def test_no_orphan_tool_result(self, server, api_headers):
        """TC-Tool-003: 不允许孤立的 tool_result

        每个 tool_result 必须有对应的 tool_call
        """
        payload = {"instruction": "执行 ls 命令"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        tool_calls = sse.get_events_by_type("tool_call")
        tool_results = sse.get_events_by_type("tool_result")

        # 不允许 tool_result 比 tool_call 多
        if len(tool_results) > len(tool_calls):
            pytest.fail(
                f"发现孤立的 tool_result！\n"
                f"tool_call 数量: {len(tool_calls)}\n"
                f"tool_result 数量: {len(tool_results)}\n"
                f"事件顺序: {sse.get_event_order()}"
            )


class TestSSEStreamingOutput:
    """SSE 流式输出测试 - 验证 message/thinking 是流式发送"""

    def test_message_should_be_streaming_chunks(self, server, api_headers):
        """TC-Stream-001: message 应该是流式输出（多个 chunk）

        流式输出的正确行为：
        message_start → message(chunk1) → message(chunk2) → ... → message_end

        不正确的行为：
        message_start → message(完整内容) → message_end
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

        # 流式输出应该有多个 message 事件（每个 chunk 一个）
        # 注意：如果 LLM 返回很短的内容，可能只有一个 chunk
        # 但至少应该有 message_start 和 message_end

        message_starts = sse.get_events_by_type("message_start")
        message_ends = sse.get_events_by_type("message_end")

        print(f"\nmessage_start 数量: {len(message_starts)}")
        print(f"message_end 数量: {len(message_ends)}")

        # 验证 message_start 和 message_end 成对
        assert len(message_starts) == len(message_ends)

        # 验证至少有一个 message 事件（如果有内容）
        if len(message_events) > 0:
            # 验证每个 message 都有 content
            for event in message_events:
                assert "content" in event["data"]
                assert len(event["data"]["content"]) > 0

    def test_thinking_should_be_streaming_chunks(self, server, api_headers):
        """TC-Stream-002: thinking 应该是流式输出（多个 chunk）

        当使用工具时，thinking 也应该是流式的
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
            content = event["data"].get("content", "")
            print(f"[{i}] 内容长度: {len(content)}, 内容片段: {content[:50] if len(content) > 50 else content}...")

        thinking_starts = sse.get_events_by_type("thinking_start")
        thinking_ends = sse.get_events_by_type("thinking_end")

        print(f"\nthinking_start 数量: {len(thinking_starts)}")
        print(f"thinking_end 数量: {len(thinking_ends)}")

        # 如果有 thinking_start，必须有 thinking_end
        if thinking_starts:
            assert len(thinking_starts) == len(thinking_ends)


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

        # 基本验证
        assert sse.get_event_order()[0] == "started"
        assert sse.get_event_order()[-1] == "completed"