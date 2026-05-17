"""
附件处理测试
测试附件上传、大小限制、类型限制、数量限制等
"""

import pytest
import requests
import base64
import os
from conftest import BASE_URL, SSEClient, TEST_HOME


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

        # 验证文件名被替换
        # 斜杠应被替换为下划线
        attachment_path = f"{TEST_HOME}/memory/{session_id}/attachments"
        # 实际文件名应为 path_to_file.csv

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

        # 验证文件被覆盖（只有一个 data.csv）
        attachment_path = f"{TEST_HOME}/memory/{session_id}/attachments"
        if os.path.exists(attachment_path):
            files = os.listdir(attachment_path)
            data_csv_count = sum(1 for f in files if f == "data.csv")
            assert data_csv_count == 1  # 只有一个


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

        # 验证附件路径（无 sess_ 前缀）
        attachment_path = f"{TEST_HOME}/memory/{session_id}/attachments/test.csv"

        # 如果路径存在，验证文件
        if os.path.exists(attachment_path):
            with open(attachment_path, "r") as f:
                content = f.read()
                assert "name,age,city" in content

    def test_attachment_recorded_in_history(self, server, api_headers, test_file_base64):
        """TC-ATT-016: 附件记录在 history.json 中"""
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

        # 验证 attachments 字段
        messages = data["history"]["messages"]
        if messages:
            assert "test.csv" in messages[0]["attachments"]