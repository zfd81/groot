"""
GrootClient 测试。

Usage:
    cd tests/examples/python
    python3 -m unittest test_groot_client -v
"""

import json
import os
import sys
import threading
import time
import unittest
from http.server import HTTPServer, BaseHTTPRequestHandler

# 将 examples/python/ 加入搜索路径
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "../../../examples/python"))

from groot_client import GrootClient


# ---- Mock Groot Server ----

class MockGrootHandler(BaseHTTPRequestHandler):
    """模拟 Groot API 服务，用于测试客户端。"""

    def do_POST(self):
        if self.path == "/chat":
            self._handle_chat()
        else:
            self.send_error(404)

    def do_GET(self):
        if self.path == "/health":
            self._json_response(200, {"status": "healthy", "version": "1.0.0"})
        elif self.path == "/skills":
            self._json_response(200, {"skills": [{"name": "test_skill"}], "total": 1})
        elif self.path == "/tools":
            self._json_response(200, {"test_mcp": {"tools": [{"name": "echo"}], "total": 1}})
        elif self.path.startswith("/chat/status/"):
            self._json_response(200, {"status": "success", "chat": None})
        elif self.path.startswith("/chat/") and self.path.count("/") >= 3:
            # GET /chat/{sid}/{cid}  →  /chat/sid/cid = ["", "chat", "sid", "cid"]
            parts = self.path.split("/")
            self._json_response(200, {
                "status": "success",
                "chat": {"chat_id": parts[3], "status": "completed"}
            })
        elif self.path.startswith("/sess/history"):
            self._json_response(200, {"status": "success", "total": 0, "sessions": []})
        elif self.path.startswith("/sess/"):
            self._json_response(200, {"status": "success", "session": {}, "history": {"messages": []}})
        else:
            self.send_error(404)

    def do_DELETE(self):
        if self.path.startswith("/chat/"):
            self._json_response(200, {
                "status": "success",
                "session_id": self.path.split("/")[-1],
                "message": "对话已取消"
            })
        else:
            self.send_error(404)

    def _handle_chat(self):
        content_length = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(content_length)) if content_length > 0 else {}

        sid = self.headers.get("X-Session-ID", f"test_sid_{int(time.time())}")
        cid = f"chat_test_{int(time.time())}"

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream; charset=utf-8")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.send_header("X-Session-ID", sid)
        self.send_header("X-Chat-ID", cid)
        self.end_headers()

        events = [
            'data: {"role":"assistant","reasoning_content":"让我思考一下..."}\n\n',
            'data: {"role":"assistant","content":"分析结果如下：' + body.get("instruction", "")[:20] + '"}\n\n',
            'data: {"role":"assistant","finish_reason":"stop"}\n\n',
            'data: {"status":"success","result":"分析完成"}\n\n',
            'data: [DONE]\n\n',
        ]
        payload = "".join(events)
        self.wfile.write(payload.encode())
        self.wfile.flush()

    def _json_response(self, status, data):
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.end_headers()
        self.wfile.write(json.dumps(data).encode())

    def log_message(self, format, *args):
        pass  # 抑制日志输出


# ---- 模拟服务管理 ----

_server = None
_server_thread = None
_server_url = None


def setUpModule():
    """模块级别 setup：启动模拟服务器。"""
    global _server, _server_thread, _server_url
    _server = HTTPServer(("127.0.0.1", 0), MockGrootHandler)
    _server_url = f"http://127.0.0.1:{_server.server_address[1]}"
    _server_thread = threading.Thread(target=_server.serve_forever, daemon=True)
    _server_thread.start()
    time.sleep(0.05)


def tearDownModule():
    """模块级别 teardown：关闭模拟服务器。"""
    global _server
    if _server:
        _server.shutdown()


# ---- 测试用例 ----


class TestGrootClient(unittest.TestCase):
    """GrootClient 完整测试"""

    @classmethod
    def setUpClass(cls):
        cls.client = GrootClient(_server_url, api_key="test-key")

    def test_health_check(self):
        resp = self.client.health_check()
        self.assertEqual(resp["status"], "healthy")
        self.assertEqual(resp["version"], "1.0.0")

    def test_list_skills(self):
        resp = self.client.list_skills()
        self.assertEqual(resp["total"], 1)
        self.assertEqual(resp["skills"][0]["name"], "test_skill")

    def test_list_tools(self):
        resp = self.client.list_tools()
        self.assertIn("test_mcp", resp)
        self.assertEqual(resp["test_mcp"]["tools"][0]["name"], "echo")

    def test_execute_chat_new_session(self):
        events = []

        def collect(event_type, data):
            events.append((event_type, data))

        result = self.client.execute_chat("测试指令", callback=collect)

        self.assertIsNotNone(result["session_id"])
        self.assertIsNotNone(result["chat_id"])
        self.assertEqual(result["status"], "success")
        self.assertEqual(result["result"], "分析完成")
        event_types = [e[0] for e in events]
        self.assertIn("message", event_types)

    def test_execute_chat_with_session(self):
        result = self.client.execute_chat("继续分析", session_id="existing_sid")
        self.assertEqual(result["session_id"], "existing_sid")

    def test_execute_chat_with_prompt(self):
        result = self.client.execute_chat("指令", prompt="你是专家")
        self.assertIsNotNone(result["session_id"])

    def test_cancel_chat(self):
        resp = self.client.cancel_chat("test_sid")
        self.assertEqual(resp["status"], "success")

    def test_get_chat_status(self):
        resp = self.client.get_chat_status("test_sid")
        self.assertEqual(resp["status"], "success")

    def test_get_chat_detail(self):
        resp = self.client.get_chat_detail("test_sid", "test_cid")
        self.assertEqual(resp["status"], "success")
        self.assertEqual(resp["chat"]["chat_id"], "test_cid")

    def test_get_session_detail(self):
        resp = self.client.get_session_detail("test_sid")
        self.assertEqual(resp["status"], "success")

    def test_list_sessions(self):
        resp = self.client.list_sessions(limit=10)
        self.assertEqual(resp["status"], "success")

    def test_list_sessions_pagination(self):
        resp = self.client.list_sessions(limit=5, offset=10)
        self.assertEqual(resp["status"], "success")

    def test_execute_chat_no_callback(self):
        result = self.client.execute_chat("简单指令")
        self.assertIsNotNone(result["session_id"])

    def test_execute_chat_with_attachments(self):
        result = self.client.execute_chat(
            "分析文件",
            attachments=[{"type": "file", "name": "test.pdf", "content": "base64content"}],
        )
        self.assertIsNotNone(result["session_id"])

    def test_execute_chat_with_model(self):
        result = self.client.execute_chat("指令", model_name="gpt-4o")
        self.assertIsNotNone(result["session_id"])
