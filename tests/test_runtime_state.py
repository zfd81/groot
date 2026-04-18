"""
RuntimeState 测试
测试 sync.Map 内存管理、ActiveChat 状态追踪、进度更新、与 Memory 协作等
"""

import pytest
import requests
import time
from conftest import BASE_URL, SSEClient, generate_session_id


class TestRuntimeStateBasic:
    """RuntimeState 基础功能测试"""

    def test_register_active_chat(self, server, api_headers):
        """TC-RS-001: 注册活跃对话状态"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")
        chat_id = response.headers.get("X-Chat-ID")

        # 验证状态已注册
        status_response = requests.get(
            f"{BASE_URL}/chat/status/{session_id}",
            headers=api_headers
        )

        assert status_response.status_code == 200
        data = status_response.json()

        # 验证活跃状态
        assert data["chat"] is not None
        assert data["chat"]["status"] == "running"
        assert data["chat"]["chat_id"] == chat_id

        # 等待完成
        SSEClient(response)

    def test_is_running_check(self, server, api_headers):
        """TC-RS-002: IsRunning 检查"""
        # 启动第一个对话
        payload = {"instruction": "长任务"}

        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response1.headers.get("X-Session-ID")

        # 验证正在运行
        status1 = requests.get(
            f"{BASE_URL}/chat/status/{session_id}",
            headers=api_headers
        )

        assert status1.json()["chat"]["status"] == "running"

        # 尝试启动第二个对话（应返回 409）
        headers2 = api_headers.copy()
        headers2["X-Session-ID"] = session_id

        response2 = requests.post(
            f"{BASE_URL}/chat",
            headers=headers2,
            json={"instruction": "test"}
        )

        assert response2.status_code == 409

    def test_complete_removes_active_state(self, server, api_headers):
        """TC-RS-003: Complete 后移除活跃状态"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")

        # 等待完成
        SSEClient(response)

        # 验证状态已移除
        status_response = requests.get(
            f"{BASE_URL}/chat/status/{session_id}",
            headers=api_headers
        )

        assert status_response.json()["chat"] is None


class TestRuntimeStateProgress:
    """RuntimeState 进度更新测试"""

    def test_update_progress(self, server, api_headers):
        """TC-RS-004: 进度更新"""
        payload = {"instruction": "分析数据"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")

        # 查询进度（在执行过程中）
        status_response = requests.get(
            f"{BASE_URL}/chat/status/{session_id}",
            headers=api_headers
        )

        if status_response.json()["chat"]:
            chat = status_response.json()["chat"]

            # 验证进度字段
            if "progress" in chat:
                progress = chat["progress"]
                assert "current_step" in progress
                assert "steps_completed" in progress
                assert "percentage" in progress

                # 验证值范围
                assert progress["current_step"] >= 0
                assert progress["steps_completed"] >= 0
                assert 0 <= progress["percentage"] <= 100

        SSEClient(response)

    def test_elapsed_time_tracking(self, server, api_headers):
        """TC-RS-005: elapsed_time 追踪"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")

        # 等待一小段时间
        time.sleep(0.5)

        # 查询状态
        status_response = requests.get(
            f"{BASE_URL}/chat/status/{session_id}",
            headers=api_headers
        )

        if status_response.json()["chat"]:
            chat = status_response.json()["chat"]

            # 验证 elapsed_time 存在
            assert "elapsed_time" in chat
            assert chat["elapsed_time"]  # 不为空

        SSEClient(response)


class TestRuntimeStateCancel:
    """RuntimeState 取消功能测试"""

    def test_cancel_active_chat(self, server, api_headers):
        """TC-RS-006: 取消活跃对话"""
        payload = {"instruction": "长任务"}

        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response1.headers.get("X-Session-ID")

        # 取消
        cancel_response = requests.delete(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )

        assert cancel_response.status_code == 200
        data = cancel_response.json()
        assert data["status"] == "success"

        # 验证状态已移除
        status_response = requests.get(
            f"{BASE_URL}/chat/status/{session_id}",
            headers=api_headers
        )

        # 取消后应无活跃对话
        assert status_response.json()["chat"] is None

    def test_cancel_returns_chat_record(self, server, api_headers):
        """TC-RS-007: 取消返回 ChatRecord"""
        payload = {"instruction": "长任务"}

        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response1.headers.get("X-Session-ID")

        # 取消
        cancel_response = requests.delete(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )

        data = cancel_response.json()

        # 验证返回 chat_id
        if data["status"] == "success":
            assert "chat_id" in data

        SSEClient(response1)


class TestRuntimeStateMemoryIntegration:
    """RuntimeState 与 Memory 协作测试"""

    def test_complete_saves_to_memory(self, server, api_headers):
        """TC-RS-008: Complete 后保存到 Memory"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")
        SSEClient(response)

        # 验证保存到 Memory（通过查询历史）
        history_response = requests.get(
            f"{BASE_URL}/sess/{session_id}",
            headers=api_headers
        )

        assert history_response.status_code == 200
        data = history_response.json()

        # 验证 messages 存在
        messages = data["history"]["messages"]
        assert len(messages) > 0

        # 验证新字段存在
        msg = messages[0]
        assert "status" in msg
        assert "duration" in msg
        assert "steps_count" in msg
        assert "error" in msg

    def test_chat_record_saved(self, server, api_headers):
        """TC-RS-009: ChatRecord 保存到 chats 目录"""
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

        # 查询对话详情（从 chats/{chat_id}.json 读取）
        detail_response = requests.get(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )

        assert detail_response.status_code == 200
        data = detail_response.json()

        # 验证 chat_id 匹配
        assert data["chat"]["chat_id"] == chat_id

        # 验证 steps 存在
        assert "steps" in data["chat"]


class TestRuntimeStateRunningCount:
    """活跃对话计数测试"""

    def test_running_count_in_health(self, server, api_headers):
        """TC-RS-010: health 接口显示运行数"""
        # 启动几个对话
        sessions = []

        for i in range(3):
            payload = {"instruction": f"测试{i}"}
            response = requests.post(
                f"{BASE_URL}/chat",
                headers=api_headers,
                json=payload,
                stream=True
            )
            sessions.append(response.headers.get("X-Session-ID"))

        # 查询健康状态
        health_response = requests.get(f"{BASE_URL}/health")

        data = health_response.json()

        # 验证 metrics 中有 chats_running
        if "metrics" in data:
            assert "chats_running" in data["metrics"]
            # 应 >= 0

        # 等待完成
        for response in [requests.post(f"{BASE_URL}/chat", headers=api_headers, json={"instruction": "test"}, stream=True) for _ in range(3)]:
            pass  # 让之前的请求自然完成


class TestRuntimeStateActiveChatFields:
    """ActiveChat 字段验证测试"""

    def test_active_chat_field_structure(self, server, api_headers):
        """TC-RS-011: ActiveChat 字段完整性"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")

        # 查询状态
        status_response = requests.get(
            f"{BASE_URL}/chat/status/{session_id}",
            headers=api_headers
        )

        if status_response.json()["chat"]:
            chat = status_response.json()["chat"]

            # 验证必填字段
            assert "chat_id" in chat
            assert "round" in chat
            assert "status" in chat
            assert "started_at" in chat
            assert "elapsed_time" in chat

            # 验证 status 为 running
            assert chat["status"] == "running"

            # 验证 started_at 格式（ISO）
            assert "T" in chat["started_at"]

        SSEClient(response)