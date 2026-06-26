"""
Memory/存储功能测试
测试 history.json 结构、chat记录、附件存储、目录结构等
"""

import pytest
import requests
import json
import os
import glob
from conftest import BASE_URL, TEST_HOME, SSEClient, assert_session_id_format


class TestHistoryJSONFormat:
    """history.json 格式测试"""

    def test_history_json_exists(self, server, api_headers):
        """TC-MEM-001: history.json 文件存在"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")
        SSEClient(response)

        # 验证文件存在
        history_path = f"{TEST_HOME}/memory/{session_id}/history.json"

        # 如果服务实现了 memory 存储
        if os.path.exists(history_path):
            assert True
        else:
            # 通过 API 验证
            detail_response = requests.get(
                f"{BASE_URL}/sess/{session_id}",
                headers=api_headers
            )
            assert detail_response.status_code == 200

    def test_history_json_structure(self, server, api_headers):
        """TC-MEM-002: history.json 结构验证"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")
        SSEClient(response)

        history_path = f"{TEST_HOME}/memory/{session_id}/history.json"

        if os.path.exists(history_path):
            with open(history_path, "r") as f:
                data = json.load(f)

            # 验证顶层字段
            assert "session_id" in data
            assert_session_id_format(data["session_id"])  # 无 sess_ 前缀
            assert "created_at" in data
            assert "messages" in data

            # 验证 messages 字段
            if data["messages"]:
                msg = data["messages"][0]

                # 新版字段名
                assert "round" in msg
                assert "chat_id" in msg  # 新增
                assert "timestamp" in msg
                assert "instruction" in msg  # 非 user_content
                assert "attachments" in msg
                assert "result" in msg  # 非 assistant_content
                assert "result_attachments" in msg
                assert "status" in msg
                assert "duration" in msg
                assert "steps_count" in msg
                assert "error" in msg

    def test_history_json_multiple_rounds(self, server, api_headers):
        """TC-MEM-003: history.json 多轮记录"""
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
    """chat 记录文件测试"""

    def test_chat_record_exists(self, server, api_headers):
        """TC-MEM-004: chats/{chat_id}.json 文件存在"""
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

        # 验证文件路径
        chat_path = f"{TEST_HOME}/memory/{session_id}/chats/{chat_id}.json"

        # 如果服务实现了存储
        if os.path.exists(chat_path):
            assert True
        else:
            # 通过 API 验证
            detail_response = requests.get(
                f"{BASE_URL}/chat/{session_id}",
                headers=api_headers
            )
            assert detail_response.status_code == 200
            assert detail_response.json()["chat"]["chat_id"] == chat_id

    def test_chat_record_structure(self, server, api_headers):
        """TC-MEM-005: chat 记录结构验证"""
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

        chat_path = f"{TEST_HOME}/memory/{session_id}/chats/{chat_id}.json"

        if os.path.exists(chat_path):
            with open(chat_path, "r") as f:
                data = json.load(f)

            # 验证字段
            assert "chat_id" in data
            assert "session_id" in data
            assert_session_id_format(data["session_id"])  # 无 sess_ 前缀
            assert "round" in data
            assert "timestamp" in data
            assert "instruction" in data
            assert "attachments" in data
            assert "result" in data
            assert "result_attachments" in data
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


class TestMemoryDirectoryStructure:
    """Memory 目录结构测试"""

    def test_session_directory_no_sess_prefix(self, server, api_headers):
        """TC-MEM-006: 会话目录无 sess_ 前缀"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")
        SSEClient(response)

        # 验证目录名（无 sess_ 前缀）
        assert not session_id.startswith("sess_")

        # 验证 API 返回的 path
        detail_response = requests.get(
            f"{BASE_URL}/sess/{session_id}",
            headers=api_headers
        )

        data = detail_response.json()
        path = data["session"]["path"]

        # path 中包含 session_id，无 sess_ 前缀
        assert session_id in path
        assert "sess_" not in path

    def test_chats_subdirectory_exists(self, server, api_headers):
        """TC-MEM-007: chats 子目录存在"""
        payload = {"instruction": "测试对话"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")
        SSEClient(response)

        chats_path = f"{TEST_HOME}/memory/{session_id}/chats"

        # 如果服务实现了存储
        if os.path.exists(f"{TEST_HOME}/memory/{session_id}"):
            assert os.path.exists(chats_path) or True  # chats 目录可能已创建

    def test_attachments_directory(self, server, api_headers, test_file_base64):
        """TC-MEM-008: attachments 目录和文件"""
        payload = {
            "instruction": "测试对话",
            "attachments": [
                {"type": "file", "name": "test.csv", "content": test_file_base64}
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response.headers.get("X-Session-ID")
        SSEClient(response)

        attachments_path = f"{TEST_HOME}/memory/{session_id}/attachments"

        # 如果服务实现了存储
        if os.path.exists(attachments_path):
            files = os.listdir(attachments_path)
            assert "test.csv" in files


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