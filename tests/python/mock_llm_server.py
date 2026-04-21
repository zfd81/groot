#!/usr/bin/env python3
"""
Simple Mock LLM Server for testing
Responds to OpenAI-style API requests with mock responses
"""

import json
import time
from http.server import HTTPServer, BaseHTTPRequestHandler

class MockLLMHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        # Handle OpenAI-style API requests (with /v1/ prefix)
        if self.path.endswith('/chat/completions') or self.path == '/v1/chat/completions':
            content_length = int(self.headers['Content-Length'])
            body = json.loads(self.rfile.read(content_length))

            # Read the messages from request
            messages = body.get('messages', [])

            # Simulate some processing time (10 seconds for testing cancellation/concurrent)
            time.sleep(10)

            # Generate a mock response
            response = {
                "id": "mock-response-id",
                "object": "chat.completion",
                "created": int(time.time()),
                "model": body.get('model', 'mock'),
                "choices": [{
                    "index": 0,
                    "message": {
                        "role": "assistant",
                        "content": "这是一个模拟的 LLM 响应。根据您的请求，我已经完成了任务。"
                    },
                    "finish_reason": "stop"
                }],
                "usage": {
                    "prompt_tokens": 100,
                    "completion_tokens": 50,
                    "total_tokens": 150
                }
            }

            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(json.dumps(response).encode())
        else:
            self.send_response(404)
            self.end_headers()

    def log_message(self, format, *args):
        pass  # Suppress logging

if __name__ == '__main__':
    server = HTTPServer(('localhost', 8888), MockLLMHandler)
    print("Mock LLM server running on http://localhost:8888")
    server.serve_forever()