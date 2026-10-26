"""
GrootClient 测试。

Usage:
    cd tests/examples/python
    python3 -m unittest test_groot_client -v
"""

import json
import os
import sys
import tempfile
import threading
import time
import unittest
from http.server import HTTPServer, BaseHTTPRequestHandler

# 将 examples/python/ 加入搜索路径
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "../../../examples/python"))

from groot_client import GrootClient


# ---- 测试辅助 ----

def _chunk(delta: dict, finish_reason=None) -> dict:
    """构造一条 OpenAI chat.completion.chunk 对象。"""
    return {
        "id": "chatcmpl-test",
        "object": "chat.completion.chunk",
        "created": 1,
        "model": "test-model",
        "choices": [{"index": 0, "delta": delta, "finish_reason": finish_reason}],
    }


# ---- Mock Groot Server ----

class MockGrootHandler(BaseHTTPRequestHandler):
    """模拟 Groot API 服务，用于测试客户端。"""

    def do_POST(self):
        if self.path == "/chat":
            self._handle_chat()
        elif self.path.startswith("/chat/") and self.path.endswith("/stop"):
            # POST /chat/{sid}/{cid}/stop  →  ["", "chat", sid, cid, "stop"]
            parts = self.path.split("/")
            self._json_response(200, {
                "status": "success",
                "session_id": parts[2],
                "chat_id": parts[3],
                "message": "对话已请求中止",
            })
        elif self.path == "/audio/transcriptions":
            self._handle_transcription()
        elif self.path == "/image/understanding":
            self._handle_image_understanding()
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

    def do_PATCH(self):
        if self.path.startswith("/sess/"):
            length = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(length)) if length > 0 else {}
            self._json_response(200, {
                "status": "success",
                "session_id": self.path.split("/")[-1],
                "title": body.get("title", ""),
            })
        else:
            self.send_error(404)

    def do_DELETE(self):
        if self.path.startswith("/sess/"):
            self._json_response(200, {
                "status": "success",
                "session_id": self.path.split("/")[-1],
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

        # 以 OpenAI chat.completion.chunk 格式输出事件，最后以 [DONE] 结束
        chunks = [
            _chunk({"role": "assistant", "reasoning_content": "让我思考一下..."}),
            _chunk({"role": "assistant", "content": "分析结果如下：" + body.get("instruction", "")[:20]}),
            _chunk({
                "role": "tool",
                "tool_call_id": "call_1",
                "tool_name": "test_skill",
                "content": "工具输出",
                "error": False,
            }),
            _chunk({}, finish_reason="stop"),
        ]
        events = ["data: " + json.dumps(c, ensure_ascii=False) + "\n\n" for c in chunks]
        events.append("data: [DONE]\n\n")
        payload = "".join(events)
        self.wfile.write(payload.encode())
        self.wfile.flush()

    def _handle_transcription(self):
        """模拟 POST /audio/transcriptions：校验 multipart，回显 model 字段。"""
        ctype = self.headers.get("Content-Type", "")
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length) if length > 0 else b""
        if not ctype.startswith("multipart/form-data"):
            self._json_response(400, {"status": "invalid_request", "message": "expect multipart"})
            return
        if b'name="file"' not in body:
            self._json_response(400, {"status": "invalid_request", "message": "缺少 file 字段"})
            return
        # 从表单里取 model；客户端没传时模拟服务端回落到默认语音模型
        model = "whisper-1"
        marker = b'name="model"\r\n\r\n'
        if marker in body:
            start = body.index(marker) + len(marker)
            end = body.index(b"\r\n", start)
            model = body[start:end].decode()
        self._json_response(200, {"text": "你好，世界", "model": model})

    def _handle_image_understanding(self):
        """模拟 POST /image/understanding：校验图片附件，回显 model 与 instruction。"""
        length = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(length)) if length > 0 else {}
        attachments = body.get("attachments") or []
        if not attachments or attachments[0].get("type") != "image" \
                or not attachments[0].get("content"):
            self._json_response(400, {"status": "invalid_request", "message": "必须提供 1 张图片附件"})
            return
        model = self.headers.get("X-Model-Name") or "qwen-vl-max"
        self._json_response(200, {
            "text": "图里是一张发票",
            "model": model,
            "instruction": body.get("instruction", ""),
        })

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
        # 正常结束时不设置 status / error
        self.assertNotIn("status", result)
        self.assertNotIn("error", result)
        event_types = [e[0] for e in events]
        self.assertIn("message", event_types)

    def test_execute_chat_event_types(self):
        events = []

        def collect(event_type, data):
            events.append((event_type, data))

        self.client.execute_chat("测试指令", callback=collect)

        event_types = [e[0] for e in events]
        self.assertEqual(event_types, ["thinking", "message", "tool_result", "finish"])
        self.assertNotIn("completed", event_types)
        self.assertNotIn("unknown", event_types)

        # 回调收到的 data 已摊平为 delta 本身
        message_data = next(d for t, d in events if t == "message")
        self.assertNotIn("choices", message_data)
        self.assertTrue(message_data["content"].startswith("分析结果如下"))
        tool_data = next(d for t, d in events if t == "tool_result")
        self.assertEqual(tool_data["tool_call_id"], "call_1")
        finish_data = next(d for t, d in events if t == "finish")
        self.assertEqual(finish_data, {"finish_reason": "stop"})

    def test_flatten_chunk_message(self):
        raw = _chunk({"role": "assistant", "content": "hi"})
        flat = GrootClient._flatten_chunk(raw)
        self.assertEqual(flat, {"role": "assistant", "content": "hi"})
        self.assertEqual(GrootClient._classify_event(flat), "message")
        # 返回的是拷贝，不影响原始 delta
        self.assertIsNot(flat, raw["choices"][0]["delta"])

    def test_flatten_chunk_finish(self):
        raw = _chunk({}, finish_reason="stop")
        flat = GrootClient._flatten_chunk(raw)
        self.assertEqual(flat, {"finish_reason": "stop"})
        self.assertEqual(GrootClient._classify_event(flat), "finish")
        # delta 为 None 时视为 {}
        raw_none = _chunk(None, finish_reason="stop")
        self.assertEqual(GrootClient._flatten_chunk(raw_none), {"finish_reason": "stop"})

    def test_flatten_chunk_error(self):
        raw = {"error": {"message": "boom", "type": "server_error", "code": None, "param": None}}
        flat = GrootClient._flatten_chunk(raw)
        self.assertEqual(flat, {"event": "error", "message": "boom", "error": raw["error"]})
        self.assertEqual(GrootClient._classify_event(flat), "error")
        # 无 error / choices 的对象原样返回
        other = {"foo": "bar"}
        self.assertIs(GrootClient._flatten_chunk(other), other)

    def test_flatten_usage_chunk(self):
        raw = {"id": "c", "object": "chat.completion.chunk", "created": 1, "model": "m",
               "choices": [], "usage": {"prompt_tokens": 120, "completion_tokens": 45, "total_tokens": 165}}
        flat = GrootClient._flatten_chunk(raw)
        self.assertEqual(flat, {"event": "usage", "usage": raw["usage"]})
        self.assertEqual(GrootClient._classify_event(flat), "usage")

    def test_flatten_empty_choices_without_usage_passthrough(self):
        raw = {"choices": [], "usage": None}
        self.assertIs(GrootClient._flatten_chunk(raw), raw)

    def test_execute_chat_with_session(self):
        result = self.client.execute_chat("继续分析", session_id="existing_sid")
        self.assertEqual(result["session_id"], "existing_sid")

    def test_execute_chat_with_prompt(self):
        result = self.client.execute_chat("指令", prompt="你是专家")
        self.assertIsNotNone(result["session_id"])

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

    def test_stop_chat(self):
        resp = self.client.stop_chat("test_sid", "test_cid")
        self.assertEqual(resp["status"], "success")
        self.assertEqual(resp["session_id"], "test_sid")
        self.assertEqual(resp["chat_id"], "test_cid")

    def test_rename_session(self):
        resp = self.client.rename_session("test_sid", "新标题")
        self.assertEqual(resp["status"], "success")
        self.assertEqual(resp["session_id"], "test_sid")
        self.assertEqual(resp["title"], "新标题")

    def test_delete_session(self):
        resp = self.client.delete_session("test_sid")
        self.assertEqual(resp["status"], "success")
        self.assertEqual(resp["session_id"], "test_sid")

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

    def _tmp_audio(self):
        f = tempfile.NamedTemporaryFile(suffix=".webm", delete=False)
        f.write(b"FAKE-AUDIO")
        f.close()
        self.addCleanup(os.unlink, f.name)
        return f.name

    def test_transcribe_default_model(self):
        result = self.client.transcribe(self._tmp_audio())
        self.assertEqual(result["text"], "你好，世界")
        # 未指定 model 时由服务端回落到默认语音模型
        self.assertEqual(result["model"], "whisper-1")

    def test_transcribe_explicit_model(self):
        result = self.client.transcribe(self._tmp_audio(), model="my-asr", language="zh")
        self.assertEqual(result["model"], "my-asr")

    def _tmp_png(self):
        f = tempfile.NamedTemporaryFile(suffix=".png", delete=False)
        f.write(b"\x89PNG\r\n\x1a\n" + b"\x00" * 32)
        f.close()
        self.addCleanup(os.unlink, f.name)
        return f.name

    def test_understand_image_default_instruction(self):
        result = self.client.understand_image(self._tmp_png())
        # 未指定 instruction 时客户端不下发该字段，由服务端使用默认指令
        self.assertEqual(result["instruction"], "")
        self.assertEqual(result["model"], "qwen-vl-max")

    def test_understand_image_explicit_instruction(self):
        result = self.client.understand_image(self._tmp_png(), instruction="提取文字")
        self.assertEqual(result["instruction"], "提取文字")

    def test_understand_image_explicit_model(self):
        result = self.client.understand_image(self._tmp_png(), model="my-vlm")
        self.assertEqual(result["model"], "my-vlm")
