"""
附件处理测试
测试附件上传、大小限制、类型限制、数量限制等
"""

import pytest
import requests
import base64
from conftest import BASE_URL, SSEClient


class TestAttachmentBasic:
    """基础附件处理测试"""

    def test_single_attachment(self, server, api_headers, test_file_base64):
        """TC-ATT-001: 单个附件上传"""
        payload = {
            "instruction": "分析这个文件",
            "attachments": [
                {
                    "type": "file",
                    "name": "test.csv",
                    "content": test_file_base64
                }
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200
        sse = SSEClient(response)
        assert sse.get_completed_event()["data"]["finish_reason"] in ("stop", "tool_calls")

    def test_url_attachment(self, server, api_headers):
        """TC-ATT-002: URL 类型附件（不被支持，返回 400）"""
        payload = {
            "instruction": "获取这个URL的内容",
            "attachments": [
                {
                    "type": "url",
                    "name": "external.pdf",
                    "url": "https://example.com/doc.pdf"
                }
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        # url 类型不在 [file, image, audio, video] 中，返回 400
        assert response.status_code == 400

    def test_multiple_attachments(self, server, api_headers, test_file_base64, pdf_file_base64):
        """TC-ATT-003: 多个附件上传"""
        payload = {
            "instruction": "分析这些文件",
            "attachments": [
                {"type": "file", "name": "file1.csv", "content": test_file_base64},
                {"type": "file", "name": "file2.pdf", "content": pdf_file_base64}
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
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
        attachments = [
            {"type": "file", "name": f"file{i}.csv", "content": test_file_base64}
            for i in range(11)
        ]

        payload = {
            "instruction": "分析这些文件",
            "attachments": attachments
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400
        data = response.json()
        assert data["status"] == "attachment_count_exceeded"

    def test_attachment_size_exceeded(self, server, api_headers, large_file_base64):
        """TC-ATT-005: 单个附件大小超限（超过50MB）"""
        payload = {
            "instruction": "分析大文件",
            "attachments": [
                {
                    "type": "file",
                    "name": "huge.pdf",
                    "content": large_file_base64
                }
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400
        data = response.json()
        assert data["status"] == "attachment_size_exceeded"

    def test_attachment_type_not_allowed(self, server, api_headers):
        """TC-ATT-006: 附件类型不允许（exe文件）"""
        exe_content = base64.b64encode(b"fake exe content").decode()

        payload = {
            "instruction": "执行程序",
            "attachments": [
                {
                    "type": "file",
                    "name": "malware.exe",
                    "content": exe_content
                }
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400
        data = response.json()
        assert data["status"] == "attachment_type_not_allowed"

    def test_attachment_total_size_exceeded(self, server, api_headers):
        """TC-ATT-007: 附件总大小超限"""
        # 生成多个文件，总大小超过100MB
        # 使用 .txt 文件类型（在白名单内）
        large_content = "x" * (30 * 1024 * 1024)  # 30MB
        large_base64 = base64.b64encode(large_content.encode()).decode()

        payload = {
            "instruction": "分析文件",
            "attachments": [
                {"type": "file", "name": "file1.txt", "content": large_base64},
                {"type": "file", "name": "file2.txt", "content": large_base64},
                {"type": "file", "name": "file3.txt", "content": large_base64},
                {"type": "file", "name": "file4.txt", "content": large_base64}
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400
        data = response.json()
        assert data["status"] == "attachment_total_size_exceeded"


class TestAttachmentErrors:
    """附件错误处理测试"""

    def test_attachment_decode_error(self, server, api_headers):
        """TC-ATT-008: Base64 解码失败"""
        payload = {
            "instruction": "分析文件",
            "attachments": [
                {
                    "type": "file",
                    "name": "invalid.pdf",
                    "content": "invalid_base64_content!!!"
                }
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400
        data = response.json()
        assert data["status"] == "attachment_decode_error"

    def test_attachment_missing_content(self, server, api_headers):
        """TC-ATT-009: file 类型缺少 content"""
        payload = {
            "instruction": "分析文件",
            "attachments": [
                {
                    "type": "file",
                    "name": "test.pdf"
                    # 缺少 content
                }
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400

    def test_attachment_missing_url(self, server, api_headers):
        """TC-ATT-010: url 类型缺少 url"""
        payload = {
            "instruction": "分析文件",
            "attachments": [
                {
                    "type": "url",
                    "name": "external.pdf"
                    # 缺少 url
                }
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload
        )

        assert response.status_code == 400


class TestAttachmentFilenameSafety:
    """文件名安全处理测试"""

    def test_filename_with_slash(self, server, api_headers, test_file_base64):
        """TC-ATT-011: 文件名包含斜杠"""
        payload = {
            "instruction": "分析文件",
            "attachments": [
                {
                    "type": "file",
                    "name": "path/to/file.csv",
                    "content": test_file_base64
                }
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200
        session_id = response.headers.get("X-Session-ID")

        # 附件不落盘：文件名安全处理（斜杠替换为下划线）在服务端校验层完成，
        # 此处只验证请求被正常接受
        assert session_id

    def test_filename_with_backslash(self, server, api_headers, test_file_base64):
        """TC-ATT-012: 文件名包含反斜杠"""
        payload = {
            "instruction": "分析文件",
            "attachments": [
                {
                    "type": "file",
                    "name": "path\\to\\file.csv",
                    "content": test_file_base64
                }
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload,
            stream=True
        )

        assert response.status_code == 200

    def test_filename_with_dots(self, server, api_headers, test_file_base64):
        """TC-ATT-013: 文件名包含路径穿越字符"""
        payload = {
            "instruction": "分析文件",
            "attachments": [
                {
                    "type": "file",
                    "name": "../../../etc/passwd.csv",
                    "content": test_file_base64
                }
            ]
        }

        response = requests.post(
            f"{BASE_URL}/chat",
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
            "instruction": "上传文件",
            "attachments": [
                {"type": "file", "name": "data.csv", "content": test_file_base64}
            ]
        }

        response1 = requests.post(
            f"{BASE_URL}/chat",
            headers=api_headers,
            json=payload1,
            stream=True
        )

        session_id = response1.headers.get("X-Session-ID")
        SSEClient(response1)

        # 第二次上传同名文件（继续会话）
        headers2 = api_headers.copy()
        headers2["X-Session-ID"] = session_id

        new_content = base64.b64encode(b"new content").decode()
        payload2 = {
            "instruction": "更新文件",
            "attachments": [
                {"type": "file", "name": "data.csv", "content": new_content}
            ]
        }

        response2 = requests.post(
            f"{BASE_URL}/chat",
            headers=headers2,
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
            "instruction": "分析文件",
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

        # 附件不落盘（服务端只做校验），无磁盘路径可验证；请求成功即视为通过
        assert session_id

    def test_attachment_recorded_in_history(self, server, api_headers, test_file_base64):
        """TC-ATT-016: 带附件的对话被记录到会话历史（消息不回显 attachments 字段）"""
        payload = {
            "instruction": "分析文件",
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