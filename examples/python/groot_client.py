"""
Groot AI Agent Python 示例（OpenAI 官方 SDK）。

Groot 对外提供 OpenAI 兼容的 REST 接口，因此客户端直接使用官方 `openai` 包，
**只需替换 base_url 与 api_key**，其余调用方式与调用 OpenAI 完全一致：

    - 接口路径：POST /chat/completions（没有 /v1 前缀，base_url 也不带 /v1）
    - 认证方式：Authorization: Bearer <api_key>
    - 模型列表：GET /models

在 OpenAI 标准字段之外，Groot 额外支持三个扩展字段，放在**请求体顶层**
（通过 SDK 的 extra_body 透传）：

    session_id  会话 ID。不传则创建新会话；服务端在响应体顶层返回它，
                后续请求带上同一个 session_id 即为多轮对话。
    agent_name  指定使用的智能体名称，省略时使用默认智能体。
    user        业务用户标识（1~64 个字符，字母/数字/下划线/短横线），
                用于会话归属与隔离。

Usage:
    export GROOT_BASE_URL=http://localhost:8080
    export GROOT_API_KEY=your-api-key
    python3 groot_client.py

依赖:
    pip install -r requirements.txt
"""

import os
from typing import Iterator, List, Optional

from openai import OpenAI


# 扩展字段的读取辅助：官方 SDK 对未知顶层字段的处理随 pydantic 版本而异 ——
# pydantic v2 下同时提供属性访问与 model_extra 容器，v1 下只有属性访问。
def _extra(obj, key: str) -> Optional[str]:
    value = getattr(obj, key, None)
    if value:
        return value
    extra = getattr(obj, "model_extra", None) or {}
    return extra.get(key)


class GrootClient:
    """Groot AI Agent 客户端（官方 openai SDK 的薄包装）。

    Args:
        base_url: Groot 服务地址，如 http://localhost:8080。**不要**带 /v1。
        api_key: 在 Web 界面创建的 API Key，SDK 会以 Bearer 方式发送。
        user: 业务用户标识；作为扩展字段 user 随每个请求发送。
    """

    def __init__(self, base_url: str, api_key: str, user: Optional[str] = None):
        self.client = OpenAI(base_url=base_url, api_key=api_key)
        self.user = user

    # ---- 请求参数组装 ----

    def _extra_body(self, session_id: Optional[str], agent_name: Optional[str]) -> dict:
        """组装扩展字段；None 的字段不下发，由服务端使用默认值。"""
        body = {}
        if session_id:
            body["session_id"] = session_id
        if agent_name:
            body["agent_name"] = agent_name
        if self.user:
            body["user"] = self.user
        return body

    # ---- 非流式 ----

    def chat(
        self,
        message: str,
        model: str,
        session_id: Optional[str] = None,
        agent_name: Optional[str] = None,
    ) -> dict:
        """一次性返回完整回答。

        Returns:
            {"content": 回答文本, "session_id": 会话 ID, "model": 实际使用的模型}
        """
        resp = self.client.chat.completions.create(
            model=model,
            messages=[{"role": "user", "content": message}],
            extra_body=self._extra_body(session_id, agent_name),
        )
        return {
            "content": resp.choices[0].message.content,
            "session_id": _extra(resp, "session_id"),
            "model": resp.model,
        }

    # ---- 流式 ----

    def chat_stream(
        self,
        message: str,
        model: str,
        session_id: Optional[str] = None,
        agent_name: Optional[str] = None,
    ) -> Iterator[tuple]:
        """流式返回，逐条产出 (增量文本, 会话 ID)。

        session_id 出现在每个 chunk 的顶层，通常在首条 chunk 读到。
        """
        stream = self.client.chat.completions.create(
            model=model,
            messages=[{"role": "user", "content": message}],
            stream=True,
            extra_body=self._extra_body(session_id, agent_name),
        )
        for chunk in stream:
            delta = ""
            if chunk.choices and chunk.choices[0].delta.content:
                delta = chunk.choices[0].delta.content
            yield delta, _extra(chunk, "session_id")

    # ---- 模型列表（标准 OpenAI 接口）----

    def list_models(self) -> List[str]:
        """GET /models，返回可用模型 ID 列表。"""
        return [m.id for m in self.client.models.list().data]


def main() -> None:
    base_url = os.environ.get("GROOT_BASE_URL", "http://localhost:8080")
    api_key = os.environ.get("GROOT_API_KEY", "your-api-key")
    model = os.environ.get("GROOT_MODEL", "qwen-max")

    client = GrootClient(base_url=base_url, api_key=api_key, user="alice")

    print("== 可用模型 ==")
    for model_id in client.list_models():
        print(" -", model_id)

    # 1. 非流式：首次提问，服务端创建新会话并在响应顶层返回 session_id
    print("\n== 非流式 ==")
    result = client.chat("用一句话介绍你自己", model=model, agent_name="analyst")
    print("回答:", result["content"])
    print("会话 ID:", result["session_id"])

    # 2. 流式：带上上一步的 session_id 继续多轮对话
    print("\n== 流式（续用同一会话）==")
    session_id = result["session_id"]
    for text, chunk_session_id in client.chat_stream(
        "继续，再说一句", model=model, session_id=session_id, agent_name="analyst"
    ):
        if chunk_session_id:
            session_id = chunk_session_id
        print(text, end="", flush=True)
    print("\n会话 ID:", session_id)


if __name__ == "__main__":
    main()
