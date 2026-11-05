"""
RuntimeState 测试
测试 sync.Map 内存管理、ActiveChat 状态追踪、进度更新、与 Memory 协作等
"""

import json

import pytest
import requests
import time
from conftest import BASE_URL, SSEClient, generate_session_id


def read_first_chunk_fields(response, *keys):
    """从流式响应的前若干个 data: chunk 中取首个出现的顶层字段

    流式响应不再携带会话标识响应头，会话标识由每个 chunk
    的顶层 session_id / id 承载。这里只读到取齐所需字段即停止，不消费后续事件，
    以便在对话仍在执行时查询 /chat/status。

    返回 ``(values, iterator)``：``iterator`` 是从 ``response.iter_lines()`` 取到的
    同一个生成器，必须由调用方交给 :func:`drain` 继续消费。若把它丢弃（生成器被
    回收），requests 会立即关闭底层连接（不再回池），后续 drain 就什么
    也读不到，断言将在对话仍在执行时求值。

    注意：只适用于「服务端不感知客户端断连」的前提（``internal/api/server.go`` 未开启
    ``WithSenseClientDisconnection``）；本文件里需要主动中止请求的用例会显式
    ``response.close()``，不依赖该前提。
    """
    it = response.iter_lines()
    wanted = {k: None for k in keys}
    for line in it:
        if not line:
            continue
        text = line.decode("utf-8") if isinstance(line, bytes) else line
        text = text.strip()
        if not text.startswith("data:"):
            continue
        body = text[5:].strip()
        if not body or body == "[DONE]":
            continue
        try:
            chunk = json.loads(body)
        except json.JSONDecodeError:
            continue
        for k in keys:
            if wanted[k] is None and chunk.get(k):
                wanted[k] = chunk[k]
        if all(v is not None for v in wanted.values()):
            break
    return tuple(wanted[k] for k in keys), it


def drain(it):
    """消费迭代器里剩余的 SSE 行，等待对话执行完毕"""
    for _ in it:
        pass


class TestRuntimeStateBasic:
    """RuntimeState 基础功能测试"""

    def test_register_active_chat(self, server, api_headers):
        """TC-RS-001: 注册活跃对话状态"""
        payload = {"messages": [{"role": "user", "content": "测试对话"}], "stream": True}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        (session_id, chat_id), it = read_first_chunk_fields(response, "session_id", "id")

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
        drain(it)

    def test_is_running_check(self, server, api_headers):
        """TC-RS-002: IsRunning 检查"""
        # 启动第一个对话
        payload = {"messages": [{"role": "user", "content": "长任务"}], "stream": True}

        response1 = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        (session_id,), it1 = read_first_chunk_fields(response1, "session_id")

        # 验证正在运行
        status1 = requests.get(
            f"{BASE_URL}/chat/status/{session_id}",
            headers=api_headers
        )

        assert status1.json()["chat"]["status"] == "running"

        # 尝试启动第二个对话（应返回 409）
        response2 = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json={
                "messages": [{"role": "user", "content": "test"}],
                "session_id": session_id,
                "stream": True,
            }
        )

        assert response2.status_code == 409

        drain(it1)

    def test_complete_removes_active_state(self, server, api_headers):
        """TC-RS-003: Complete 后移除活跃状态"""
        payload = {"messages": [{"role": "user", "content": "测试对话"}], "stream": True}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        (session_id,), it = read_first_chunk_fields(response, "session_id")

        # 等待完成
        drain(it)

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
        payload = {"messages": [{"role": "user", "content": "分析数据"}], "stream": True}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        (session_id,), it = read_first_chunk_fields(response, "session_id")

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

        drain(it)

    def test_elapsed_time_tracking(self, server, api_headers):
        """TC-RS-005: elapsed_time 追踪"""
        payload = {"messages": [{"role": "user", "content": "测试对话"}], "stream": True}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        (session_id,), it = read_first_chunk_fields(response, "session_id")

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

        drain(it)


class TestRuntimeStateCancel:
    """RuntimeState 取消功能测试"""

    def test_cancel_active_chat(self, server, api_headers):
        """TC-RS-006: 取消活跃对话（DELETE 端点已删除，SSE 断开即取消）"""
        payload = {"messages": [{"role": "user", "content": "长任务"}], "stream": True}

        response1 = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        (session_id,), _it = read_first_chunk_fields(response1, "session_id")

        # DELETE 端点已删除，返回 404
        cancel_response = requests.delete(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )
        assert cancel_response.status_code == 404

        # 关闭 SSE 流即取消（此处刻意不 drain，直接断连）
        response1.close()

        # 等服务器处理取消
        import time
        time.sleep(1)

        # 验证状态已移除
        status_response = requests.get(
            f"{BASE_URL}/chat/status/{session_id}",
            headers=api_headers
        )
        status_data = status_response.json()

        # SSE 断开后，服务器可能仍在处理中或已完成
        assert status_data["chat"] is None or status_data.get("status") in ("idle", "success")

    def test_cancel_returns_chat_record(self, server, api_headers):
        """TC-RS-007: 取消后仍可查看对话记录"""
        payload = {"messages": [{"role": "user", "content": "测试任务"}], "stream": True}

        response1 = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        (session_id,), _it = read_first_chunk_fields(response1, "session_id")

        # DELETE 端点已删除，返回 404
        cancel_response = requests.delete(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )
        assert cancel_response.status_code == 404

        # 关闭 SSE 流即取消（此处刻意不 drain，直接断连）
        response1.close()

        # 等待服务器生成 chat record
        import time
        time.sleep(1)

        # 验证可通过 GET /chat/{sid} 查询对话记录
        detail_response = requests.get(
            f"{BASE_URL}/chat/{session_id}",
            headers=api_headers
        )
        assert detail_response.status_code == 200
        detail_data = detail_response.json()
        assert detail_data.get("status") in ("success", "idle")


class TestRuntimeStateMemoryIntegration:
    """RuntimeState 与 Memory 协作测试"""

    def test_complete_saves_to_memory(self, server, api_headers):
        """TC-RS-008: Complete 后保存到 Memory"""
        payload = {"messages": [{"role": "user", "content": "测试对话"}], "stream": True}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        (session_id,), it = read_first_chunk_fields(response, "session_id")
        drain(it)

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
        payload = {"messages": [{"role": "user", "content": "测试对话"}], "stream": True}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        (session_id, chat_id), it = read_first_chunk_fields(response, "session_id", "id")
        drain(it)

        # 查询对话详情
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
        iterators = []

        for i in range(3):
            payload = {"messages": [{"role": "user", "content": f"测试{i}"}], "stream": True}
            response = requests.post(
                f"{BASE_URL}/chat/completions",
                headers=api_headers,
                json=payload,
                stream=True
            )
            (session_id,), it = read_first_chunk_fields(response, "session_id")
            sessions.append(session_id)
            iterators.append(it)

        # 查询健康状态
        health_response = requests.get(f"{BASE_URL}/web/health")

        data = health_response.json()

        # 验证 metrics 中有 chats_running
        if "metrics" in data:
            assert "chats_running" in data["metrics"]
            # 应 >= 0

        # 等待完成
        for response in [
            requests.post(
                f"{BASE_URL}/chat/completions",
                headers=api_headers,
                json={"messages": [{"role": "user", "content": "test"}], "stream": True},
                stream=True,
            )
            for _ in range(3)
        ]:
            pass  # 让之前的请求自然完成

        # 用存活的迭代器把前 3 个对话读完（丢弃迭代器会立刻断开连接）
        for it in iterators:
            drain(it)


class TestRuntimeStateActiveChatFields:
    """ActiveChat 字段验证测试"""

    def test_active_chat_field_structure(self, server, api_headers):
        """TC-RS-011: ActiveChat 字段完整性"""
        payload = {"messages": [{"role": "user", "content": "测试对话"}], "stream": True}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        (session_id,), it = read_first_chunk_fields(response, "session_id")

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

        drain(it)
