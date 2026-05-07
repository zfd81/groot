"""
Groot AI Agent Python 客户端。

Usage:
    from groot_client import GrootClient

    client = GrootClient("http://localhost:8080", api_key="your-key")

    # 新会话
    def on_event(event_type, data):
        if event_type == "message":
            print(data, end="")

    result = client.execute_chat("帮我分析数据", callback=on_event)
    print(f"会话ID: {result['session_id']}")

    # 继续会话（多轮对话）
    result2 = client.execute_chat("生成图表", session_id=result["session_id"])
"""

import json
from typing import Optional, Callable

import requests


class GrootClient:
    """Groot AI Agent 客户端"""

    def __init__(self, base_url: str, api_key: Optional[str] = None):
        self.base_url = base_url.rstrip("/")
        self.session = requests.Session()
        self.session.headers["Content-Type"] = "application/json"
        if api_key:
            self.session.headers["X-API-Key"] = api_key

    # ---- 核心接口 ----

    def execute_chat(
        self,
        instruction: str,
        attachments: Optional[list] = None,
        session_id: Optional[str] = None,
        prompt: Optional[str] = None,
        model_name: Optional[str] = None,
        callback: Optional[Callable[[str, str], None]] = None,
    ) -> dict:
        """
        执行对话（SSE 流式返回）。

        Args:
            instruction: 用户任务指令。
            attachment: 附件列表，格式 [{"type": "file", "name": "x.pdf", "content": "base64..."}]。
            session_id: 会话ID，传 None 创建新会话。
            prompt: 系统提示词。
            model_name: 指定模型名称。
            callback: 回调函数 callback(event_type, data)，event_type 见 README API 事件类型表。

        Returns:
            {"session_id": "...", "chat_id": "...", "result": ..., "status": "success|error"}
        """
        body = {"instruction": instruction}
        if prompt:
            body["prompt"] = prompt
        if attachments:
            body["attachments"] = attachments

        headers = {}
        if session_id:
            headers["X-Session-ID"] = session_id
        if model_name:
            headers["X-Model-Name"] = model_name

        response = self.session.post(
            f"{self.base_url}/chat",
            headers=headers,
            json=body,
            stream=True,
        )
        response.raise_for_status()

        result = {
            "session_id": response.headers.get("X-Session-ID"),
            "chat_id": response.headers.get("X-Chat-ID"),
        }

        for line in response.iter_lines(decode_unicode=True):
            if not line:
                continue
            if line.startswith("event:"):
                # 服务器可能发送 event: 行，跳过（事件类型从 data JSON 推断）
                continue
            if not line.startswith("data:"):
                continue
            data = line[5:].strip()
            if data == "[DONE]":
                break

            # 解析事件 JSON，推断事件类型
            try:
                parsed = json.loads(data)
            except json.JSONDecodeError:
                continue

            event_type = self._classify_event(parsed)

            if callback:
                callback(event_type, parsed)

            if event_type == "completed":
                result["status"] = parsed.get("status")
                if parsed.get("status") == "success":
                    result["result"] = parsed.get("result")
            elif event_type == "error":
                result["status"] = "error"
                result["error"] = parsed.get("message", data)

        return result

    def cancel_chat(self, session_id: str) -> dict:
        """取消指定会话中正在执行的对话。"""
        resp = self.session.delete(f"{self.base_url}/chat/{session_id}")
        resp.raise_for_status()
        return resp.json()

    def get_chat_status(self, session_id: str) -> dict:
        """查询指定会话最近一次对话的运行状态。"""
        resp = self.session.get(f"{self.base_url}/chat/status/{session_id}")
        resp.raise_for_status()
        return resp.json()

    def get_chat_detail(self, session_id: str, chat_id: str) -> dict:
        """查询指定会话中某次对话的完整详情。"""
        resp = self.session.get(f"{self.base_url}/chat/{session_id}/{chat_id}")
        resp.raise_for_status()
        return resp.json()

    def get_session_detail(self, session_id: str) -> dict:
        """查询会话详情（包含完整对话历史）。"""
        resp = self.session.get(f"{self.base_url}/sess/{session_id}")
        resp.raise_for_status()
        return resp.json()

    def list_sessions(self, limit: int = 20, offset: int = 0) -> dict:
        """分页查询会话列表。"""
        resp = self.session.get(
            f"{self.base_url}/sess/history",
            params={"limit": limit, "offset": offset},
        )
        resp.raise_for_status()
        return resp.json()

    # ---- 内部方法 ----

    @staticmethod
    def _classify_event(data: dict) -> str:
        """根据 JSON 内容推断 SSE 事件类型。"""
        if data.get("role") == "tool":
            return "tool_result"
        if data.get("role") == "assistant":
            if "tool_calls" in data:
                return "tool_calls"
            if "finish_reason" in data:
                return "finish"
            if "reasoning_content" in data:
                return "thinking"
            if "content" in data:
                return "message"
        if "status" in data:
            return "completed"
        return "unknown"

    # ---- 查询接口 ----

    def health_check(self) -> dict:
        """健康检查。"""
        resp = self.session.get(f"{self.base_url}/health")
        resp.raise_for_status()
        return resp.json()

    def list_skills(self) -> dict:
        """列出可用 Skills。"""
        resp = self.session.get(f"{self.base_url}/skills")
        resp.raise_for_status()
        return resp.json()

    def list_tools(self) -> dict:
        """列出可用 MCP 工具。"""
        resp = self.session.get(f"{self.base_url}/tools")
        resp.raise_for_status()
        return resp.json()
