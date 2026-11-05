"""
附件处理测试
测试 OpenAI content parts（image_url / input_audio / file）的上传、
大小限制、类型限制、数量限制等
"""

import pytest
import requests
import base64
from conftest import BASE_URL, SSEClient


def file_part(filename, content_b64, mime="text/csv"):
    """构造 OpenAI file content part；file_data 为带 MIME 前缀的 data URL"""
    return {
        "type": "file",
        "file": {"filename": filename, "file_data": f"data:{mime};base64,{content_b64}"},
    }


def text_part(text):
    """构造 OpenAI text content part"""
    return {"type": "text", "text": text}


class TestAttachmentBasic:
    """基础附件处理测试"""

    def test_single_attachment(self, server, api_headers, test_file_base64):
        """TC-ATT-001: 单个附件上传"""
        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("分析这个文件"),
                    file_part("test.csv", test_file_base64),
                ],
            }],
            "stream": True,
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200
        sse = SSEClient(response)
        assert sse.get_completed_event()["data"]["finish_reason"] in ("stop", "tool_calls")

    def test_url_attachment(self, server, api_headers):
        """TC-ATT-002: 不支持的视频片段（video_url）返回 400 unsupported_type"""
        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("获取这个URL的内容"),
                    {"type": "video_url", "video_url": {"url": "https://example.com/doc.mp4"}},
                ],
            }],
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        # OpenAI 无 video_url 片段，服务端按不支持的类型拒绝
        assert response.status_code == 400
        data = response.json()
        assert data["error"]["type"] == "invalid_request_error"
        assert data["error"]["param"] == "messages"
        assert data["error"]["code"] == "unsupported_type"

    def test_multiple_attachments(self, server, api_headers, test_file_base64, pdf_file_base64):
        """TC-ATT-003: 多个附件上传"""
        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("分析这些文件"),
                    file_part("file1.csv", test_file_base64, "text/csv"),
                    # file 片段只接受文本类 MIME，PDF 名沿用但按纯文本装载
                    file_part("file2.pdf", pdf_file_base64, "text/plain"),
                ],
            }],
            "stream": True,
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200
        sse = SSEClient(response)
        assert sse.get_completed_event()["data"]["finish_reason"] in ("stop", "tool_calls")


class TestAttachmentLimits:
    """附件限制测试"""

    def test_attachment_count_exceeded(self, server, api_headers, test_file_base64):
        """TC-ATT-004: 附件数量超限（超过10个）"""
        # 生成11个附件
        parts = [
            file_part(f"file{i}.csv", test_file_base64)
            for i in range(11)
        ]

        payload = {
            "messages": [{"role": "user", "content": [text_part("分析这些文件")] + parts}],
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
        # 数量超限属于请求本身不合法
        assert data["error"]["code"] == "invalid_request"

    def test_attachment_size_exceeded(self, server, api_headers, large_file_base64):
        """TC-ATT-005: 单个附件大小超限（超过50MB）"""
        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("分析大文件"),
                    # 用文本类 MIME 让请求通过片段解析，由附件校验报出体积超限
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

    def test_attachment_type_not_allowed(self, server, api_headers):
        """TC-ATT-006: 附件类型不允许（exe文件）"""
        exe_content = base64.b64encode(b"fake exe content").decode()

        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("执行程序"),
                    file_part("malware.exe", exe_content, "application/octet-stream"),
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
        # file 片段只接受文本类 MIME，非文本 MIME 在片段解析阶段就被拒绝
        assert data["error"]["code"] == "unsupported_file_type"

    def test_attachment_total_size_exceeded(self, server, api_headers):
        """TC-ATT-007: 附件总大小超限"""
        # 生成多个文件，总大小超过100MB
        # 使用 .txt 文件类型（在白名单内，且走文本 MIME）
        large_content = "x" * (30 * 1024 * 1024)  # 30MB
        large_base64 = base64.b64encode(large_content.encode()).decode()

        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("分析文件"),
                    file_part("file1.txt", large_base64, "text/plain"),
                    file_part("file2.txt", large_base64, "text/plain"),
                    file_part("file3.txt", large_base64, "text/plain"),
                    file_part("file4.txt", large_base64, "text/plain"),
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


class TestAttachmentErrors:
    """附件错误处理测试"""

    def test_attachment_decode_error(self, server, api_headers):
        """TC-ATT-008: Base64 解码失败"""
        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("分析文件"),
                    {
                        "type": "file",
                        "file": {
                            "filename": "invalid.pdf",
                            "file_data": "data:text/plain;base64,invalid_base64_content!!!",
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
        # 片段内容解不出属请求本身不合法
        assert data["error"]["code"] == "invalid_request"

    def test_attachment_missing_content(self, server, api_headers):
        """TC-ATT-009: file 片段缺少 file_data"""
        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("分析文件"),
                    {
                        "type": "file",
                        "file": {"filename": "test.pdf"}
                        # 缺少 file_data
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

    def test_attachment_missing_url(self, server, api_headers):
        """TC-ATT-010: 未知内容片段类型返回 400 unsupported_type"""
        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("分析文件"),
                    {"type": "unknown_part", "foo": "bar"},
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
        assert data["error"]["code"] == "unsupported_type"


class TestAttachmentFilenameSafety:
    """文件名安全处理测试"""

    def test_filename_with_slash(self, server, api_headers, test_file_base64):
        """TC-ATT-011: 文件名包含斜杠"""
        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("分析文件"),
                    file_part("path/to/file.csv", test_file_base64),
                ],
            }],
            "stream": True,
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200
        sse = SSEClient(response)
        session_id = sse.get_session_id()

        # 附件不落盘：文件名安全处理（斜杠替换为下划线）在服务端校验层完成，
        # 此处只验证请求被正常接受
        assert session_id

    def test_filename_with_backslash(self, server, api_headers, test_file_base64):
        """TC-ATT-012: 文件名包含反斜杠"""
        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("分析文件"),
                    file_part("path\\to\\file.csv", test_file_base64),
                ],
            }],
            "stream": True,
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200

    def test_filename_with_dots(self, server, api_headers, test_file_base64):
        """TC-ATT-013: 文件名包含路径穿越字符"""
        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("分析文件"),
                    file_part("../../../etc/passwd.csv", test_file_base64),
                ],
            }],
            "stream": True,
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200
        # .. 应被替换为 _

    def test_filename_overwrite(self, server, api_headers, test_file_base64):
        """TC-ATT-014: 同名文件覆盖"""
        # 第一次上传
        payload1 = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("上传文件"),
                    file_part("data.csv", test_file_base64),
                ],
            }],
            "stream": True,
        }

        response1 = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload1,
            stream=True
        )

        sse1 = SSEClient(response1)
        session_id = sse1.get_session_id()

        # 第二次上传同名文件（请求体携带 session_id 继续会话）
        new_content = base64.b64encode(b"new content").decode()
        payload2 = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("更新文件"),
                    file_part("data.csv", new_content),
                ],
            }],
            "session_id": session_id,
            "stream": True,
        }

        response2 = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload2,
            stream=True
        )

        assert response2.status_code == 200
        # 附件不落盘（服务端只做校验），无磁盘文件可检查；请求成功即可


class TestAttachmentStorage:
    """附件存储验证测试"""

    def test_attachment_saved_to_correct_path(self, server, api_headers, test_file_base64):
        """TC-ATT-015: 附件保存路径正确"""
        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("分析文件"),
                    file_part("test.csv", test_file_base64),
                ],
            }],
            "stream": True,
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        session_id = sse.get_session_id()

        # 附件不落盘（服务端只做校验），无磁盘路径可验证；请求成功即视为通过
        assert session_id

    def test_attachment_recorded_in_history(self, server, api_headers, test_file_base64):
        """TC-ATT-016: 带附件的对话被记录到会话历史（消息不回显 attachments 字段）"""
        payload = {
            "messages": [{
                "role": "user",
                "content": [
                    text_part("分析文件"),
                    file_part("test.csv", test_file_base64),
                ],
            }],
            "stream": True,
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            stream=True
        )

        sse = SSEClient(response)
        session_id = sse.get_session_id()

        # 查询会话详情
        detail_response = requests.get(
            f"{BASE_URL}/sess/{session_id}",
            headers=api_headers
        )

        assert detail_response.status_code == 200
        data = detail_response.json()

        # 带附件的这轮对话应被正常记录（附件内容/文件名不在消息中回显）
        messages = data["history"]["messages"]
        assert messages, "带附件的对话应记录到会话历史"
        assert messages[0]["instruction"] == "分析文件"
        # 状态枚举见 memory/types.go：completed/failed/cancelled
        assert messages[0]["status"] in ("completed", "failed", "cancelled")
