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
    assert_step_id_format
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

        # intent 必须是第一个事件
        assert order[0] == "intent"

        # completed 必须是最后一个事件
        assert order[-1] == "completed"

        # 使用 verify_event_order 方法验证整体顺序
        assert sse.verify_event_order()

    def test_intent_is_first_event(self, server, api_headers):
        """TC-SSE-002: intent 是首个事件"""
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        # intent 事件必须在最前
        events = sse.events
        assert events[0]["event"] == "intent"

        # intent 事件只有一个
        intent_events = sse.get_events_by_type("intent")
        assert len(intent_events) == 1

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

    def test_step_start_step_end_pairing(self, server, api_headers):
        """TC-SSE-004: step_start 和 step_end 成对"""
        payload = {"instruction": "帮我分析数据"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        step_starts = sse.get_events_by_type("step_start")
        step_ends = sse.get_events_by_type("step_end")

        # 数量应该相等
        assert len(step_starts) == len(step_ends)

        # 每个 step_end 的 step_id 应对应 step_start
        start_ids = [s["data"]["step_id"] for s in step_starts]
        end_ids = [e["data"]["step_id"] for e in step_ends]

        for end_id in end_ids:
            assert end_id in start_ids

    def test_progress_between_steps(self, server, api_headers):
        """TC-SSE-005: progress 事件在 step_start 和 step_end 之间"""
        payload = {"instruction": "帮我分析数据"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        progress_events = sse.get_events_by_type("progress")
        order = sse.get_event_order()

        # 如果有 progress 事件
        if progress_events:
            # progress 应出现在 intent 之后
            first_progress_idx = order.index("progress")
            assert first_progress_idx > 0
            assert order[first_progress_idx - 1] in ["intent", "step_start", "progress"]

            # progress 应出现在 completed 之前
            last_progress_idx = max(
                i for i, e in enumerate(order) if e == "progress"
            )
            assert last_progress_idx < len(order) - 1


class TestSSEEventFields:
    """SSE 事件字段完整性测试"""

    def test_intent_event_fields(self, server, api_headers):
        """TC-SSE-006: intent 事件字段验证"""
        payload = {"instruction": "测试"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        intent = sse.get_intent_event()

        assert intent is not None
        data = intent["data"]

        # 必须包含 timestamp
        assert "timestamp" in data
        assert data["timestamp"]  # 不为空

        # timestamp 应为 ISO 格式
        timestamp = data["timestamp"]
        assert "T" in timestamp  # ISO 格式包含 T

    def test_step_start_event_fields(self, server, api_headers):
        """TC-SSE-007: step_start 事件字段验证"""
        payload = {"instruction": "帮我分析数据"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        step_starts = sse.get_events_by_type("step_start")

        if step_starts:
            step = step_starts[0]["data"]

            # 必填字段
            assert "type" in step
            assert "name" in step
            assert "step_id" in step
            assert "timestamp" in step

            # 验证 type 可选值
            assert step["type"] in ["skill", "tool", "llm"]

            # 验证 step_id 格式
            assert_step_id_format(step["step_id"])

            # 验证 nesting_level 存在（默认0）
            assert "nesting_level" in step
            assert isinstance(step["nesting_level"], int)
            assert step["nesting_level"] >= 0

    def test_step_end_event_fields(self, server, api_headers):
        """TC-SSE-008: step_end 事件字段验证"""
        payload = {"instruction": "帮我分析数据"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        step_ends = sse.get_events_by_type("step_end")

        if step_ends:
            step = step_ends[0]["data"]

            # 必填字段
            assert "step_id" in step
            assert "timestamp" in step
            assert "status" in step

            # 验证 status 可选值
            assert step["status"] in ["success", "failed"]

            # 失败时应包含 error
            if step["status"] == "failed":
                assert "error" in step
                assert "code" in step["error"]
                assert "message" in step["error"]

    def test_progress_event_fields(self, server, api_headers):
        """TC-SSE-009: progress 事件字段验证"""
        payload = {"instruction": "帮我分析数据"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        progress_events = sse.get_events_by_type("progress")

        if progress_events:
            progress = progress_events[0]["data"]

            # 必填字段
            assert "message" in progress
            assert "timestamp" in progress

            # 可选字段：step_id
            if "step_id" in progress:
                assert progress["step_id"]

    def test_completed_event_fields(self, server, api_headers):
        """TC-SSE-010: completed 事件字段验证"""
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
        """TC-SSE-011: 取消对话 completed 事件验证"""
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
        """TC-SSE-012: 多轮对话 round 递增"""
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
        """TC-SSE-013: 无效 session_id round 为 1"""
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