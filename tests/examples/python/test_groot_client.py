"""
GrootClient 测试（OpenAI 官方 SDK 驱动本地 mock 服务）。

被测对象是 examples/python/groot_client.py —— 一个基于官方 `openai` 包的示例客户端。
本测试用真实 SDK 打本地 HTTPServer 的 mock 服务，验证 Groot 的 OpenAI 兼容契约：

    1. 请求走 POST /chat/completions，且路径里没有 /v1 前缀
    2. 认证走 Authorization: Bearer <key>（不再有 X-API-Key 之类的自定义头）
    3. 扩展字段 session_id / agent_name / user 出现在请求体顶层
    4. 非流式响应顶层的 session_id 能被客户端读到
    5. 流式每个 chunk 顶层的 session_id 能被客户端读到
    6. HTTP 400 错误体触发 openai.BadRequestError，且能取到 message

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
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# 将 examples/python/ 加入搜索路径
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "../../../examples/python"))

import openai  # noqa: E402
from groot_client import GrootClient, _extra  # noqa: E402


# ---- 响应构造辅助 ----

def _chunk(content=None, finish_reason=None, session_id=None, role=None):
    """构造一条 chat.completion.chunk；session_id 放在顶层（Groot 扩展字段）。"""
    delta = {}
    if role:
        delta["role"] = role
    if content is not None:
        delta["content"] = content
    obj = {
        "id": "chatcmpl-test",
        "object": "chat.completion.chunk",
        "created": 1,
        "model": "test-model",
        "choices": [{"index": 0, "delta": delta, "finish_reason": finish_reason}],
    }
    if session_id is not None:
        obj["session_id"] = session_id
    return obj


def _completion(content, session_id="sess_001", model="test-model"):
    """构造一条非流式 chat.completion；session_id 放在顶层（Groot 扩展字段）。"""
    return {
        "id": "chatcmpl-test",
        "object": "chat.completion",
        "created": 1,
        "model": model,
        "choices": [{
            "index": 0,
            "message": {"role": "assistant", "content": content},
            "finish_reason": "stop",
        }],
        "usage": {"prompt_tokens": 5, "completion_tokens": 3, "total_tokens": 8},
        "session_id": session_id,
    }


# ---- Mock Groot Server ----

class MockGrootHandler(BaseHTTPRequestHandler):
    """记录收到的请求，并按队列返回预置响应。"""

    protocol_version = "HTTP/1.1"

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(length) if length > 0 else b""
        self.server.recorded.append({
            "method": "POST",
            "path": self.path,
            # HTTP 头名大小写不敏感，统一小写便于断言
            "headers": {k.lower(): v for k, v in self.headers.items()},
            "body": json.loads(raw) if raw else None,
        })
        if not self.server.responses:
            self._send(500, "application/json", b'{"error":"no response enqueued"}')
            return
        status, ctype, payload = self.server.responses.pop(0)
        self._send(status, ctype, payload)

    def do_GET(self):
        self.server.recorded.append({
            "method": "GET",
            "path": self.path,
            "headers": {k.lower(): v for k, v in self.headers.items()},
            "body": None,
        })
        status, ctype, payload = self.server.responses.pop(0)
        self._send(status, ctype, payload)

    def _send(self, status, ctype, payload):
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)
        self.wfile.flush()

    def log_message(self, fmt, *args):
        pass  # 抑制日志


_server = None
_server_thread = None
_server_url = None


def setUpModule():
    global _server, _server_thread, _server_url
    _server = ThreadingHTTPServer(("127.0.0.1", 0), MockGrootHandler)
    _server.recorded = []
    _server.responses = []
    _server_url = "http://127.0.0.1:%d" % _server.server_address[1]
    _server_thread = threading.Thread(target=_server.serve_forever, daemon=True)
    _server_thread.start()
    time.sleep(0.05)


def tearDownModule():
    if _server:
        _server.shutdown()
        _server.server_close()


def _enqueue(status, ctype, obj):
    """入队一个响应；obj 为 dict/list 时序列化为 JSON，为 str 时当作 SSE 原文。"""
    if isinstance(obj, (dict, list)):
        payload = json.dumps(obj, ensure_ascii=False).encode()
    else:
        payload = obj.encode()
    _server.responses.append((status, ctype, payload))


def _sse(*chunks):
    """把若干 chunk 拼成 SSE 流，末尾补 data: [DONE]。"""
    lines = ["data: " + json.dumps(c, ensure_ascii=False) + "\n\n" for c in chunks]
    lines.append("data: [DONE]\n\n")
    return "".join(lines)


def _last_request():
    return _server.recorded[-1]


class TestGrootClient(unittest.TestCase):
    """GrootClient 的 OpenAI 兼容契约。"""

    def setUp(self):
        _server.recorded.clear()
        del _server.responses[:]
        # 关闭 SDK 重试，保证 1:1 请求数，便于断言
        self.client = GrootClient(_server_url, "test-key", user="alice")
        self.client.client = self.client.client.with_options(max_retries=0)

    # ---- 请求形状 ----

    def test_request_path_and_auth_header(self):
        _enqueue(200, "application/json", _completion("你好"))
        self.client.chat("你好", model="test-model")

        req = _last_request()
        self.assertEqual(req["path"], "/chat/completions")
        self.assertNotIn("/v1", req["path"])
        self.assertEqual(req["headers"].get("authorization"), "Bearer test-key")
        # 旧自定义头一律不再出现
        self.assertNotIn("x-api-key", req["headers"])
        self.assertNotIn("x-session-id", req["headers"])
        self.assertNotIn("x-chat-id", req["headers"])

    def test_extension_fields_in_request_body_top_level(self):
        _enqueue(200, "application/json", _completion("ok"))
        self.client.chat("你好", model="test-model", session_id="sess_existing",
                         agent_name="analyst")

        body = _last_request()["body"]
        self.assertEqual(body["model"], "test-model")
        self.assertEqual(body["messages"], [{"role": "user", "content": "你好"}])
        self.assertEqual(body["session_id"], "sess_existing")
        self.assertEqual(body["agent_name"], "analyst")
        self.assertEqual(body["user"], "alice")

    def test_extension_fields_omitted_when_absent(self):
        _enqueue(200, "application/json", _completion("ok"))
        client = GrootClient(_server_url, "test-key")  # 不带 user
        client.client = client.client.with_options(max_retries=0)
        client.chat("你好", model="test-model")

        body = _last_request()["body"]
        self.assertNotIn("session_id", body)
        self.assertNotIn("agent_name", body)
        self.assertNotIn("user", body)

    # ---- 非流式 ----

    def test_chat_reads_top_level_session_id(self):
        _enqueue(200, "application/json", _completion("分析结果", session_id="sess_new"))
        resp = self.client.chat("分析数据", model="test-model", agent_name="analyst")

        self.assertEqual(resp["content"], "分析结果")
        self.assertEqual(resp["session_id"], "sess_new")
        self.assertEqual(resp["model"], "test-model")

    def test_list_models(self):
        _enqueue(200, "application/json", {
            "object": "list",
            "data": [{"id": "qwen-max", "object": "model", "created": 1, "owned_by": "groot"}],
        })
        self.assertEqual(self.client.list_models(), ["qwen-max"])

        req = _last_request()
        self.assertEqual(req["path"], "/models")
        self.assertEqual(req["headers"].get("authorization"), "Bearer test-key")

    # ---- 流式 ----

    def test_chat_stream_collects_text_and_session_id(self):
        _enqueue(200, "text/event-stream", _sse(
            _chunk(role="assistant", session_id="sess_stream"),
            _chunk(content="你好", session_id="sess_stream"),
            _chunk(content="，世界", session_id="sess_stream"),
            _chunk(finish_reason="stop", session_id="sess_stream"),
        ))

        pieces = list(self.client.chat_stream("打个招呼", model="test-model"))

        self.assertEqual("".join(p[0] for p in pieces), "你好，世界")
        self.assertEqual(pieces[0][1], "sess_stream")

        body = _last_request()["body"]
        self.assertIs(body["stream"], True)
        self.assertEqual(body["user"], "alice")

    def test_chat_stream_passes_session_id_in_body(self):
        _enqueue(200, "text/event-stream", _sse(
            _chunk(content="继续", session_id="sess_existing"),
            _chunk(finish_reason="stop", session_id="sess_existing"),
        ))
        list(self.client.chat_stream("继续", model="test-model", session_id="sess_existing",
                                     agent_name="analyst"))

        body = _last_request()["body"]
        self.assertEqual(body["session_id"], "sess_existing")
        self.assertEqual(body["agent_name"], "analyst")

    # ---- 扩展字段读取形状 ----

    def test_extra_reads_attribute_when_container_absent(self):
        """pydantic v1 形状：只有属性访问，没有 model_extra 时也要取到 session_id。"""

        class AttrOnly:
            session_id = "sess_attr"

        self.assertEqual(_extra(AttrOnly(), "session_id"), "sess_attr")

    def test_extra_falls_back_to_model_extra(self):
        """只有在 model_extra 容器里（属性缺失）时也能取到 session_id。"""

        class ContainerOnly:
            model_extra = {"session_id": "sess_container"}

        self.assertEqual(_extra(ContainerOnly(), "session_id"), "sess_container")

    def test_extra_returns_none_when_both_absent(self):
        """属性与容器都没有该字段时返回 None，不抛异常。"""

        class Neither:
            pass

        self.assertIsNone(_extra(Neither(), "session_id"))

    # ---- 错误路径 ----

    def test_bad_request_raises_bad_request_error(self):
        _enqueue(400, "application/json", {
            "error": {
                "message": "messages 不能为空",
                "type": "invalid_request_error",
                "param": "messages",
                "code": None,
            }
        })
        with self.assertRaises(openai.BadRequestError) as ctx:
            self.client.chat("", model="test-model")

        err = ctx.exception
        self.assertEqual(err.status_code, 400)
        # 官方 SDK 的 str(err) 形如 "Error code: 400 - <error.message>"
        self.assertIn("messages 不能为空", err.message)
        # 结构化错误体：err.body 是 OpenAI 的 error 对象本身（message/type/param/code）
        self.assertEqual(err.body["message"], "messages 不能为空")
        self.assertEqual(err.body["type"], "invalid_request_error")
        self.assertEqual(err.body["param"], "messages")

    def test_bad_request_error_on_stream(self):
        _enqueue(400, "application/json", {
            "error": {
                "message": "模型不存在",
                "type": "invalid_request_error",
                "param": "model",
                "code": None,
            }
        })
        with self.assertRaises(openai.BadRequestError) as ctx:
            # 流式请求在拿到响应头时就抛错（消费迭代器之前）
            self.client.client.chat.completions.create(
                model="no-such-model",
                messages=[{"role": "user", "content": "hi"}],
                stream=True,
            )
        self.assertIn("模型不存在", ctx.exception.message)


if __name__ == "__main__":
    unittest.main()
