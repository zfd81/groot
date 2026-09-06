"""CLI 命令系统测试（groot init / user reset / status / push / --help）。

所有会写配置或删用户的用例均使用独立临时 GROOT_HOME（tempfile.mkdtemp），
不碰共享 TEST_HOME：user reset 会清空用户表、init 会写配置文件。

源码依据：
- init（internal/cmd/init.go）：创建 skills/mcp/subagents/logs 目录，
  生成 config.yaml（含随机 security.auth.secret，权限 0600）、env.yaml（0600，
  全注释 → SQLite 本地模式）、GROOT.md；已存在的文件一律跳过不覆盖。
- user reset（internal/cmd/user.go）：删除用户表全部数据，-y 跳过确认；
  直接读写数据库（SQLite WAL 支持多进程），无需停服，/web/me 的 needs_setup
  实时反映用户表计数。
- status（internal/cmd/status.go）：GET /web/health；服务未运行时打印
  「未检测到运行中的 Groot 实例」并以退出码 0 结束。
- push（internal/cmd/push.go + internal/sync/sync.go）：SQLite 模式下
  ResourceRepo 为 nil → disabledSyncManager → 报 ErrSyncDisabled，退出码 1。

用例点：
- TC-CLI-101 groot init 生成 config.yaml（含非空 secret、0600）/env.yaml/GROOT.md/子目录
- TC-CLI-102 重复 init 跳过已有文件，secret 不被覆盖
- TC-CLI-103 groot --help 含 init/status/tail/push/pull/diff/user，不含 chat/schedule 子命令
- TC-CLI-104 groot status 未运行实例：提示未检测到，退出码 0
- TC-CLI-105 groot status 运行中实例（共享服务）：输出健康信息
- TC-CLI-106 groot push（SQLite 模式）：报「仅在 MySQL/PostgreSQL 模式下可用」，退出码 1
- TC-CLI-107 user reset 空表：提示「用户表为空」，退出码 0
- TC-CLI-108 独立实例全流程：空库 setup 弱密码 400 → setup 成功 →
  schedule 未启用时 /schedule 返回 503 schedule_unavailable →
  user reset -y 后 /web/me needs_setup 回到 true（无需重启服务）
"""
import os
import shutil
import socket
import stat
import subprocess
import tempfile
import time

import pytest
import requests
import yaml

from conftest import BASE_URL, GROOT_BIN, TEST_HOME, TEST_PORT

pytestmark = pytest.mark.skipif(
    not os.path.exists(GROOT_BIN),
    reason=f"groot 二进制不存在: {GROOT_BIN}（先执行 go build -o dist/groot ./cmd）",
)


def _run_groot(args, home, timeout=30):
    """以指定 GROOT_HOME 运行 groot 子命令，返回 CompletedProcess"""
    env = os.environ.copy()
    env["GROOT_HOME"] = home
    return subprocess.run(
        [GROOT_BIN] + args,
        env=env, capture_output=True, text=True, timeout=timeout,
    )


def _free_port() -> int:
    """向内核申请一个空闲端口（bind 后立即释放）"""
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _wait_health(port: int, timeout: int = 30) -> bool:
    """等待指定端口的 /web/health 就绪"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            if requests.get(f"http://127.0.0.1:{port}/web/health", timeout=2).status_code == 200:
                return True
        except requests.RequestException:
            pass
        time.sleep(0.5)
    return False


@pytest.fixture
def temp_home():
    """独立临时 GROOT_HOME，用后整体删除"""
    home = tempfile.mkdtemp(prefix="groot_cli_test_")
    yield home
    shutil.rmtree(home, ignore_errors=True)


class TestGrootInit:
    """groot init 初始化工作目录"""

    def test_init_creates_files(self, temp_home):
        """TC-CLI-101: 生成 config.yaml（非空 secret、0600）、env.yaml、GROOT.md、子目录"""
        result = _run_groot(["init"], temp_home)
        assert result.returncode == 0, result.stderr
        assert "初始化完成" in result.stdout

        # 子目录（运行时数据已入库，仅保留资源与日志目录）
        for d in ("skills", "mcp", "subagents", "logs"):
            assert os.path.isdir(os.path.join(temp_home, d)), f"缺少目录 {d}"

        # config.yaml：yaml 可解析，security.auth.secret 非空，权限 0600
        config_path = os.path.join(temp_home, "config.yaml")
        assert os.path.isfile(config_path)
        with open(config_path) as f:
            config = yaml.safe_load(f)
        secret = config["security"]["auth"]["secret"]
        assert secret and isinstance(secret, str), "secret 应为非空字符串"
        mode = stat.S_IMODE(os.stat(config_path).st_mode)
        assert mode == 0o600, f"config.yaml 权限应为 0600，实际: {oct(mode)}"

        # env.yaml（0600）与 GROOT.md
        env_path = os.path.join(temp_home, "env.yaml")
        assert os.path.isfile(env_path)
        assert stat.S_IMODE(os.stat(env_path).st_mode) == 0o600
        assert os.path.isfile(os.path.join(temp_home, "GROOT.md"))

    def test_init_idempotent_keeps_secret(self, temp_home):
        """TC-CLI-102: 重复 init 跳过已有文件，不覆盖已生成的 secret"""
        assert _run_groot(["init"], temp_home).returncode == 0
        config_path = os.path.join(temp_home, "config.yaml")
        with open(config_path) as f:
            secret_before = yaml.safe_load(f)["security"]["auth"]["secret"]

        result = _run_groot(["init"], temp_home)
        assert result.returncode == 0, result.stderr
        assert "已存在，跳过创建" in result.stdout

        with open(config_path) as f:
            secret_after = yaml.safe_load(f)["security"]["auth"]["secret"]
        assert secret_after == secret_before, "重复 init 不应改变已有 secret"

    def test_init_unknown_flag(self, temp_home):
        """init 传未知 flag 报错退出（退出码 1）"""
        result = _run_groot(["init", "--bogus"], temp_home)
        assert result.returncode == 1
        assert "unknown flag" in result.stderr


class TestGrootHelp:
    """groot --help 子命令清单"""

    def test_help_lists_subcommands(self, temp_home):
        """TC-CLI-103: 帮助含全部子命令，不含已移除的 chat/schedule"""
        result = _run_groot(["--help"], temp_home)
        assert result.returncode == 0
        out = result.stdout
        for sub in ("init", "status", "tail", "push", "pull", "diff", "user"):
            assert sub in out, f"帮助应包含子命令 {sub}"
        # Chat TUI 与调度 CLI 已移除，帮助不应再出现
        assert "chat" not in out, "帮助不应包含已移除的 chat 子命令"
        assert "schedule" not in out, "帮助不应包含已移除的 schedule 子命令"


class TestGrootStatus:
    """groot status 实例状态"""

    def test_status_not_running(self, temp_home):
        """TC-CLI-104: 目标端口无实例时打印未检测到提示，退出码 0"""
        port = _free_port()  # 申请后立即释放，该端口上无服务
        result = _run_groot(["status", "-p", str(port)], temp_home)
        assert result.returncode == 0, result.stderr
        assert "未检测到运行中的 Groot 实例" in result.stdout

    def test_status_running_shared(self, server, temp_home):
        """TC-CLI-105: 对共享测试服务输出健康信息（GROOT_HOME=TEST_HOME 读配置端口）"""
        result = _run_groot(["status"], TEST_HOME)
        assert result.returncode == 0, result.stderr
        assert "Groot 实例状态" in result.stdout
        assert "状态:" in result.stdout
        assert f"端口:      {TEST_PORT}" in result.stdout


class TestGrootPush:
    """groot push 在 SQLite 模式下不可用"""

    def test_push_sqlite_local_noop(self, temp_home):
        """TC-CLI-106: SQLite 模式下 push 走本地文件系统实现（空跑），
        扫描后报告无差异、退出码 0（见 repofactory：SQLite 用 resourcelocal，
        MySQL/PG 才用数据库实现做真正同步）"""
        assert _run_groot(["init"], temp_home).returncode == 0
        result = _run_groot(["push", "-y"], temp_home)
        assert result.returncode == 0, result.stderr
        assert "No differences" in result.stdout


class TestGrootUserReset:
    """groot user reset 重置 Web 用户"""

    def test_user_reset_empty_table(self, temp_home):
        """TC-CLI-107: 用户表为空时提示无需重置，退出码 0"""
        assert _run_groot(["init"], temp_home).returncode == 0
        result = _run_groot(["user", "reset", "-y"], temp_home)
        assert result.returncode == 0, result.stderr
        assert "用户表为空" in result.stdout

    def test_user_reset_full_flow(self, temp_home):
        """TC-CLI-108: 独立实例：弱密码 setup 400 → setup 成功 → schedule 未启用 503 →
        user reset -y → needs_setup 回到 true（reset 直接写库，无需重启服务）"""
        assert _run_groot(["init"], temp_home).returncode == 0

        port = _free_port()
        base = f"http://127.0.0.1:{port}"
        env = os.environ.copy()
        env["GROOT_HOME"] = temp_home
        proc = subprocess.Popen(
            [GROOT_BIN, "-p", str(port)],
            env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        try:
            assert _wait_health(port), "独立实例启动失败"

            # 空库：needs_setup 为 true
            assert requests.get(f"{base}/web/me", timeout=5).json()["needs_setup"] is True

            # 弱密码（<8 位）setup 400——该场景只在空库时可达（有用户先返回 409）
            r = requests.post(f"{base}/web/setup",
                              json={"username": "admin", "password": "short"}, timeout=5)
            assert r.status_code == 400, r.text

            # 正常 setup 成功，needs_setup 变 false
            r = requests.post(f"{base}/web/setup",
                              json={"username": "admin", "password": "cli-test-pass-1"},
                              timeout=5)
            assert r.status_code == 200, r.text
            assert requests.get(f"{base}/web/me", timeout=5).json()["needs_setup"] is False

            # init 模板 schedule 默认关闭 → scheduleMgr 未注册 → 503 schedule_unavailable
            # （共享测试服务 schedule.enabled=true 且单实例即 Leader，测不到此分支）
            s = requests.Session()
            r = s.post(f"{base}/web/login",
                       json={"username": "admin", "password": "cli-test-pass-1"}, timeout=5)
            assert r.status_code == 200, r.text
            r = s.get(f"{base}/schedule", timeout=5)
            assert r.status_code == 503, r.text
            assert r.json()["status"] == "schedule_unavailable"

            # user reset -y：直接删库中用户表，无需停服
            result = _run_groot(["user", "reset", "-y"], temp_home)
            assert result.returncode == 0, result.stderr
            assert "已删除" in result.stdout

            # needs_setup 实时回到 true（Count 每次读库）
            assert requests.get(f"{base}/web/me", timeout=5).json()["needs_setup"] is True
        finally:
            proc.terminate()
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill()
