"""
错误处理测试
测试各种错误场景的 OpenAI 错误信封格式和处理逻辑
"""

import base64
import json

import pytest
import requests
from conftest import BASE_URL, SSEClient


def read_first_session_id(response):
    """从流式响应读到首个带 session_id 的 chunk 即停止，返回 ``(session_id, iterator)``。

    流式响应不再携带会话标识响应头；只读到取到就停，以便对话仍在执行时发起
    冲突请求。``iterator`` 必须交给 :func:`drain` 继续消费，直接丢弃会让
    requests 关闭连接、对话被中断。
    """
    it = response.iter_lines()
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
            session_id = json.loads(body).get("session_id")
        except json.JSONDecodeError:
            continue
        if session_id:
            return session_id, it
    return None, it


def drain(it):
    """消费迭代器里剩余的 SSE 行，等待对话执行完毕"""
    for _ in it:
        pass


def file_part(filename, content_b64, mime="text/csv"):
    """构造 OpenAI file content part；file_data 为带 MIME 前缀的 data URL"""
    return {
        "type": "file",
        "file": {"filename": filename, "file_data": f"data:{mime};base64,{content_b64}"},
    }


def text_part(text):
    """构造 OpenAI text content part"""
    return {"type": "text", "text": text}


class TestErrorResponseFormat:
    """错误响应格式测试"""

    def test_400_error_format(self, server, api_headers):
        """TC-ERR-001: 400 错误响应格式"""
        payload = {"messages": []}  # 空 messages

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400
        data = response.json()

        # 验证 OpenAI 错误信封
        error = data["error"]
        assert error["type"] == "invalid_request_error"
        assert error["code"] == "invalid_request"
        assert error["param"] == "messages"
        assert isinstance(error["message"], str)

    def test_401_error_format(self, server, no_auth_headers):
        """TC-ERR-002: 401 错误响应格式"""
        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=no_auth_headers,
            json={"messages": [{"role": "user", "content": "test"}]}
        )

        assert response.status_code == 401
        data = response.json()

        assert data["error"]["type"] == "authentication_error"
        assert data["error"]["code"] == "invalid_api_key"

    def test_409_error_format(self, server, api_headers):
        """TC-ERR-003: 409 错误响应格式"""
        # 启动第一个请求
        payload = {
            "messages": [{"role": "user", "content": "帮我写一个Python快速排序函数"}],
            "stream": True,
        }

        response1 = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id, it1 = read_first_session_id(response1)
        assert session_id, "首个流式响应应带 session_id"

        # 同时发送第二个请求（请求体携带相同的 session_id）
        response2 = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json={
                "messages": [{"role": "user", "content": "test"}],
                "session_id": session_id,
            }
        )

        assert response2.status_code == 409
        data = response2.json()

        assert data["error"]["type"] == "invalid_request_error"
        assert data["error"]["code"] == "chat_limit_exceeded"

        drain(it1)


class TestErrorCodeList:
    """错误码完整列表测试"""

    def test_invalid_request(self, server, api_headers):
        """TC-ERR-004: invalid_request 错误"""
        payload = {}  # 缺少 messages

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400
        assert response.json()["error"]["code"] == "invalid_request"

    def test_attachment_count_exceeded(self, server, api_headers, test_file_base64):
        """TC-ERR-005: 附件数量超限（对外映射为 invalid_request）"""
        parts = [
            file_part(f"file{i}.csv", test_file_base64)
            for i in range(11)
        ]

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json={"messages": [{"role": "user", "content": [text_part("test")] + parts}]}
        )

        assert response.status_code == 400
        data = response.json()
        assert data["error"]["type"] == "invalid_request_error"
        assert data["error"]["param"] == "messages"
        assert data["error"]["code"] == "invalid_request"

    def test_attachment_type_not_allowed(self, server, api_headers):
        """TC-ERR-006: 非文本 MIME 的 file 片段（对外映射为 unsupported_file_type）"""
        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("test"),
                    file_part(
                        "malware.exe",
                        base64.b64encode(b"test").decode(),
                        "application/octet-stream",
                    ),
                ],
            }],
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400
        data = response.json()
        assert data["error"]["type"] == "invalid_request_error"
        assert data["error"]["param"] == "messages"
        assert data["error"]["code"] == "unsupported_file_type"

    def test_attachment_size_exceeded(self, server, api_headers, large_file_base64):
        """TC-ERR-007: 附件大小超限（对外映射为 file_too_large）"""
        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("test"),
                    file_part("huge.pdf", large_file_base64, "text/plain"),
                ],
            }],
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 413
        data = response.json()
        assert data["error"]["type"] == "invalid_request_error"
        assert data["error"]["param"] == "messages"
        assert data["error"]["code"] == "file_too_large"

    def test_attachment_decode_error(self, server, api_headers):
        """TC-ERR-008: Base64 解码失败（对外映射为 invalid_request）"""
        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("test"),
                    {
                        "type": "file",
                        "file": {
                            "filename": "test.pdf",
                            "file_data": "data:text/plain;base64,invalid!!!base64",
                        },
                    },
                ],
            }],
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400
        data = response.json()
        assert data["error"]["type"] == "invalid_request_error"
        assert data["error"]["param"] == "messages"
        assert data["error"]["code"] == "invalid_request"

    def test_unauthorized(self, server, no_auth_headers):
        """TC-ERR-009: unauthorized 错误"""
        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=no_auth_headers,
            json={"messages": [{"role": "user", "content": "test"}]}
        )

        assert response.status_code == 401
        assert response.json()["error"]["code"] == "invalid_api_key"

    def test_session_not_found(self, server, api_headers):
        """TC-ERR-010: session_not_found 错误（GET /chat/{sid}）"""
        response = requests.get(
            f"{BASE_URL}/chat/nonexistent_session_12345",
            headers=api_headers
        )
        assert response.status_code == 404
        assert response.json()["error"]["code"] == "session_not_found"


class TestSSEErrorHandling:
    """SSE 错误处理测试"""

    def test_sse_error_event_on_failure(self, server, api_headers):
        """TC-ERR-011: SSE 包含 error 事件（失败场景）"""
        # 构造会导致失败的请求
        payload = {
            "messages": [{"role": "user", "content": "读取不存在的文件 /nonexistent/path/file.txt"}],
            "stream": True,
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        completed = sse.get_completed_event()
        # 若有致命错误，流里下发的是完整错误信封（flatten_chunk 已摊平为
        # {"event":"error","message":...,"error":{...}}）
        errors = sse.get_events_by_type("error")

        # 流必须以 finish 或 error 收尾，否则说明流被静默截断
        assert completed is not None or errors, (
            "流中既无 finish 事件也无 error 事件：\n" + response.text[:2000]
        )

        if completed:
            # finish 事件只承载 finish_reason（原样透传上游取值），不携带 error 对象；
            # 致命错误以独立的 error 事件 + [DONE] 下发
            assert "finish_reason" in completed["data"]

        for event in errors:
            err = event["data"]["error"]
            assert isinstance(err["message"], str)
            assert "type" in err
            assert "param" in err
            assert "code" in err

    def test_thinking_error_on_failure(self, server, api_headers):
        """TC-ERR-012: 失败场景下 SSE 事件流仍然完整

        SSE 事件负载不含 status 字段（步骤状态在 GET /chat/:sid 的 steps 里），
        此处只验证事件流可解析且存在有意义的事件。
        """
        payload = {
            "messages": [{"role": "user", "content": "执行可能失败的命令"}],
            "stream": True,
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)

        # 事件流应可解析且非空（thinking/tool_calls/message/finish 至少其一）
        assert len(sse.events) > 0
        assert sse.verify_event_order()

    def test_tool_result_error_on_failure(self, server, api_headers):
        """TC-ERR-013: tool_result 包含 error（工具调用失败）"""
        payload = {
            "messages": [{"role": "user", "content": "读取不存在的文件"}],
            "stream": True,
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

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
                f"{BASE_URL}/chat/completions",
                headers=api_headers,
                json={"messages": []}
            )

        # 验证服务仍然可用
        response = requests.get(f"{BASE_URL}/web/health")
        assert response.status_code == 200

    def test_error_response_is_json(self, server, api_headers):
        """TC-ERR-014: 错误响应为 JSON 格式"""
        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json={"messages": []}
        )

        # 验证 Content-Type
        assert "application/json" in response.headers.get("Content-Type", "")

        # 验证可解析为 JSON
        data = response.json()
        assert isinstance(data, dict)


class TestErrorFields:
    """错误字段完整性测试"""

    def test_error_contains_status_and_message(self, server, api_headers):
        """TC-ERR-015: 错误信封四字段齐全"""
        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json={"messages": []}
        )

        data = response.json()
        error = data["error"]
        # param / code 允许为 None，但键必须存在
        assert "message" in error
        assert "type" in error
        assert "param" in error
        assert "code" in error

    def test_conflict_error_envelope(self, server, api_headers):
        """TC-ERR-016: 409 冲突响应也是完整错误信封"""
        # 启动第一个请求
        payload = {
            "messages": [{"role": "user", "content": "帮我写一个Python快速排序函数"}],
            "stream": True,
        }

        response1 = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        session_id, it1 = read_first_session_id(response1)
        assert session_id, "首个流式响应应带 session_id"

        # 同时发送第二个请求（409错误）
        response2 = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json={
                "messages": [{"role": "user", "content": "test"}],
                "session_id": session_id,
            }
        )

        drain(it1)

        assert response2.status_code == 409
        data = response2.json()
        assert data["error"]["type"] == "invalid_request_error"
        assert data["error"]["code"] == "chat_limit_exceeded"
        assert data["error"]["param"] is None or isinstance(data["error"]["param"], str)
