"""
多 Agent 系统测试（v3.8 后）

设计：docs/superpowers/specs/2026-05-24-multi-agent-design.md
计划：docs/superpowers/plans/2026-05-28-multi-agent-implementation.md
用例点清单：tests/TEST_CASES.md 2.21 节

本文件覆盖**无需真实 LLM**的部分：
- 子 Agent 启动期注册行为
- /web/agents 列表接口（Web 登录 Cookie）
- /web/skills /web/tools + X-Agent-Name 路由（Web 登录 Cookie）
- /chat 的 X-Agent-Name 输入校验（unknown_agent → 400）
- /chat/status 含 sub_agents 字段
- groot init 子目录与 GROOT.md 调度引导段

涉及真实 LLM 的端到端用例（Solo 模式实际执行子 Agent / 编排模式调用 call_agent）
单独放在 test_multi_agent_real_llm.py，避免 mock LLM 也无法验证 prompt 拼接细节。

每个测试自带独立 GROOT_HOME 与端口，不复用 conftest 的 session 级 server fixture，
原因：BuildSubAgentRegistry 只在启动期扫描一次，必须先写好 subagents/ 再启动。
"""

# 兼容 Python 3.9 venv：让 `X | None` 注解惰性求值
from __future__ import annotations

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

from conftest import GROOT_BIN, TEST_AUTH_SECRET, _web_login, bootstrap_api_key, ensure_default_model


def _free_port() -> int:
    """挑一个空闲端口给独占 server 用，避免与默认 8080 / 已运行的 conftest server 冲突。"""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _wait_health(base_url: str, timeout: int = 30) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            # 健康检查唯一入口为 /web/health（免认证）
            r = requests.get(f"{base_url}/web/health", timeout=2)
            if r.status_code == 200:
                return True
        except Exception:
            pass
        time.sleep(0.5)
    return False


def _write_minimal_config(home: str, port: int) -> None:
    """写入测试用最小配置。

    注意：模型只存数据库（配置文件 llm 节已失效），模型由 ensure_default_model
    通过 /web/models 创建；memory.directory / skills.hot_reload 配置项已删除。
    """
    cfg = {
        "agent": {"name": "groot", "version": "test"},
        "server": {"host": "127.0.0.1", "port": port},
        # 认证始终开启：只需请求头名与 JWT 签名密钥；API Key 通过 Web 端点创建
        "security": {
            "auth": {
                "header_name": "X-API-Key",
                "secret": TEST_AUTH_SECRET,
            }
        },
        "memory": {"history_window": 20},
        "schedule": {"enabled": False, "max_concurrent_tasks": 1, "sync_interval": "30s"},
        "message": {
            "queue_size": 10,
            "workers": 1,
            "senders": {"webhook": {"enabled": False, "url": ""}, "email": {"enabled": False}},
        },
        "logging": {"level": "info", "format": "json", "output": ["stdout"]},
        "subagent": {
            "max_concurrency": 5,
            "max_task_length": 4000,
            "max_result_length": 8000,
            "exec_timeout": "5m",
        },
    }
    with open(os.path.join(home, "config.yaml"), "w") as f:
        yaml.dump(cfg, f)


def _write_subagent(home: str, name: str, description: str, body: str = "测试 Agent 正文") -> str:
    """在 home/subagents/<name>/ 下写入合法 agent.md，返回该目录路径。"""
    d = os.path.join(home, "subagents", name)
    os.makedirs(d, exist_ok=True)
    md = f"---\ndescription: {description}\n---\n\n# {name}\n\n{body}\n"
    with open(os.path.join(d, "agent.md"), "w") as f:
        f.write(md)
    return d


def _bootstrap_home(extra_dirs: list[str] | None = None) -> str:
    """创建 TempDir 作为独立 GROOT_HOME，预创建必备子目录。
    extra_dirs 用来插入额外目录（例如某些 fixture 测试要预置 broken subagent）。
    """
    home = tempfile.mkdtemp(prefix="groot_multi_agent_")
    for sub in ("skills", "mcp", "subagents", "logs"):
        os.makedirs(os.path.join(home, sub), exist_ok=True)
    for sub in extra_dirs or []:
        os.makedirs(os.path.join(home, sub), exist_ok=True)
    return home


class _Server:
    """启动一个独占 groot 进程，按指定 home + port 跑；上下文管理器自动清理。

    每个实例使用独立数据库，启动后自动：
    - 通过 Web 端点创建 API Key（self.headers，用于 /chat 等 API 端点）
    - 建立 Web 登录 Cookie Session（self.web，用于 /web/agents|skills|tools）
    - 确保模型库中有默认模型（TC-MA-020 等依赖：模型解析先于 agent 校验，
      空模型库时打不存在的 agent 会返回 invalid_model 而非 unknown_agent）
    """

    def __init__(self, home: str, port: int):
        self.home = home
        self.port = port
        self.base_url = f"http://127.0.0.1:{port}"
        self.proc: subprocess.Popen | None = None
        self.headers: dict | None = None
        self.web: requests.Session | None = None

    def __enter__(self):
        env = os.environ.copy()
        env["GROOT_HOME"] = self.home
        # 不依赖 -p 参数；config.yaml 里 server.port 已设置
        self.proc = subprocess.Popen(
            [GROOT_BIN],
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        if not _wait_health(self.base_url, timeout=30):
            self._dump_logs()
            self.proc.kill()
            raise RuntimeError(f"groot 启动失败 (home={self.home}, port={self.port})")
        # 独立数据库：为本实例创建 all 权限的 JWT API Key
        self.headers = {
            "Content-Type": "application/json",
            "X-API-Key": bootstrap_api_key(self.base_url, name="pytest-multi-agent"),
        }
        # /web/* 端点需要 Web 登录 Cookie（X-API-Key 无效）
        self.web = _web_login(self.base_url)
        # 模型只存数据库：确保空库中有默认模型
        ensure_default_model(self.base_url)
        return self

    def __exit__(self, exc_type, exc, tb):
        if self.proc and self.proc.poll() is None:
            self.proc.send_signal(signal.SIGTERM)
            try:
                self.proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.proc.kill()
        # 保留 home 目录方便排查；CI 上由 tmpdir 机制清理
        try:
            shutil.rmtree(self.home, ignore_errors=True)
        except Exception:
            pass

    def _dump_logs(self):
        """启动失败时把进程 stderr 打印出来，方便 CI 排查。"""
        if self.proc and self.proc.stderr:
            try:
                err = self.proc.stderr.read().decode(errors="replace")[:4000]
                print(f"\n[groot stderr]\n{err}\n[/groot stderr]")
            except Exception:
                pass


# ---------------------------------------------------------------------------
# 12.2.1 子 Agent 启动期注册（4 用例）
# ---------------------------------------------------------------------------


class TestSubAgentRegistration:
    """启动期扫描 subagents/ 的容错与正确性。对应 TEST_CASES 2.21.1。"""

    def test_no_subagents_dir_only_groot(self):
        """TC-MA-001: subagents/ 目录不存在 → 启动正常，/web/agents 仅返回 groot。

        覆盖 plan Task 7：scanSubAgentDirs 对缺失根目录静默返回空切片。
        """
        home = tempfile.mkdtemp(prefix="groot_no_subagents_")
        # 故意只建 skills / mcp 等基础目录，不建 subagents
        for sub in ("skills", "mcp", "logs"):
            os.makedirs(os.path.join(home, sub), exist_ok=True)
        port = _free_port()
        _write_minimal_config(home, port)

        with _Server(home, port) as srv:
            r = srv.web.get(f"{srv.base_url}/web/agents", timeout=5)
            assert r.status_code == 200
            data = r.json()
            names = [a["name"] for a in data["agents"]]
            assert names == ["groot"], f"应只返回 groot，实际 {names}"

    def test_missing_description_skipped(self):
        """TC-MA-002: agent.md 缺 description → 启动跳过该目录，其它正常加载。"""
        home = _bootstrap_home()
        # 合法 Agent
        _write_subagent(home, "good-agent", "合法 Agent")
        # 非法：缺 description
        bad_dir = os.path.join(home, "subagents", "no-desc")
        os.makedirs(bad_dir, exist_ok=True)
        with open(os.path.join(bad_dir, "agent.md"), "w") as f:
            f.write("---\nmodel: gpt-4\n---\n\n正文\n")

        port = _free_port()
        _write_minimal_config(home, port)

        with _Server(home, port) as srv:
            r = srv.web.get(f"{srv.base_url}/web/agents", timeout=5)
            assert r.status_code == 200
            names = [a["name"] for a in r.json()["agents"]]
            assert "good-agent" in names
            assert "no-desc" not in names

    def test_missing_agent_md_skipped(self):
        """TC-MA-003: agent.md 缺失 → 启动跳过该目录。"""
        home = _bootstrap_home()
        _write_subagent(home, "good-agent", "合法 Agent")
        # 非法：建目录但不放 agent.md
        os.makedirs(os.path.join(home, "subagents", "empty-dir"), exist_ok=True)

        port = _free_port()
        _write_minimal_config(home, port)

        with _Server(home, port) as srv:
            r = srv.web.get(f"{srv.base_url}/web/agents", timeout=5)
            names = [a["name"] for a in r.json()["agents"]]
            assert "good-agent" in names
            assert "empty-dir" not in names

    def test_directory_named_groot_skipped(self):
        """TC-MA-004: 子目录名为 "groot"（与主 Agent 同名）→ 跳过，日志 ERROR。

        覆盖 plan Task 7：MainAgentName 保留检查。
        """
        home = _bootstrap_home()
        _write_subagent(home, "real-agent", "正常的 Agent")
        # 故意造一个名字冲突的
        _write_subagent(home, "groot", "冒名顶替")

        port = _free_port()
        _write_minimal_config(home, port)

        with _Server(home, port) as srv:
            r = srv.web.get(f"{srv.base_url}/web/agents", timeout=5)
            data = r.json()
            # groot 仅出现一次（主 Agent），且主 Agent 的 description 不是 "冒名顶替"
            groot_entries = [a for a in data["agents"] if a["name"] == "groot"]
            assert len(groot_entries) == 1, f"groot 应只出现一次，实际 {groot_entries}"
            assert groot_entries[0]["description"] != "冒名顶替"


# ---------------------------------------------------------------------------
# 12.2.2 /agents API（2 用例）
# ---------------------------------------------------------------------------


class TestAgentsAPI:
    """GET /agents 接口。对应 TEST_CASES 2.21.4。"""

    def test_groot_first_then_subagents_alphabetical(self):
        """TC-MA-010: groot 首位 + 子 Agent 按字典序。"""
        home = _bootstrap_home()
        # 故意按非字典序写入；返回应自动排序
        _write_subagent(home, "zeta-agent", "Z 开头")
        _write_subagent(home, "alpha-agent", "A 开头")
        _write_subagent(home, "mid-agent", "M 开头")

        port = _free_port()
        _write_minimal_config(home, port)

        with _Server(home, port) as srv:
            r = srv.web.get(f"{srv.base_url}/web/agents", timeout=5)
            assert r.status_code == 200
            names = [a["name"] for a in r.json()["agents"]]
            assert names[0] == "groot"
            # 字典序验证（plan Task 14 规定）
            sub = names[1:]
            assert sub == sorted(sub), f"子 Agent 应按字典序，实际 {sub}"

    def test_each_entry_has_required_fields(self):
        """TC-MA-011: 每条 Agent 含 name / description / skills 字段。"""
        home = _bootstrap_home()
        _write_subagent(home, "db-agent", "数据库专家")
        port = _free_port()
        _write_minimal_config(home, port)

        with _Server(home, port) as srv:
            r = srv.web.get(f"{srv.base_url}/web/agents", timeout=5)
            data = r.json()
            for a in data["agents"]:
                assert "name" in a
                assert "description" in a
                assert "skills" in a
                assert isinstance(a["skills"], list)


# ---------------------------------------------------------------------------
# 12.2.3 X-Agent-Name 输入校验（3 用例）
# ---------------------------------------------------------------------------


class TestXAgentNameValidation:
    """/chat /web/skills /web/tools 的 X-Agent-Name 输入校验。

    本组用例**只验证错误路径**（unknown_agent → 400 / 主 Agent 等价空），
    不真实触发 LLM 推理。对应 TEST_CASES 2.21.2 / 2.21.4。
    """

    def test_chat_unknown_agent_returns_400(self):
        """TC-MA-020: POST /chat + X-Agent-Name=ghost → 400 unknown_agent。

        注意：chat.go 先解析模型再校验 agent，_Server 已经保证模型库中有
        默认模型，因此此处应得到 unknown_agent 而非 invalid_model。
        """
        home = _bootstrap_home()
        port = _free_port()
        _write_minimal_config(home, port)

        with _Server(home, port) as srv:
            h = {**srv.headers, "X-Agent-Name": "ghost-agent"}
            r = requests.post(
                f"{srv.base_url}/chat",
                headers=h,
                json={"instruction": "x"},
                timeout=5,
            )
            assert r.status_code == 400
            assert "unknown_agent" in r.text or "Unknown" in r.text

    def test_skills_unknown_agent_returns_400(self):
        """TC-MA-021: GET /web/skills + X-Agent-Name=ghost → 400 unknown_agent。"""
        home = _bootstrap_home()
        port = _free_port()
        _write_minimal_config(home, port)

        with _Server(home, port) as srv:
            r = srv.web.get(
                f"{srv.base_url}/web/skills",
                headers={"X-Agent-Name": "ghost-agent"},
                timeout=5,
            )
            assert r.status_code == 400

    def test_tools_unknown_agent_returns_400(self):
        """TC-MA-022: GET /web/tools + X-Agent-Name=ghost → 400 unknown_agent。"""
        home = _bootstrap_home()
        port = _free_port()
        _write_minimal_config(home, port)

        with _Server(home, port) as srv:
            r = srv.web.get(
                f"{srv.base_url}/web/tools",
                headers={"X-Agent-Name": "ghost-agent"},
                timeout=5,
            )
            assert r.status_code == 400


# ---------------------------------------------------------------------------
# 12.2.4 主 Agent 等价（2 用例）
# ---------------------------------------------------------------------------


class TestMainAgentEquivalence:
    """X-Agent-Name=groot 等价于不传 header（plan Task 13）。"""

    def test_skills_groot_header_equals_omit(self):
        """TC-MA-030: /web/skills + X-Agent-Name=groot vs 不传 → 响应相同。"""
        home = _bootstrap_home()
        _write_subagent(home, "db-agent", "数据库专家")
        port = _free_port()
        _write_minimal_config(home, port)

        with _Server(home, port) as srv:
            r1 = srv.web.get(f"{srv.base_url}/web/skills", timeout=5)
            r2 = srv.web.get(
                f"{srv.base_url}/web/skills",
                headers={"X-Agent-Name": "groot"},
                timeout=5,
            )
            assert r1.status_code == 200 and r2.status_code == 200
            # skills 列表语义上相同；忽略字段顺序
            assert r1.json() == r2.json()

    def test_tools_groot_header_equals_omit(self):
        """TC-MA-031: /web/tools + X-Agent-Name=groot vs 不传 → 响应相同。"""
        home = _bootstrap_home()
        port = _free_port()
        _write_minimal_config(home, port)

        with _Server(home, port) as srv:
            r1 = srv.web.get(f"{srv.base_url}/web/tools", timeout=5)
            r2 = srv.web.get(
                f"{srv.base_url}/web/tools",
                headers={"X-Agent-Name": "groot"},
                timeout=5,
            )
            assert r1.status_code == 200 and r2.status_code == 200
            assert r1.json() == r2.json()


# ---------------------------------------------------------------------------
# 12.2.5 子 Agent 路由（2 用例）
# ---------------------------------------------------------------------------


class TestSubAgentRouting:
    """X-Agent-Name=<已注册子 Agent> 时 /web/skills /web/tools 走子 Agent 后端。"""

    def test_skills_subagent_returns_subagent_skills(self):
        """TC-MA-040: 写一个 SKILL.md 进 subagents/db-agent/skills/，
        然后 GET /web/skills + X-Agent-Name=db-agent 应能返回它。
        """
        home = _bootstrap_home()
        _write_subagent(home, "db-agent", "数据库专家")
        sub_skills_dir = os.path.join(home, "subagents", "db-agent", "skills", "sql-review")
        os.makedirs(sub_skills_dir, exist_ok=True)
        with open(os.path.join(sub_skills_dir, "SKILL.md"), "w") as f:
            f.write(
                "---\n"
                "name: sql-review\n"
                'description: "审查 SQL 是否高效"\n'
                "---\n\n# sql-review\n"
            )
        port = _free_port()
        _write_minimal_config(home, port)

        with _Server(home, port) as srv:
            r = srv.web.get(
                f"{srv.base_url}/web/skills",
                headers={"X-Agent-Name": "db-agent"},
                timeout=5,
            )
            assert r.status_code == 200
            skills = r.json().get("skills", [])
            names = [s.get("name") for s in skills]
            assert "sql-review" in names, f"未找到 sql-review，实际 {names}"

    def test_tools_subagent_no_mcp_returns_empty(self):
        """TC-MA-041: 子 Agent 没有 mcp/ 目录时 /web/tools 返回空 group map（不是 500，
        也不挂 call_agent —— Solo 模式不挂载内置工具）。"""
        home = _bootstrap_home()
        _write_subagent(home, "minimal-agent", "最小化 Agent，无 mcp")
        port = _free_port()
        _write_minimal_config(home, port)

        with _Server(home, port) as srv:
            r = srv.web.get(
                f"{srv.base_url}/web/tools",
                headers={"X-Agent-Name": "minimal-agent"},
                timeout=5,
            )
            assert r.status_code == 200
            grouped = r.json()
            # 形态：{group: {tools: [...]}}；子 Agent 无 MCP → 空 map；
            # 不应包含 call_agent（Solo 模式不挂载）
            assert isinstance(grouped, dict)
            for _, g in grouped.items():
                names = [t.get("name", "") for t in g.get("tools", [])]
                assert "call_agent" not in names, (
                    f"Solo 模式 /tools 不应含 call_agent，实际 {grouped}"
                )


# ---------------------------------------------------------------------------
# 12.2.6 /chat/status 含 sub_agents（1 用例 - 不需 LLM）
# ---------------------------------------------------------------------------


class TestStatusSubAgents:
    """/chat/status 响应字段验证。LLM 真实跑动并发场景在 real_llm 文件。"""

    def test_status_idle_session_has_chat_null(self):
        """TC-MA-050: 不存在的 session → status=idle, chat=null（plan Task 19）。"""
        home = _bootstrap_home()
        port = _free_port()
        _write_minimal_config(home, port)

        with _Server(home, port) as srv:
            r = requests.get(
                f"{srv.base_url}/chat/status/never-existed",
                headers=srv.headers,
                timeout=5,
            )
            assert r.status_code == 200
            data = r.json()
            assert data["status"] == "idle"
            assert data["chat"] is None


# ---------------------------------------------------------------------------
# 12.2.7 groot init（3 用例 - 不启 server）
# ---------------------------------------------------------------------------


class TestInit:
    """groot init 子命令行为，对应 TEST_CASES 2.21.7。直接跑 CLI，不启 server。"""

    def test_init_creates_subagents_dir(self):
        """TC-MA-060: 全新目录 init 后包含 subagents/。"""
        home = tempfile.mkdtemp(prefix="groot_init_")
        try:
            r = subprocess.run(
                [GROOT_BIN, "init"],
                env={**os.environ, "GROOT_HOME": home, "HOME": home},
                capture_output=True,
                timeout=10,
            )
            assert r.returncode == 0, f"init 失败: {r.stderr.decode()}"
            assert os.path.isdir(os.path.join(home, "subagents"))
        finally:
            shutil.rmtree(home, ignore_errors=True)

    def test_init_writes_grootmd_with_scheduling_hint(self):
        """TC-MA-061: 全新目录 init 后 GROOT.md 含子 Agent 调度引导关键词。"""
        home = tempfile.mkdtemp(prefix="groot_init_")
        try:
            subprocess.run(
                [GROOT_BIN, "init"],
                env={**os.environ, "GROOT_HOME": home, "HOME": home},
                capture_output=True,
                timeout=10,
            )
            md_path = os.path.join(home, "GROOT.md")
            assert os.path.exists(md_path)
            content = open(md_path).read()
            for keyword in ("子 Agent 调度", "call_agent", "按需调用", "逐个调用", "明确传参", "附件引用"):
                assert keyword in content, f"缺关键词 {keyword!r}"
        finally:
            shutil.rmtree(home, ignore_errors=True)

    def test_init_preserves_existing_grootmd(self):
        """TC-MA-062: 已有 GROOT.md → init 跳过不覆盖。"""
        home = tempfile.mkdtemp(prefix="groot_init_")
        try:
            os.makedirs(home, exist_ok=True)
            custom = "# 我自己的 GROOT.md\n请别覆盖我。\n"
            md_path = os.path.join(home, "GROOT.md")
            with open(md_path, "w") as f:
                f.write(custom)

            subprocess.run(
                [GROOT_BIN, "init"],
                env={**os.environ, "GROOT_HOME": home, "HOME": home},
                capture_output=True,
                timeout=10,
            )
            assert open(md_path).read() == custom
        finally:
            shutil.rmtree(home, ignore_errors=True)
