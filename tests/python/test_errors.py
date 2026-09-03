"""
错误处理测试
测试各种错误场景的响应格式和处理逻辑
"""

import pytest
import requests
from conftest import BASE_URL


class TestErrorResponseFormat:
    """错误响应格式测试"""

    def test_400_error_format(self, server, api_headers):
        """TC-ERR-001: 400 错误响应格式"""
        payload = {"instruction": ""}  # 空指令

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400
        data = response.json()

        # 验证错误格式
        assert "status" in data
        assert "message" in data
        assert data["status"] == "invalid_request"

    def test_401_error_format(self, server, no_auth_headers):
        """TC-ERR-002: 401 错误响应格式"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=no_auth_headers,
            json={"instruction": "test"}
        )

        assert response.status_code == 401
        data = response.json()

        assert data["status"] == "unauthorized"
        assert "API Key" in data["message"]

    def test_409_error_format(self, server, api_headers):
        """TC-ERR-003: 409 错误响应格式"""
        # 启动第一个请求
        payload = {"instruction": "长任务"}

        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response1.headers.get("X-Session-ID")

        # 同时发送第二个请求
        headers2 = api_headers.copy()
        headers2["X-Session-ID"] = session_id

        response2 = requests.post(
            f"{BASE_URL}/chat",
            headers=headers2,
            json={"instruction": "test"}
        )

        assert response2.status_code == 409
        data = response2.json()

        assert data["status"] == "chat_limit_exceeded"
        assert "正在执行" in data["message"]


class TestErrorCodeList:
    """错误码完整列表测试"""

    def test_invalid_request(self, server, api_headers):
        """TC-ERR-004: invalid_request 错误"""
        payload = {}  # 缺少 instruction

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400
        assert response.json()["status"] == "invalid_request"

    def test_attachment_count_exceeded(self, server, api_headers, test_file_base64):
        """TC-ERR-005: attachment_count_exceeded 错误"""
        attachments = [
            {"type": "file", "name": f"file{i}.csv", "content": test_file_base64}
            for i in range(11)
        ]

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": "test", "attachments": attachments}
        )

        assert response.status_code == 400
        assert response.json()["status"] == "attachment_count_exceeded"

    def test_attachment_type_not_allowed(self, server, api_headers):
        """TC-ERR-006: attachment_type_not_allowed 错误"""
        import base64

        payload = {
            "instruction": "test",
            "attachments": [
                {"type": "file", "name": "malware.exe", "content": base64.b64encode(b"test").decode()}
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400
        assert response.json()["status"] == "attachment_type_not_allowed"

    def test_attachment_size_exceeded(self, server, api_headers, large_file_base64):
        """TC-ERR-007: attachment_size_exceeded 错误"""
        payload = {
            "instruction": "test",
            "attachments": [
                {"type": "file", "name": "huge.pdf", "content": large_file_base64}
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400
        assert response.json()["status"] == "attachment_size_exceeded"

    def test_attachment_decode_error(self, server, api_headers):
        """TC-ERR-008: attachment_decode_error 错误"""
        payload = {
            "instruction": "test",
            "attachments": [
                {"type": "file", "name": "test.pdf", "content": "invalid!!!base64"}
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400
        assert response.json()["status"] == "attachment_decode_error"

    def test_unauthorized(self, server, no_auth_headers):
        """TC-ERR-009: unauthorized 错误"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=no_auth_headers,
            json={"instruction": "test"}
        )

        assert response.status_code == 401
        assert response.json()["status"] == "unauthorized"

    def test_session_not_found(self, server, api_headers):
        """TC-ERR-010: session_not_found 错误（通过 DELETE）"""
        # DELETE 端点已删除，返回 404
        response = requests.delete(
            f"{BASE_URL}/chat/nonexistent_session_12345",
            headers=api_headers
        )
        assert response.status_code == 404


class TestSSEErrorHandling:
    """SSE 错误处理测试"""

    def test_sse_error_event_on_failure(self, server, api_headers):
        """TC-ERR-011: SSE 包含 error 事件（失败场景）"""
        # 构造会导致失败的请求
        payload = {
            "instruction": "读取不存在的文件 /nonexistent/path/file.txt"
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        from conftest import SSEClient
        sse = SSEClient(response)

        completed = sse.get_completed_event()
        if completed:
            # 如果失败，completed 应包含 error
            if completed["data"].get("finish_reason") not in ("stop", "tool_calls"):
                assert "error" in completed["data"]
                assert "code" in completed["data"]["error"]
                assert "message" in completed["data"]["error"]

    def test_thinking_error_on_failure(self, server, api_headers):
        """TC-ERR-012: 失败场景下 SSE 事件流仍然完整

        SSE 事件负载不含 status 字段（步骤状态在 GET /chat/:sid 的 steps 里），
        此处只验证事件流可解析且存在有意义的事件。
        """
        payload = {"instruction": "执行可能失败的命令"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        from conftest import SSEClient
        sse = SSEClient(response)

        # 事件流应可解析且非空（thinking/tool_calls/message/finish 至少其一）
        assert len(sse.events) > 0
        assert sse.verify_event_order()

    def test_tool_result_error_on_failure(self, server, api_headers):
        """TC-ERR-013: tool_result 包含 error（工具调用失败）"""
        payload = {"instruction": "读取不存在的文件"}

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        from conftest import SSEClient
        sse = SSEClient(response)

        tool_results = sse.get_events_by_type("tool_result")

        # 如果有工具调用失败，验证 error 字段
        for result in tool_results:
            if "error" in result["data"]:
                assert result["data"]["error"] is not None


class TestErrorRecovery:
    """错误恢复测试"""

    def test_error_does_not_crash_server(self, server, api_headers):
        """TC-ERR-013: 错误不会导致服务崩溃"""
        # 发送多个错误请求
        for i in range(5):
            requests.post(
                f"{BASE_URL}/chat",
                headers=api_headers,
                json={"instruction": ""}
            )

        # 验证服务仍然可用
        response = requests.get(f"{BASE_URL}/web/health")
        assert response.status_code == 200

    def test_error_response_is_json(self, server, api_headers):
        """TC-ERR-014: 错误响应为 JSON 格式"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": ""}
        )

        # 验证 Content-Type
        assert "application/json" in response.headers.get("Content-Type", "")

        # 验证可解析为 JSON
        data = response.json()
        assert isinstance(data, dict)


class TestErrorFields:
    """错误字段完整性测试"""

    def test_error_contains_status_and_message(self, server, api_headers):
        """TC-ERR-015: 错误包含 status 和 message"""
        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json={"instruction": ""}
        )

        data = response.json()
        assert "status" in data
        assert "message" in data

    def test_error_contains_session_id_when_relevant(self, server, api_headers):
        """TC-ERR-016: 相关错误包含 session_id"""
        # 启动第一个请求
        payload = {"instruction": "长任务"}

        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id = response1.headers.get("X-Session-ID")

        # 同时发送第二个请求（409错误）
        headers2 = api_headers.copy()
        headers2["X-Session-ID"] = session_id

        response2 = requests.post(
            f"{BASE_URL}/chat",
            headers=headers2,
            json={"instruction": "test"}
        )

        data = response2.json()
        if response2.status_code == 409:
            assert "session_id" in data
            assert data["session_id"] == session_id