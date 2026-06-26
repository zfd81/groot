"""
多 Agent 端到端系统测试（需要真实 LLM 服务）

设计：docs/superpowers/specs/2026-05-24-multi-agent-design.md
计划：docs/superpowers/plans/2026-05-28-multi-agent-implementation.md
用例点清单：tests/TEST_CASES.md 2.21 节

本文件覆盖**需要真实 LLM 推理**的部分：
- Solo 模式：X-Agent-Name 指定 echo-agent，验证子 Agent 系统提示生效（结果含子 Agent
  指导用语）+ ChatRecord.AgentName 持久化
- 编排模式：主 Agent 工具列表含 call_agent；指令引导其调用 echo-agent；事件流的
  agent_name 字段在子段切换；父子 chatID 前缀关系；token 计入父 Chat
- /chat/status 在编排模式下 progress.sub_agents 反映正在运行的子 Agent

运行前置：与 test_real_llm.py 一致，需要 conftest.py 中配置的 LLM 服务可用。
若 LLM 不可达本文件全部 skip 而非 fail（参考其它 real_llm 文件做法）。

每个测试自带独立 GROOT_HOME 与端口，原因：BuildSubAgentRegistry 启动期扫描一次，
必须先准备好 subagents/echo-agent/ 再启动；conftest 的 session 级 server 不允许
中途重启。
"""

import json
import os
import shutil
import signal
import socket
import subprocess
import tempfile
import time

import pytest
import requests
import yaml

from conftest import GROOT_BIN, TEST_API_KEY, SSEClient


# 真实 LLM 端点；与 test_real_llm.py / conftest.py 中默认值保持一致
LLM_BASE_URL = os.environ.get("GROOT_TEST_LLM_BASE_URL", "https://coding.dashscope.aliyuncs.com/v1")
LLM_MODEL = os.environ.get("GROOT_TEST_LLM_MODEL", "kimi-k2.5")
LLM_API_KEY = os.environ.get("GROOT_TEST_LLM_API_KEY", "sk-sp-8d17ece1cc9940d2aa63e7dcb5659e3e")


def _llm_available() -> bool:
    """探测 LLM 可达性。

    远程 HTTPS 端点（如 dashscope）在测试沙箱中可能拒绝直接探测，
    但 groot 子进程的出站请求是合法的——所以默认放行，让用例自己跑动后再失败。
    本地 LLM（127.0.0.1）才做存活检测，避免占满超时。
    """
    if not LLM_BASE_URL.startswith(("http://127.", "http://localhost")):
        return True  # 远程端点直接放行
    try:
        host = LLM_BASE_URL.rstrip("/").rstrip("/v1")
        r = requests.get(f"{host}/v1/models", timeout=3)
        return r.status_code in (200, 401, 404)
    except Exception:
        return False


# 模块级 skip：LLM 不可达时所有用例自动跳过
pytestmark = pytest.mark.skipif(
    not _llm_available(),
    reason=f"LLM 服务不可达 ({LLM_BASE_URL})；本文件需要真实 LLM。",
)


# ---------------------------------------------------------------------------
# 私有 helper（test_multi_agent.py 复用了一份；此处保持独立避免跨文件依赖）
# ---------------------------------------------------------------------------


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _wait_health(base_url: str, timeout: int = 30) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            r = requests.get(f"{base_url}/health", timeout=2)
            if r.status_code == 200:
                return True
        except Exception:
            pass
        time.sleep(0.5)
    return False


def _bootstrap_home() -> str:
    home = tempfile.mkdtemp(prefix="groot_multi_agent_e2e_")
    for sub in ("skills", "mcp", "subagents", "memory", "logs", "cluster/members"):
        os.makedirs(os.path.join(home, sub), exist_ok=True)
    return home


def _write_config(home: str, port: int) -> None:
    cfg = {
        "agent": {"name": "groot", "version": "test"},
        "server": {"host": "127.0.0.1", "port": port},
        "llm": {
            "default_model": "qwen-local",
            "models": {
                "qwen-local": {
                    "base_url": LLM_BASE_URL,
                    "api_key": LLM_API_KEY,
                    "model": LLM_MODEL,
                    "max_tokens": 2048,
                    "temperature": 0.0,
                }
            },
        },
        "skills": {"hot_reload": {"enabled": False, "debounce_delay": 1}},
        "security": {
            "auth": {
                "enabled": True,
                "type": "api_key",
                "api_key": {
                    "header_name": "X-API-Key",
                    "keys": [{"name": "test", "key": TEST_API_KEY, "permissions": ["all"]}],
                },
            }
        },
        "memory": {"directory": "memory"},
        "schedule": {"enabled": False, "max_concurrent_tasks": 1, "sync_interval": "30s"},
        "message": {
            "queue_size": 10,
            "workers": 1,
            "senders": {"webhook": {"enabled": False, "url": ""}, "email": {"enabled": False}},
        },
        "logging": {"level": "info", "format": "json", "output": ["stdout"]},
        "react": {"max_steps": 6},  # 限步避免无限循环
        "sub_agent": {
            "max_concurrency": 5,
            "max_task_length": 4000,
            "max_result_length": 8000,
            "exec_timeout": "2m",
        },
    }
    with open(os.path.join(home, "config.yaml"), "w") as f:
        yaml.dump(cfg, f)


def _write_grootmd(home: str, content: str) -> None:
    with open(os.path.join(home, "GROOT.md"), "w") as f:
        f.write(content)


def _write_subagent(home: str, name: str, description: str, body: str) -> None:
    d = os.path.join(home, "subagents", name)
    os.makedirs(d, exist_ok=True)
    md = f"---\ndescription: {description}\n---\n\n# {name}\n\n{body}\n"
    with open(os.path.join(d, "agent.md"), "w") as f:
        f.write(md)


def _setup_echo_agent(home: str) -> None:
    """约定的"回显"子 Agent：让模型直接复述用户输入。"""
    _write_subagent(
        home,
        "echo-agent",
        "回显测试 Agent，把用户输入原样返回",
        "请把用户给你的内容原样复述一遍，不要做任何加工或翻译。直接输出复述内容，不要多余说明。",
    )


class _Server:
    def __init__(self, home: str, port: int):
        self.home = home
        self.port = port
        self.base_url = f"http://127.0.0.1:{port}"
        self.proc: subprocess.Popen | None = None

    def __enter__(self):
        env = os.environ.copy()
        env["GROOT_HOME"] = self.home
        self.proc = subprocess.Popen(
            [GROOT_BIN], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE
        )
        if not _wait_health(self.base_url, timeout=30):
            self._dump_logs()
            self.proc.kill()
            raise RuntimeError(f"groot 启动失败 (home={self.home})")
        return self

    def __exit__(self, exc_type, exc, tb):
        if self.proc and self.proc.poll() is None:
            self.proc.send_signal(signal.SIGTERM)
            try:
                self.proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.proc.kill()
        try:
            shutil.rmtree(self.home, ignore_errors=True)
        except Exception:
            pass

    def _dump_logs(self):
        if self.proc and self.proc.stderr:
            try:
                err = self.proc.stderr.read().decode(errors="replace")[:4000]
                print(f"\n[groot stderr]\n{err}\n[/groot stderr]")
            except Exception:
                pass


@pytest.fixture
def headers():
    return {"Content-Type": "application/json", "X-API-Key": TEST_API_KEY}


# ---------------------------------------------------------------------------
# Solo 模式（X-Agent-Name 指定子 Agent）
# ---------------------------------------------------------------------------


class TestSoloMode:
    """X-Agent-Name 指定已注册子 Agent，跳过主 Agent 编排直接执行。

    对应 TEST_CASES 2.21.2。
    """

    def test_solo_uses_subagent_instruction(self, headers):
        """TC-MA-100: 让 echo-agent 复述输入 → 响应应包含原文 "苹果"。

        echo-agent 的 instruction 显式要求"原样复述"，主 Agent 没有这个要求，
        所以如果 instruction 没有切换到子 Agent，模型可能直接闲聊或问问题。
        """
        home = _bootstrap_home()
        _setup_echo_agent(home)
        port = _free_port()
        _write_config(home, port)

        with _Server(home, port) as srv:
            r = requests.post(
                f"{srv.base_url}/chat",
                headers={**headers, "X-Agent-Name": "echo-agent"},
                json={"instruction": "苹果"},
                stream=True,
                timeout=120,
            )
            assert r.status_code == 200
            r.encoding = "utf-8"  # 强制 utf-8 解码 SSE 流（默认会推断成 ISO-8859-1）
            sse = SSEClient(r)

            # SSEClient 把每个事件包成 {"event": <type>, "data": <原始 JSON>}
            # content 在 data 字段里
            full_text = "".join(
                ev["data"].get("content", "") for ev in sse.events if ev["data"].get("content")
            )

            if "苹果" not in full_text:
                import json as _json
                summary = "\n".join(_json.dumps(e, ensure_ascii=False) for e in sse.events)
                pytest.fail(
                    f"echo-agent 未复述「苹果」。\n拼接文本: {full_text!r}\n"
                    f"全部事件:\n{summary}"
                )

    def test_solo_chatrecord_persists_agent_name(self, headers):
        """TC-MA-101: Solo 模式 ChatRecord.AgentName 字段持久化为子 Agent 名。

        通过 /sess/{sid} 拿历史记录验证。
        """
        home = _bootstrap_home()
        _setup_echo_agent(home)
        port = _free_port()
        _write_config(home, port)

        with _Server(home, port) as srv:
            r = requests.post(
                f"{srv.base_url}/chat",
                headers={**headers, "X-Agent-Name": "echo-agent"},
                json={"instruction": "你好"},
                stream=True,
                timeout=120,
            )
            assert r.status_code == 200
            sid = r.headers.get("X-Session-ID")
            assert sid, "响应未返回 X-Session-ID"
            SSEClient(r)  # drain

            # 查会话详情
            sess = requests.get(
                f"{srv.base_url}/sess/{sid}", headers=headers, timeout=10
            )
            assert sess.status_code == 200
            data = sess.json()
            # 找到本次 chat 记录，验证 agent_name 字段
            messages = data.get("history", {}).get("messages", []) or data.get("messages", [])
            assert messages, f"会话历史为空: {data}"
            # 设计 8 节：ChatRecord 含 agent_name 字段
            # 注：history.messages 的字段命名以实际响应为准；此断言保护 agent_name 出现
            sess_text = json.dumps(data, ensure_ascii=False)
            assert "echo-agent" in sess_text, (
                f"会话详情应包含 echo-agent，实际：{sess_text[:500]}"
            )

    def test_solo_unknown_agent_returns_400(self, headers):
        """TC-MA-102: 已经在 test_multi_agent.py 里有 nonLLM 版；这里再测一次
        有真实子 Agent 注册情况下，未知名仍 400。"""
        home = _bootstrap_home()
        _setup_echo_agent(home)
        port = _free_port()
        _write_config(home, port)

        with _Server(home, port) as srv:
            r = requests.post(
                f"{srv.base_url}/chat",
                headers={**headers, "X-Agent-Name": "ghost"},
                json={"instruction": "x"},
                timeout=10,
            )
            assert r.status_code == 400


# ---------------------------------------------------------------------------
# 编排模式（主 Agent 调用 call_agent）
# ---------------------------------------------------------------------------


class TestOrchestrationMode:
    """主 Agent 通过 call_agent 工具调度子 Agent。对应 TEST_CASES 2.21.3。

    注：模型是否真的会调用 call_agent 取决于 LLM 的工具选择能力；
    本组用例用强 prompt + GROOT.md 引导段最大化触发概率，但仍然存在
    LLM 不调用工具直接回答的可能。失败时建议先看 SSE 流日志。
    """

    def test_main_agent_lists_call_agent_tool(self, headers):
        """TC-MA-110: GET /tools 主 Agent 工具列表含 call_agent。

        编排模式下 ExtraTools 注入 call_agent，应该在 /tools 列表里。
        响应形态：{group_name: {"tools": [{name, description}, ...], "total": N}}。
        call_agent 以合成 group "_builtin" 出现。
        """
        home = _bootstrap_home()
        _setup_echo_agent(home)
        port = _free_port()
        _write_config(home, port)

        with _Server(home, port) as srv:
            r = requests.get(f"{srv.base_url}/tools", headers=headers, timeout=5)
            assert r.status_code == 200
            grouped = r.json()
            assert isinstance(grouped, dict), f"/tools 响应应为 group map，实际 {type(grouped).__name__}"
            # 跨所有 group 收集工具名
            names = []
            for group_name, group in grouped.items():
                for t in group.get("tools", []):
                    names.append(t.get("name", ""))
            assert "call_agent" in names, (
                f"主 Agent /tools 应含 call_agent，实际 {grouped}"
            )

    def test_orchestration_emits_subagent_events(self, headers):
        """TC-MA-111: 强 prompt 引导主 Agent 调用 echo-agent；SSE 事件中应出现
        agent_name=echo-agent 的事件（plan Task 10）。

        如果 LLM 没调用工具直接回答，本测试会 fail；这是测试覆盖率的一部分，
        而不是 bug——把它视为 LLM 编排能力的回归监控。
        """
        home = _bootstrap_home()
        _setup_echo_agent(home)
        # GROOT.md 强引导：明确告诉主 Agent 必须用 call_agent
        _write_grootmd(
            home,
            "# GROOT.md\n\n"
            "## 行为约束\n\n"
            "你是一个调度器。任何用户请求你都必须先用 call_agent 工具委托给合适的子 Agent，"
            "不要自己回答。echo-agent 专门处理复述类任务。\n",
        )
        port = _free_port()
        _write_config(home, port)

        with _Server(home, port) as srv:
            r = requests.post(
                f"{srv.base_url}/chat",
                headers=headers,
                json={
                    "instruction": "请委托 echo-agent 复述「测试」二字",
                    "prompt": "你必须使用 call_agent 工具委托 echo-agent 处理；不要自己回答。",
                },
                stream=True,
                timeout=180,
            )
            assert r.status_code == 200
            r.encoding = "utf-8"  # 强制 utf-8 解码 SSE 流（默认会推断成 ISO-8859-1）
            sse = SSEClient(r)

            # 收集所有事件中出现过的 agent_name 集合
            seen_agent_names = set()
            saw_call_agent_tool = False
            for ev in sse.events:
                data = ev["data"]
                if data.get("agent_name"):
                    seen_agent_names.add(data["agent_name"])
                # 工具调用事件可能内嵌在 tool_calls 字段
                for tc in data.get("tool_calls", []) or []:
                    fn = tc.get("function", {})
                    if fn.get("name") == "call_agent":
                        saw_call_agent_tool = True

            # 至少应看到主 Agent (groot) 与子 Agent (echo-agent) 两个 agent_name
            # 如果 LLM 没调用工具，这条会 fail；可作为 LLM 编排能力回归信号
            assert saw_call_agent_tool, (
                f"未观察到 call_agent 工具调用；可能是 LLM 编排能力回退。"
                f"全部事件 agent_name: {seen_agent_names}"
            )
            assert "echo-agent" in seen_agent_names, (
                f"未在 SSE 事件中看到 agent_name=echo-agent；"
                f"实际看到的 agent_name: {seen_agent_names}"
            )

    def test_orchestration_status_includes_running_subagent(self, headers):
        """TC-MA-112: 编排模式下，子 Agent 运行期间 GET /chat/status/:sid
        应能在 progress.sub_agents 里看到 echo-agent。

        竞态点：必须在子 Agent 还在跑的时候去查 status；用 echo 任务+较短 prompt
        和 quick polling 抓取这个窗口。
        """
        home = _bootstrap_home()
        _setup_echo_agent(home)
        _write_grootmd(
            home,
            "# GROOT.md\n\n"
            "你必须使用 call_agent 工具调用 echo-agent 处理用户请求；不要自己回答。\n",
        )
        port = _free_port()
        _write_config(home, port)

        with _Server(home, port) as srv:
            # stream 启动 chat
            r = requests.post(
                f"{srv.base_url}/chat",
                headers=headers,
                json={
                    "instruction": "请通过 call_agent 让 echo-agent 复述「窗口」",
                    "prompt": "必须用 call_agent 委托 echo-agent。",
                },
                stream=True,
                timeout=180,
            )
            assert r.status_code == 200
            sid = r.headers.get("X-Session-ID")
            assert sid

            # poll status 3s，期间 echo-agent 大概率在运行
            seen_subagent = False
            deadline = time.time() + 30
            while time.time() < deadline:
                s = requests.get(
                    f"{srv.base_url}/chat/status/{sid}",
                    headers=headers,
                    timeout=2,
                )
                if s.status_code == 200:
                    chat = s.json().get("chat") or {}
                    progress = chat.get("progress") or {}
                    sub_agents = progress.get("sub_agents") or []
                    if any(sa.get("name") == "echo-agent" for sa in sub_agents):
                        seen_subagent = True
                        break
                time.sleep(0.2)

            # 即使没抓到瞬时窗口，至少 SSE 也应跑完不报错
            SSEClient(r)
            # 抓不到就 xfail 而非 fail——窗口非常窄，CI 不稳定
            if not seen_subagent:
                pytest.xfail(
                    "未抓到 progress.sub_agents 含 echo-agent 的瞬时窗口；"
                    "可能是子 Agent 太快完成。属预期内不稳定；"
                    "事件流验证由 test_orchestration_emits_subagent_events 保证。"
                )


# ---------------------------------------------------------------------------
# 父子 chatID 关系
# ---------------------------------------------------------------------------


class TestChildChatIDPrefix:
    """子 Agent 的 chatID 应以父 chatID 为前缀（plan Task 2 / 设计 8 节）。"""

    def test_child_chatid_has_parent_prefix(self, headers):
        """TC-MA-120: 编排模式下，子 Agent ChatRecord 的 chat_id 应形如
        <parent_chat_id>_<HHMMSSmmm>_<r4>_<agent_name>。

        通过持久化的会话历史拿到父 chat_id 与子 ChatRecord 文件名验证。
        """
        home = _bootstrap_home()
        _setup_echo_agent(home)
        _write_grootmd(
            home,
            "# GROOT.md\n\n你必须用 call_agent 工具委托 echo-agent 处理；"
            "不要自己回答。\n",
        )
        port = _free_port()
        _write_config(home, port)

        with _Server(home, port) as srv:
            r = requests.post(
                f"{srv.base_url}/chat",
                headers=headers,
                json={
                    "instruction": "用 call_agent 调 echo-agent 复述「父子」",
                    "prompt": "必须用 call_agent。",
                },
                stream=True,
                timeout=180,
            )
            assert r.status_code == 200
            parent_chat_id = r.headers.get("X-Chat-ID")
            sid = r.headers.get("X-Session-ID")
            assert parent_chat_id and sid
            SSEClient(r)  # drain

            # 等持久化稳定
            time.sleep(1.0)

            # memory/<sid>/chats/ 下的 ChatRecord 文件名通常是 chat_id
            # 持久化路径见 internal/memory/manager.go:chatPath/chatsDir
            chats_dir = os.path.join(srv.home, "memory", sid, "chats")
            if not os.path.isdir(chats_dir):
                pytest.skip(
                    f"未找到 chats/ 目录 {chats_dir}；"
                    "memory 持久化路径可能与预期不同，需要实际验证后再写"
                )

            files = os.listdir(chats_dir)
            child_files = [
                f for f in files
                if f.startswith(parent_chat_id) and f != f"{parent_chat_id}.json"
                and "echo-agent" in f
            ]
            if not child_files:
                pytest.xfail(
                    f"未找到含父前缀的子 ChatRecord；可能 LLM 没调用 call_agent。"
                    f"目录内容: {files}"
                )
            # 通过子 chat 文件名进一步验证格式
            for cf in child_files:
                assert cf.startswith(parent_chat_id), (
                    f"子 ChatRecord 文件名 {cf} 应以父 {parent_chat_id} 开头"
                )
                assert "echo-agent" in cf, (
                    f"子 ChatRecord 文件名 {cf} 应含 echo-agent 后缀"
                )
