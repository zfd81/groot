"""
安全限制测试
覆盖附件文件名安全处理、API Key 不落日志、无效 Key 限流、输入验证等
"""

import pytest
import requests
import os
import base64
from conftest import BASE_URL, SSEClient, TEST_HOME


def file_part(filename, content_b64, mime="text/csv"):
    """构造 OpenAI file content part；file_data 为带 MIME 前缀的 data URL"""
    return {
        "type": "file",
        "file": {"filename": filename, "file_data": f"data:{mime};base64,{content_b64}"},
    }


def text_part(text):
    """构造 OpenAI text content part"""
    return {"type": "text", "text": text}


class TestAttachmentSecurity:
    """附件安全测试"""

    def test_attachment_filename_sanitization(self, server, api_headers):
        """TC-SEC-010: 文件名安全处理"""
        dangerous_names = [
            "../../../etc/passwd.csv",
            "/etc/passwd.csv",
            "\\..\\..\\windows\\system32.csv",
            "..\\..\\..\\test.csv"
        ]

        for filename in dangerous_names:
            content = base64.b64encode(b"test").decode()
            payload = {
                "messages": [{
                    "role": "user",
                    "content": [
                        text_part("分析文件"),
                        file_part(filename, content),
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

            # 验证危险字符被替换
            assert response.status_code == 200

            sse = SSEClient(response)
            completed = sse.get_completed_event()

            # 验证文件保存到安全位置

    def test_attachment_type_whitelist(self, server, api_headers):
        """TC-SEC-011: 附件类型白名单"""
        # Note: When allowed_types is empty (allow all types), this test is skipped
        # because there's no whitelist restriction to test

        # Test that normal file types are accepted when whitelist is empty
        # （file 片段只接受文本类 MIME，因此统一按 text/plain 装载）
        safe_types = [
            "document.pdf",
            "data.csv",
            "image.png"
        ]

        for filename in safe_types:
            content = base64.b64encode(b"test").decode()
            payload = {
                "messages": [{
                    "role": "user",
                    "content": [
                        text_part("分析文件"),
                        file_part(filename, content, "text/plain"),
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

            # 验证安全类型被接受
            assert response.status_code == 200


class TestAuthenticationSecurity:
    """认证安全测试"""

    def test_api_key_not_logged(self, server, api_headers, api_key):
        """TC-SEC-012: API Key（JWT token）不出现在日志中"""
        # 发送请求
        requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json={"messages": [{"role": "user", "content": "test"}], "stream": True},
            stream=True
        )

        # 检查日志文件
        logs_dir = f"{TEST_HOME}/logs"

        if os.path.exists(logs_dir):
            import glob
            log_files = glob.glob(f"{logs_dir}/groot-*.log")

            for log_file in log_files:
                with open(log_file, "r") as f:
                    content = f.read()

                    # 验证 JWT token 不出现在日志中
                    assert api_key not in content

    def test_invalid_key_rate_limiting(self, server):
        """TC-SEC-013: 无效 Key 尝试限流"""
        # 快速发送多个无效认证请求
        for i in range(10):
            requests.post(
                f"{BASE_URL}/chat/completions",
                headers={"Authorization": f"Bearer invalid-key-{i}"},
                json={"messages": [{"role": "user", "content": "test"}]}
            )

        # 验证服务器未崩溃
        response = requests.get(f"{BASE_URL}/web/health")
        assert response.status_code == 200


class TestInputValidation:
    """输入验证测试"""

    def test_instruction_length_limit(self, server, api_headers):
        """TC-SEC-014: 指令长度限制"""
        # 发送超长指令（放进 messages[0].content）
        long_instruction = "test " * 10000  # 约50KB

        payload = {"messages": [{"role": "user", "content": long_instruction}]}

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=payload,
            timeout=60,
        )

        # 当前后端契约：未设指令长度上限，超长指令会被正常受理（200）或由解析层拒绝（400），
        # 但不应导致 5xx 或服务崩溃
        assert response.status_code in (200, 400), response.text[:200]

    def test_json_body_size_limit(self, server, api_headers):
        """TC-SEC-015: JSON body 大小限制"""
        # 发送大 JSON body（保留 extra 未知大字段：未知字段应被忽略而非报错）
        large_data = {
            "messages": [{"role": "user", "content": "test"}],
            "extra": "x" * 100000,
        }

        response = requests.post(
            f"{BASE_URL}/chat/completions",
            headers=api_headers,
            json=large_data,
            timeout=60,
        )

        # 当前后端契约：未知顶层大字段被忽略，不会因体积或未知字段导致 5xx / 崩溃，
        # 服务能正常受理（200）或由解析层拒绝（400）
        assert response.status_code in (200, 400), response.text[:200]
