"""
集群管理系统测试

测试覆盖：
- 单实例启动，自动成为 Leader
- 双实例启动，第二个成为 Follower
- Leader 被 kill（宕机），Follower 提升为 Leader
- Leader 优雅退出，Follower 提升为 Leader
- 多实例同时运行，恰好一个 Leader
- 故障恢复实例重新注册为 Follower
- Leader 心跳持续刷新 heartbeat_at

成员注册已入库：多实例共享同一 GROOT_HOME 时共享 {GROOT_HOME}/groot.db 的
cluster_members 表（reg_id/role/host/port/pid/heartbeat_at/created_at，时间戳
为毫秒；见 internal/repo/memberdb/member.go 与 internal/db/migrate.go）。
SQLite 以 WAL + busy_timeout=5000 打开（internal/db/db.go），支持同机多进程
读写，因此共享 GROOT_HOME 的多实例集群在 SQLite 下是被支持的。
"""

import pytest
import os
import time
import signal
import sqlite3
import subprocess
import requests

from conftest import GROOT_BIN, TEST_AUTH_SECRET

# 集群测试使用独立的 GROOT_HOME，避免与默认测试 server fixture 冲突
CLUSTER_HOME = os.environ.get("GROOT_TEST_CLUSTER_HOME", "/tmp/groot_cluster_test")

# 共享数据库文件（多实例共享同一 GROOT_HOME → 同一 groot.db）
CLUSTER_DB = os.path.join(CLUSTER_HOME, "groot.db")

# 集群实例专用端口：避开 8080 等常用端口，绝不与开发服务或共享测试服务器冲突
PORT_A = int(os.environ.get("GROOT_TEST_CLUSTER_PORT_A", "18201"))
PORT_B = int(os.environ.get("GROOT_TEST_CLUSTER_PORT_B", "18202"))
PORT_C = int(os.environ.get("GROOT_TEST_CLUSTER_PORT_C", "18203"))

# 本模块启动过的实例注册表：清理时只杀自己启动的进程，
# 禁止全局 pkill groot（会误杀共享测试服务器与开发环境服务）
_SPAWNED: list = []


def _create_cluster_config(home: str, port: int) -> None:
    """创建测试用的 config.yaml

    注意：模型只存数据库（配置文件 llm 节已失效）；集群用例只测选举/心跳，
    不发 /chat，无需创建模型。memory.directory / skills.hot_reload 配置已删除。
    """
    os.makedirs(home, exist_ok=True)
    for d in ["skills", "mcp", "logs"]:
        os.makedirs(os.path.join(home, d), exist_ok=True)

    import yaml
    config = {
        "agent": {"name": "groot", "version": "1.0.0"},
        "server": {"host": "127.0.0.1", "port": port},
        # 认证始终开启（新 JWT API Key 机制）；集群用例只访问免认证端点
        "security": {"auth": {"header_name": "X-API-Key", "secret": TEST_AUTH_SECRET}},
        "memory": {"history_window": 20},
        "schedule": {"enabled": False, "max_concurrent_tasks": 10, "sync_interval": "30s"},
        "message": {
            "queue_size": 10, "workers": 1,
            "senders": {"webhook": {"enabled": False}, "email": {"enabled": False}},
        },
        "logging": {"level": "info", "format": "console", "output": ["stdout"]},
        "attachment": {"max_size": 50, "max_total_size": 100, "max_count": 10,
                       "allowed_types": ["txt"]},
    }
    with open(os.path.join(home, "config.yaml"), "w") as f:
        yaml.dump(config, f)


def _start_groot(port: int, home: str = CLUSTER_HOME) -> subprocess.Popen:
    """启动一个 groot 实例，返回 Popen 进程。若进程启动后立即退出则抛出 RuntimeError。"""
    _create_cluster_config(home, port)
    env = os.environ.copy()
    env["GROOT_HOME"] = home
    proc = subprocess.Popen(
        [GROOT_BIN, "-p", str(port)],
        env=env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    # 等待一小段时间确认进程存活（端口被占用时会立即退出）
    time.sleep(0.5)
    if proc.poll() is not None:
        raise RuntimeError(
            f"groot 进程在端口 {port} 上启动后立即退出 (exit code={proc.returncode})，"
            f"可能端口被占用"
        )
    _SPAWNED.append(proc)
    return proc


def _wait_health(port: int, timeout: int = 20) -> bool:
    """等待实例健康检查通过（健康检查唯一入口 /web/health，免认证）"""
    start = time.time()
    while time.time() - start < timeout:
        try:
            r = requests.get(f"http://127.0.0.1:{port}/web/health", timeout=1)
            if r.status_code == 200:
                return True
        except Exception:
            pass
        time.sleep(0.5)
    return False


def _get_members(home: str = CLUSTER_HOME) -> dict:
    """查询共享数据库中的集群成员，返回
    {reg_id: {'role': str, 'host': str, 'port': int, 'pid': int, 'heartbeat_at': int}}

    列名见 internal/repo/memberdb/member.go；heartbeat_at 为毫秒时间戳。
    """
    db_path = os.path.join(home, "groot.db")
    if not os.path.exists(db_path):
        return {}
    try:
        conn = sqlite3.connect(db_path, timeout=5)
        try:
            rows = conn.execute(
                "SELECT reg_id, role, host, port, pid, heartbeat_at FROM cluster_members"
            ).fetchall()
        finally:
            conn.close()
    except sqlite3.Error:
        return {}
    result = {}
    for reg_id, role, host, port, pid, heartbeat_at in rows:
        result[reg_id] = {
            "role": role,
            "host": host,
            "port": int(port),
            "pid": int(pid),
            "heartbeat_at": int(heartbeat_at),
        }
    return result


def _cleanup():
    """清理测试环境：只杀成员表登记的 PID 与本模块启动的实例，不做全局 pkill"""
    import shutil
    for info in _get_members().values():
        try:
            os.kill(info["pid"], signal.SIGTERM)
        except OSError:
            pass
    for proc in _SPAWNED:
        if proc.poll() is None:
            proc.terminate()
    _SPAWNED.clear()
    time.sleep(0.5)
    if os.path.exists(CLUSTER_HOME):
        shutil.rmtree(CLUSTER_HOME, ignore_errors=True)


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

@pytest.fixture(autouse=True)
def cluster_setup():
    """每个测试前后清理集群环境"""
    _cleanup()
    os.makedirs(CLUSTER_HOME, exist_ok=True)
    yield
    # 杀掉所有残留的 groot 进程（通过成员表中的 PID）
    members = _get_members()
    for info in members.values():
        try:
            os.kill(info["pid"], signal.SIGTERM)
        except OSError:
            pass
    time.sleep(1)
    _cleanup()


# ---------------------------------------------------------------------------
# 测试用例
# ---------------------------------------------------------------------------

class TestSingleInstance:
    """单实例场景"""

    def test_single_instance_becomes_leader(self):
        """启动一个实例，应自动成为 Leader"""
        proc = _start_groot(PORT_A)
        try:
            assert _wait_health(PORT_A), "实例未能在 20s 内启动"

            members = _get_members()
            assert len(members) == 1, f"应恰好 1 条成员记录，实际: {len(members)}"

            info = list(members.values())[0]
            assert info["role"] == "leader", f"应为 leader，实际: {info['role']}"
            assert info["port"] == PORT_A
            assert info["host"] == "127.0.0.1"
        finally:
            proc.send_signal(signal.SIGTERM)
            proc.wait(timeout=10)

    def test_registration_record_format(self):
        """验证成员记录格式（reg_id/role/pid）"""
        proc = _start_groot(PORT_A)
        try:
            assert _wait_health(PORT_A), "实例未能在 20s 内启动"

            members = _get_members()
            reg_id = list(members.keys())[0]
            info = members[reg_id]

            # 注册编号应为 17 位数字 (YYYYMMDDHHMMSSmmm)
            assert len(reg_id) == 17, f"注册编号应为 17 位，实际: {len(reg_id)}"
            assert reg_id.isdigit(), f"注册编号应为纯数字，实际: {reg_id}"

            # 角色应为 leader/follower
            assert info["role"] in ("leader", "follower")

            # PID 应为正整数
            assert info["pid"] > 0
        finally:
            proc.send_signal(signal.SIGTERM)
            proc.wait(timeout=10)


class TestDualInstance:
    """双实例场景（共享 GROOT_HOME → 共享 groot.db 的成员表）"""

    def test_second_instance_becomes_follower(self):
        """第二个实例启动后应成为 Follower"""
        p1 = _start_groot(PORT_A)
        try:
            assert _wait_health(PORT_A), "实例1 未能在 20s 内启动"
            time.sleep(0.1)  # 确保注册编号不同

            p2 = _start_groot(PORT_B)
            try:
                assert _wait_health(PORT_B), "实例2 未能在 20s 内启动"
                time.sleep(1)

                members = _get_members()
                assert len(members) == 2, f"应恰好 2 条成员记录，实际: {len(members)}"

                roles = [info["role"] for info in members.values()]
                assert "leader" in roles, f"缺少 leader，角色列表: {roles}"
                assert "follower" in roles, f"缺少 follower，角色列表: {roles}"
                assert roles.count("leader") == 1, f"应有恰好 1 个 leader，实际: {roles}"
            finally:
                p2.send_signal(signal.SIGTERM)
                p2.wait(timeout=10)
        finally:
            p1.send_signal(signal.SIGTERM)
            p1.wait(timeout=10)

    def test_first_instance_is_leader(self):
        """先启动的实例注册编号更小，应为 Leader"""
        p1 = _start_groot(PORT_A)
        try:
            assert _wait_health(PORT_A)
            time.sleep(0.1)

            p2 = _start_groot(PORT_B)
            try:
                assert _wait_health(PORT_B)
                time.sleep(1)

                members = _get_members()
                # 找出注册编号最小的实例
                sorted_ids = sorted(members.keys())
                leader_info = members[sorted_ids[0]]
                assert leader_info["role"] == "leader", (
                    f"注册编号最小的实例应为 leader，实际: {leader_info['role']}"
                )
            finally:
                p2.send_signal(signal.SIGTERM)
                p2.wait(timeout=10)
        finally:
            p1.send_signal(signal.SIGTERM)
            p1.wait(timeout=10)


class TestFailover:
    """故障转移场景"""

    def test_leader_killed_follower_promotes(self):
        """杀掉 Leader 进程，Follower 应在超时后提升为 Leader"""
        p1 = _start_groot(PORT_A)
        p2 = _start_groot(PORT_B)
        try:
            assert _wait_health(PORT_A), "实例1 未启动"
            assert _wait_health(PORT_B), "实例2 未启动"
            time.sleep(0.5)

            # 确认 p1 是 leader
            members = _get_members()
            sorted_ids = sorted(members.keys())
            leader_id = sorted_ids[0]
            assert members[leader_id]["role"] == "leader"

            # 杀掉 Leader（模拟宕机，不用 SIGTERM）
            leader_port = members[leader_id]["port"]
            leader_proc = p1 if leader_port == PORT_A else p2
            os.kill(leader_proc.pid, signal.SIGKILL)
            leader_proc.wait(timeout=5)

            # 等待心跳超时 (7s) + 一个心跳周期 (3s) + 余量
            time.sleep(12)

            # Follower 应提升为 Leader（提升后 RemoveExpired 清掉宕机成员记录）
            members = _get_members()
            assert len(members) == 1, (
                f"Leader 被杀后应只剩 1 条成员记录 (Follower 提升为 Leader)，实际: {len(members)}"
            )
            survivor = list(members.values())[0]
            assert survivor["role"] == "leader", (
                f"存活实例应提升为 leader，实际: {survivor['role']}"
            )
        finally:
            for p in [p1, p2]:
                try:
                    p.send_signal(signal.SIGTERM)
                    p.wait(timeout=5)
                except Exception:
                    pass

    def test_leader_graceful_shutdown_follower_promotes(self):
        """Leader 优雅退出后，Follower 应提升为 Leader"""
        p1 = _start_groot(PORT_A)
        p2 = _start_groot(PORT_B)
        try:
            assert _wait_health(PORT_A), "实例1 未启动"
            assert _wait_health(PORT_B), "实例2 未启动"
            time.sleep(0.5)

            members = _get_members()
            sorted_ids = sorted(members.keys())
            leader_id = sorted_ids[0]
            leader_port = members[leader_id]["port"]

            # Leader 优雅退出（Leave 会删除自己的成员记录）
            leader_proc = p1 if leader_port == PORT_A else p2
            leader_proc.send_signal(signal.SIGTERM)
            leader_proc.wait(timeout=10)

            # 等待 Follower 检测到 Leader 记录消失并提升
            time.sleep(8)

            members = _get_members()
            assert len(members) == 1, (
                f"Leader 退出后应只剩 1 条成员记录，实际: {len(members)}"
            )
            survivor = list(members.values())[0]
            assert survivor["role"] == "leader", (
                f"存活实例应提升为 leader，实际: {survivor['role']}"
            )
        finally:
            for p in [p1, p2]:
                try:
                    p.send_signal(signal.SIGTERM)
                    p.wait(timeout=5)
                except Exception:
                    pass


class TestMultipleInstances:
    """多实例场景"""

    def test_three_instances_exactly_one_leader(self):
        """3 个实例共享 GROOT_HOME，恰好 1 个 Leader"""
        procs = []
        try:
            for port in [PORT_A, PORT_B, PORT_C]:
                procs.append(_start_groot(port))
                time.sleep(0.1)

            for port in [PORT_A, PORT_B, PORT_C]:
                assert _wait_health(port), f"实例 {port} 未启动"

            time.sleep(1)

            members = _get_members()
            assert len(members) == 3, f"应有 3 条成员记录，实际: {len(members)}"

            roles = [info["role"] for info in members.values()]
            assert roles.count("leader") == 1, (
                f"应恰好 1 个 leader，实际: {roles}"
            )
            assert roles.count("follower") == 2, (
                f"应恰好 2 个 follower，实际: {roles}"
            )
        finally:
            for p in procs:
                try:
                    p.send_signal(signal.SIGTERM)
                    p.wait(timeout=5)
                except Exception:
                    pass


class TestCrashRecovery:
    """故障恢复场景"""

    def test_restarted_old_leader_becomes_follower(self):
        """旧 Leader 被杀后重新启动，应成为 Follower"""
        p1 = _start_groot(PORT_A)
        p2 = _start_groot(PORT_B)
        try:
            assert _wait_health(PORT_A), "实例1 未启动"
            assert _wait_health(PORT_B), "实例2 未启动"
            time.sleep(0.5)

            # 确认 leader
            members = _get_members()
            sorted_ids = sorted(members.keys())
            leader_port = members[sorted_ids[0]]["port"]

            # 杀掉 Leader
            leader_proc = p1 if leader_port == PORT_A else p2
            os.kill(leader_proc.pid, signal.SIGKILL)
            leader_proc.wait(timeout=5)

            # 等待 Follower 提升
            time.sleep(12)

            # 确认存活实例已是 Leader
            members = _get_members()
            assert len(members) == 1
            assert list(members.values())[0]["role"] == "leader"

            # 旧 Leader 重新启动（使用新端口）
            p3 = _start_groot(PORT_C)
            try:
                assert _wait_health(PORT_C), "重启的实例未启动"
                time.sleep(2)

                members = _get_members()
                assert len(members) >= 2, f"重启后应有至少 2 条成员记录，实际: {len(members)}"

                # 重启的实例应为 Follower (注册编号最大)
                sorted_ids = sorted(members.keys())
                newest_id = sorted_ids[-1]
                assert members[newest_id]["role"] == "follower", (
                    f"重启的旧 Leader 应为 follower，实际: {members[newest_id]['role']}"
                )
            finally:
                p3.send_signal(signal.SIGTERM)
                p3.wait(timeout=5)
        finally:
            for p in [p1, p2]:
                try:
                    p.send_signal(signal.SIGTERM)
                    p.wait(timeout=5)
                except Exception:
                    pass


class TestHeartbeatUpdate:
    """心跳时间戳更新"""

    def test_leader_heartbeat_at_updates(self):
        """Leader 心跳应持续刷新成员表的 heartbeat_at 列"""
        proc = _start_groot(PORT_A)
        try:
            assert _wait_health(PORT_A)

            members = _get_members()
            assert len(members) == 1
            reg_id = list(members.keys())[0]
            hb1 = members[reg_id]["heartbeat_at"]

            # 等待超过一个心跳周期 (3s)
            time.sleep(5)

            # heartbeat_at 应已更新（毫秒时间戳增大）
            members2 = _get_members()
            assert reg_id in members2, "成员记录不应消失"
            hb2 = members2[reg_id]["heartbeat_at"]
            assert hb2 > hb1, (
                f"心跳应更新 heartbeat_at: {hb1} -> {hb2}"
            )
        finally:
            proc.send_signal(signal.SIGTERM)
            proc.wait(timeout=10)
