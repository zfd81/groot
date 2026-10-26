"""
Groot AI Agent Python 客户端。

Usage:
    from groot_client import GrootClient

    # user_id 作为 X-User-ID 请求头随每个请求发送，标识当前业务用户；
    # 会话类接口（/chat、/chat/*、/sess/*）要求携带，会话按（API Key 所属租户，用户）归属
    client = GrootClient("http://localhost:8080", api_key="your-key", user_id="alice")

    # 新会话
    def on_event(event_type, data):
        if event_type == "message":
            print(data["content"], end="")

    result = client.execute_chat("帮我分析数据", callback=on_event)
    print(f"会话ID: {result['session_id']}")

    # 继续会话（多轮对话）
    result2 = client.execute_chat("生成图表", session_id=result["session_id"])

SSE 格式说明:
    POST /chat 以 OpenAI chat.completion.chunk 格式流式返回，每条 data 行形如：

        data: {"id":"...","object":"chat.completion.chunk","created":...,"model":"...",
               "choices":[{"index":0,"delta":{"role":"assistant","content":"..."},"finish_reason":null}]}

    出错时为 {"error":{"message":"...","type":"...","code":null,"param":null}}，
    流末尾为 data: [DONE]。

    客户端会把每条 chunk 摊平后再交给 callback(event_type, data)：
    data 即 choices[0].delta（finish_reason 非 null 时并入），错误时为
    {"event":"error","message":"...","error":{...}}。event_type 取值：
    thinking / message / tool_calls / tool_result / finish / error / unknown。
"""

import base64
import json
import os
from typing import Optional, Callable

import requests


class GrootClient:
    """
    Groot AI Agent 客户端。

    Args:
        base_url: 服务地址，如 http://localhost:8080。
        api_key: 在 Web 界面创建的 API Key，作为 X-API-Key 请求头发送。
        user_id: 当前业务用户标识（1 到 64 个字符，仅限字母、数字、下划线、短横线），
            作为 X-User-ID 请求头随每个请求发送。会话类接口（/chat、/chat/*、/sess/*）
            要求携带，服务端以（API Key 所属租户，user_id）记录并隔离会话归属；
            缺失时这些接口返回 400。
    """

    def __init__(self, base_url: str, api_key: Optional[str] = None, user_id: Optional[str] = None):
        self.base_url = base_url.rstrip("/")
        self.session = requests.Session()
        self.session.headers["Content-Type"] = "application/json"
        if api_key:
            self.session.headers["X-API-Key"] = api_key
        if user_id:
            self.session.headers["X-User-ID"] = user_id

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
            {"session_id": "...", "chat_id": "..."}；
            出错时额外包含 "status": "error" 与 "error": 错误信息。
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

            # 解析 chunk JSON，摊平为扁平事件对象后推断事件类型
            try:
                parsed = json.loads(data)
            except json.JSONDecodeError:
                continue

            flat = self._flatten_chunk(parsed)
            event_type = self._classify_event(flat)

            if callback:
                callback(event_type, flat)

            if event_type == "error":
                result["status"] = "error"
                result["error"] = flat.get("message", data)

        return result

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

    def stop_chat(self, session_id: str, chat_id: str) -> dict:
        """中止指定会话中正在执行的对话（需要 chat 权限）。

        返回体的 status 为 success（本节点已取消）或 accepted（已广播到集群）。
        对话未在执行时服务端返回 409 chat_not_running，raise_for_status 抛出 HTTPError。
        """
        resp = self.session.post(f"{self.base_url}/chat/{session_id}/{chat_id}/stop")
        resp.raise_for_status()
        return resp.json()

    def get_session_detail(self, session_id: str) -> dict:
        """查询会话详情（包含完整对话历史）。"""
        resp = self.session.get(f"{self.base_url}/sess/{session_id}")
        resp.raise_for_status()
        return resp.json()

    def rename_session(self, session_id: str, title: str) -> dict:
        """重命名会话（需要 session 权限）。

        title 去首尾空白后须为 1 到 100 个字符，否则服务端返回 400 invalid_request，
        raise_for_status 抛出 HTTPError。重命名不改变会话排序与最后活跃时间。
        """
        resp = self.session.patch(
            f"{self.base_url}/sess/{session_id}",
            json={"title": title},
        )
        resp.raise_for_status()
        return resp.json()

    def delete_session(self, session_id: str) -> dict:
        """删除会话及其全部对话记录（需要 delete 权限）。

        会话有对话正在执行时服务端先中止再删除；等待超时返回 409 chat_still_running，
        raise_for_status 抛出 HTTPError，此时未删除任何数据。
        """
        resp = self.session.delete(f"{self.base_url}/sess/{session_id}")
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
    def _flatten_chunk(raw: dict) -> dict:
        """将 OpenAI chat.completion.chunk 摊平为扁平事件对象。

        - 含 "error" 键：返回 {"event": "error", "message": <error.message>, "error": <error 对象>}
        - 含非空 "choices"：返回 choices[0].delta 的拷贝；finish_reason 非 None 时并入
        - "choices" 为空列表且顶层 "usage" 为对象：返回 {"event": "usage", "usage": <usage 对象>}
        - 其他：原样返回
        """
        if "error" in raw:
            err = raw["error"]
            return {
                "event": "error",
                "message": err.get("message", "") if isinstance(err, dict) else "",
                "error": err,
            }

        if raw.get("choices"):
            choice = raw["choices"][0]
            delta = dict(choice.get("delta") or {})
            finish_reason = choice.get("finish_reason")
            if finish_reason is not None:
                delta["finish_reason"] = finish_reason
            return delta

        usage = raw.get("usage")
        if isinstance(raw.get("choices"), list) and isinstance(usage, dict):
            return {"event": "usage", "usage": usage}

        return raw

    @staticmethod
    def _classify_event(data: dict) -> str:
        """根据摊平后的事件对象推断 SSE 事件类型。"""
        if data.get("event") == "error":
            return "error"
        if data.get("event") == "usage":
            return "usage"
        if data.get("role") == "tool":
            return "tool_result"
        if "tool_calls" in data:
            return "tool_calls"
        if "finish_reason" in data:
            return "finish"
        if data.get("role") == "assistant":
            if "reasoning_content" in data:
                return "thinking"
            if "content" in data:
                return "message"
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

    def transcribe(self, audio_path: str, model: Optional[str] = None,
                   language: Optional[str] = None) -> dict:
        """音频转文字。返回 {"text": 识别文本, "model": 实际使用的模型}。

        model 省略时由服务端使用默认语音模型。
        """
        # 该接口是 multipart 表单。session 默认带 application/json 的 Content-Type，
        # 这里显式置 None 让 requests 按 files 自动生成 multipart 边界。
        data = {}
        if model:
            data["model"] = model
        if language:
            data["language"] = language
        with open(audio_path, "rb") as f:
            resp = self.session.post(
                f"{self.base_url}/audio/transcriptions",
                headers={"Content-Type": None},
                data=data,
                files={"file": (os.path.basename(audio_path), f)},
            )
        resp.raise_for_status()
        return resp.json()

    def understand_image(self, image_path: str, instruction: Optional[str] = None,
                         model: Optional[str] = None) -> dict:
        """图片解析。返回 {"text": 模型输出, "model": 实际使用的模型}，上游给用量时含 usage。

        本接口与会话无关：不产生会话、不写历史。instruction 省略时由服务端使用默认指令；
        model 省略时由服务端使用默认视觉模型。
        """
        with open(image_path, "rb") as f:
            content = base64.b64encode(f.read()).decode()
        payload = {"attachments": [{
            "type": "image",
            "name": os.path.basename(image_path),
            "content": content,
        }]}
        if instruction:
            payload["instruction"] = instruction
        headers = {"X-Model-Name": model} if model else None
        resp = self.session.post(f"{self.base_url}/image/understanding",
                                 json=payload, headers=headers)
        resp.raise_for_status()
        return resp.json()
