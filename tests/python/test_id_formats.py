"""
ID 格式验证测试和 nesting_level 测试
测试 session_id、chat_id、step_id 格式以及 nesting_level 字段
"""

import pytest
import requests
from conftest import (
    BASE_URL,
    SSEClient,
    assert_session_id_format,
    assert_chat_id_format,
    assert_step_id_format
)


class TestSessionIdFormat:
    """session_id 格式测试"""

    def test_new_session_id_format(self, server, api_headers):
        """TC-ID-001: 新会话 session_id 格式"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")

        # 验证格式
        assert_session_id_format(session_id)

        # 新格式：{YYYYMMDDHHMMSSmmm}_{random4}
        # 无 sess_ 前缀
        assert not session_id.startswith("sess_")

        SSEClient(response)

    def test_session_id_no_prefix(self, server, api_headers):
        """TC-ID-002: session_id 无 sess_ 前缀"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")

        # 验证无前缀
        assert not session_id.startswith("sess_")

        # 验证通过 API 查询
        SSEClient(response)

        detail_response = requests.get(
            f"{BASE_URL}/sess/{session_id}",
            headers=api_headers
        )

        data = detail_response.json()

        # API 返回的 session_id 也应无前缀
        assert not data["session_id"].startswith("sess_")

        # path 中也应无前缀
        path = data["session"]["path"]
        assert "sess_" not in path

    def test_session_id_timestamp_accuracy(self, server, api_headers):
        """TC-ID-003: session_id 时间戳精度"""
        import time
        from datetime import datetime

        # 记录当前时间
        before_time = datetime.now()

        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        after_time = datetime.now()

        session_id = response.headers.get("X-Session-ID")

        # 提取时间戳部分
        timestamp_part = session_id.split("_")[0]

        # 验证时间戳在合理范围
        # 格式：YYYYMMDDHHMMSSmmm
        year = int(timestamp_part[:4])
        month = int(timestamp_part[4:6])
        day = int(timestamp_part[6:8])
        hour = int(timestamp_part[8:10])
        minute = int(timestamp_part[10:12])
        second = int(timestamp_part[12:14])
        millisecond = int(timestamp_part[14:17])

        # 验证值范围
        assert 2026 <= year <= 2100
        assert 1 <= month <= 12
        assert 1 <= day <= 31
        assert 0 <= hour <= 23
        assert 0 <= minute <= 59
        assert 0 <= second <= 59
        assert 0 <= millisecond <= 999

        SSEClient(response)

    def test_session_id_random_length(self, server, api_headers):
        """TC-ID-004: session_id 随机部分长度"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")

        # 验证随机部分
        random_part = session_id.split("_")[1]

        assert len(random_part) == 4
        assert random_part.isalnum()  # 字母或数字

        SSEClient(response)


class TestChatIdFormat:
    """chat_id 格式测试"""

    def test_chat_id_format(self, server, api_headers):
        """TC-ID-005: chat_id 格式"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        chat_id = response.headers.get("X-Chat-ID")

        # 验证格式
        assert_chat_id_format(chat_id)

        # 格式：{YYYYMMDDHHMMSSmmm}，17 位纯数字，无前缀
        assert len(chat_id) == 17

        SSEClient(response)

    def test_chat_id_no_prefix(self, server, api_headers):
        """TC-ID-006: chat_id 为 17 位纯数字时间戳（无 chat_ 前缀）"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        chat_id = response.headers.get("X-Chat-ID")

        # 验证无前缀的纯时间戳格式
        assert not chat_id.startswith("chat_")

        assert len(chat_id) == 17
        assert chat_id.isdigit()

        SSEClient(response)

    def test_chat_id_changes_per_round(self, server, api_headers):
        """TC-ID-007: 每轮对话 chat_id 不同"""
        # 第一轮
        payload1 = {"instruction": "第一轮"}

        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload1,
            stream=True
        )

        chat_id1 = response1.headers.get("X-Chat-ID")
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

        chat_id2 = response2.headers.get("X-Chat-ID")

        SSEClient(response2)

        # 验证 chat_id 不同
        assert chat_id1 != chat_id2

        # 验证格式相同
        assert_chat_id_format(chat_id1)
        assert_chat_id_format(chat_id2)


class TestStepIdFormat:
    """step_id 格式测试

    SSE 事件负载不含 step_id 字段，步骤信息（含 step_id）通过
    GET /chat/:sid 详情接口的 steps[] 获取。
    """

    @staticmethod
    def _get_steps(api_headers, instruction="帮我分析数据"):
        """执行一次对话并通过详情接口返回 steps 列表"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": instruction},
            stream=True
        )
        session_id = response.headers.get("X-Session-ID")
        SSEClient(response)  # 等待对话完成

        detail_response = requests.get(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )
        assert detail_response.status_code == 200
        chat = detail_response.json()["chat"]
        return chat.get("steps") or []

    def test_step_id_format(self, server, api_headers):
        """TC-ID-008: step_id 格式"""
        steps = self._get_steps(api_headers)

        for step in steps:
            step_id = step["step_id"]

            # 验证格式
            assert_step_id_format(step_id)

            # 格式：{YYYYMMDD}-{HHMMSSmmm}-{random6}
            parts = step_id.split("-")

            assert len(parts) == 3
            assert len(parts[0]) == 8  # 日期
            assert len(parts[1]) == 9  # 时间（含毫秒）
            assert len(parts[2]) == 6  # 随机

    def test_step_id_uniqueness(self, server, api_headers):
        """TC-ID-009: step_id 唯一性"""
        steps = self._get_steps(api_headers)

        step_ids = [s["step_id"] for s in steps]

        # 验证唯一性
        assert len(step_ids) == len(set(step_ids))

    def test_step_id_pairing(self, server, api_headers):
        """TC-ID-010: 步骤 step_id 唯一性验证（详情接口 steps 包含步骤信息）"""
        steps = self._get_steps(api_headers)

        step_ids = [s["step_id"] for s in steps if "step_id" in s]

        # 验证唯一性
        assert len(step_ids) == len(set(step_ids))


class TestNestingLevel:
    """nesting_level 测试

    SSE 事件负载不含 nesting_level 字段，
    该字段只出现在 GET /chat/:sid 详情接口的 steps[] 中。
    """

    def test_nesting_level_in_chat_record(self, server, api_headers):
        """TC-NEST-005: chat 记录包含 nesting_level"""
        payload = {"instruction": "帮我分析数据"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")

        SSEClient(response)

        # 查询对话详情
        detail_response = requests.get(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )

        data = detail_response.json()
        chat = data["chat"]

        if chat["steps"]:
            for step in chat["steps"]:
                # 验证 steps 中包含 nesting_level
                assert "nesting_level" in step
                assert isinstance(step["nesting_level"], int)


class TestIDGenerationUniqueness:
    """ID 生成唯一性测试"""

    def test_multiple_sessions_unique_ids(self, server, api_headers):
        """TC-ID-011: 多次会话 session_id 唯一"""
        session_ids = []

        for i in range(10):
            payload = {"instruction": f"测试{i}"}

            response = requests.post(
                f"{BASE_URL}/chat",
                headers=api_headers,
                json=payload,
                stream=True
            )

            session_id = response.headers.get("X-Session-ID")
            session_ids.append(session_id)

            SSEClient(response)

        # 验证唯一性
        assert len(session_ids) == len(set(session_ids))

    def test_multiple_chats_unique_ids(self, server, api_headers):
        """TC-ID-012: 多轮对话 chat_id 唯一"""
        # 第一轮
        payload1 = {"instruction": "第一轮"}

        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload1,
            stream=True
        )

        session_id = response1.headers.get("X-Session-ID")
        chat_ids = [response1.headers.get("X-Chat-ID")]

        SSEClient(response1)

        # 多轮对话
        for i in range(5):
            headers = api_headers.copy()
            headers["X-Session-ID"] = session_id

            payload = {"instruction": f"第{i+2}轮"}

            response = requests.post(
                f"{BASE_URL}/chat",
                headers=headers,
                json=payload,
                stream=True
            )

            chat_ids.append(response.headers.get("X-Chat-ID"))
            SSEClient(response)

        # 验证唯一性
        assert len(chat_ids) == len(set(chat_ids))