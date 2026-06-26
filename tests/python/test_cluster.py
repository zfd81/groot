"""
集群管理系统测试

测试覆盖：
- 单实例启动，自动成为 Leader
- 双实例启动，第二个成为 Follower
- Leader 被 kill（宕机），Follower 提升为 Leader
- Leader 优雅退出，Follower 提升为 Leader
- 多实例同时运行，恰好一个 Leader
- 故障恢复实例重新注册为 Follower
"""

import pytest
import os
import time
import signal
import subprocess
import requests
from pathlib import Path

from conftest import GROOT_BIN

# 集群测试使用独立的 GROOT_HOME，避免与默认测试 server fixture 冲突
CLUSTER_HOME = os.environ.get("GROOT_TEST_CLUSTER_HOME", "/tmp/groot_cluster_test")


def _create_cluster_config(home: str, port: int) -> None:
    """创建测试用的 config.yaml"""
    os.makedirs(home, exist_ok=True)
    for d in ["skills", "mcp", "memory", "logs", "schedules/active",
              "schedules/disabled", "schedules/archive", "schedules/executions"]:
        os.makedirs(os.path.join(home, d), exist_ok=True)

    import yaml
    config = {
        "agent": {"name": "groot", "version": "1.0.0"},
        "server": {"host": "127.0.0.1", "port": port},
        "llm": {
            "default_model": "qwen-local",
            "models": {
                "qwen-local": {
                    "base_url": "http://127.0.0.1:8230/v1",
                    "api_key": "bonc1q2w3e",
                    "model": "Qwen3.5-122B-A10B-6bit",
                    "temperature": 0.2,
                }
            },
        },
        "skills": {"hot_reload": {"enabled": False}},
        "security": {"auth": {"enabled": False}},
        "memory": {"directory": "memory"},
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
            f"可能端口被占用。请先运行: pkill -f 'bin/groot'"
        )
    return proc


def _wait_health(port: int, timeout: int = 20) -> bool:
    """等待实例健康检查通过"""
    start = time.time()
    while time.time() - start < timeout:
        try:
            r = requests.get(f"http://127.0.0.1:{port}/health", timeout=1)
            if r.status_code == 200:
                return True
        except Exception:
            pass
        time.sleep(0.5)
    return False


def _get_registration_files(home: str = CLUSTER_HOME) -> dict:
    """读取集群注册文件，返回 {reg_id: {'role': str, 'host': str, 'port': int, 'pid': int}}"""
    members_dir = Path(home) / "cluster" / "members"
    if not members_dir.exists():
        return {}
    result = {}
    for f in members_dir.iterdir():
        if f.is_file():
            content = f.read_text().strip()
            parts = content.split("|")
            if len(parts) == 3:
                host_port = parts[1].split(":")
                result[f.name] = {
                    "role": parts[0],
                    "host": host_port[0],
                    "port": int(host_port[1]),
                    "pid": int(parts[2]),
                }
    return result


def _cleanup():
    """清理测试环境"""
    import shutil
    # 先杀残留 groot 进程
    subprocess.run(["pkill", "-f", "bin/groot"], check=False, capture_output=True)
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
    # 杀掉所有残留的 groot 进程 (通过注册文件中的 PID)
    regs = _get_registration_files()
    for info in regs.values():
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
        proc = _start_groot(8080)
        try:
            assert _wait_health(8080), "实例未能在 20s 内启动"

            regs = _get_registration_files()
            assert len(regs) == 1, f"应恰好 1 个注册文件，实际: {len(regs)}"

            info = list(regs.values())[0]
            assert info["role"] == "leader", f"应为 leader，实际: {info['role']}"
            assert info["port"] == 8080
            assert info["host"] == "127.0.0.1"
        finally:
            proc.send_signal(signal.SIGTERM)
            proc.wait(timeout=10)

    def test_registration_file_format(self):
        """验证注册文件内容格式 {role}|{host}:{port}|{pid}"""
        proc = _start_groot(8080)
        try:
            assert _wait_health(8080), "实例未能在 20s 内启动"

            regs = _get_registration_files()
            reg_id = list(regs.keys())[0]
            info = regs[reg_id]

            # 注册编号应为 17 位数字 (YYYYMMDDHHMMSSmmm)
            assert len(reg_id) == 17, f"注册编号应为 17 位，实际: {len(reg_id)}"
            assert reg_id.isdigit(), f"注册编号应为纯数字，实际: {reg_id}"

            # 角色应为 leader
            assert info["role"] in ("leader", "follower")

            # PID 应为正整数
            assert info["pid"] > 0
        finally:
            proc.send_signal(signal.SIGTERM)
            proc.wait(timeout=10)


class TestDualInstance:
    """双实例场景"""

    def test_second_instance_becomes_follower(self):
        """第二个实例启动后应成为 Follower"""
        p1 = _start_groot(8080)
        try:
            assert _wait_health(8080), "实例1 未能在 20s 内启动"
            time.sleep(0.1)  # 确保注册编号不同

            p2 = _start_groot(8081)
            try:
                assert _wait_health(8081), "实例2 未能在 20s 内启动"
                time.sleep(1)

                regs = _get_registration_files()
                assert len(regs) == 2, f"应恰好 2 个注册文件，实际: {len(regs)}"

                roles = [info["role"] for info in regs.values()]
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
        p1 = _start_groot(8080)
        try:
            assert _wait_health(8080)
            time.sleep(0.1)

            p2 = _start_groot(8081)
            try:
                assert _wait_health(8081)
                time.sleep(1)

                regs = _get_registration_files()
                # 找出注册编号最小的实例
                sorted_ids = sorted(regs.keys())
                leader_info = regs[sorted_ids[0]]
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
        p1 = _start_groot(8080)
        p2 = _start_groot(8081)
        try:
            assert _wait_health(8080), "实例1 未启动"
            assert _wait_health(8081), "实例2 未启动"
            time.sleep(0.5)

            # 确认 p1 是 leader
            regs = _get_registration_files()
            sorted_ids = sorted(regs.keys())
            leader_id = sorted_ids[0]
            assert regs[leader_id]["role"] == "leader"

            # 杀掉 Leader（模拟宕机，不用 SIGTERM）
            leader_port = regs[leader_id]["port"]
            leader_proc = p1 if leader_port == 8080 else p2
            os.kill(leader_proc.pid, signal.SIGKILL)
            leader_proc.wait(timeout=5)

            # 等待心跳超时 (7s) + 一个心跳周期 (3s) + 余量
            time.sleep(12)

            # Follower 应提升为 Leader
            regs = _get_registration_files()
            assert len(regs) == 1, (
                f"Leader 被杀后应只剩 1 个注册文件 (Follower 提升为 Leader)，实际: {len(regs)}"
            )
            survivor = list(regs.values())[0]
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
        p1 = _start_groot(8080)
        p2 = _start_groot(8081)
        try:
            assert _wait_health(8080), "实例1 未启动"
            assert _wait_health(8081), "实例2 未启动"
            time.sleep(0.5)

            regs = _get_registration_files()
            sorted_ids = sorted(regs.keys())
            leader_id = sorted_ids[0]
            leader_port = regs[leader_id]["port"]

            # Leader 优雅退出
            leader_proc = p1 if leader_port == 8080 else p2
            leader_proc.send_signal(signal.SIGTERM)
            leader_proc.wait(timeout=10)

            # 等待 Follower 检测到 Leader 文件被删除并提升
            time.sleep(8)

            regs = _get_registration_files()
            assert len(regs) == 1, (
                f"Leader 退出后应只剩 1 个注册文件，实际: {len(regs)}"
            )
            survivor = list(regs.values())[0]
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
            for port in [8080, 8081, 8082]:
                procs.append(_start_groot(port))
                time.sleep(0.1)

            for port in [8080, 8081, 8082]:
                assert _wait_health(port), f"实例 {port} 未启动"

            time.sleep(1)

            regs = _get_registration_files()
            assert len(regs) == 3, f"应有 3 个注册文件，实际: {len(regs)}"

            roles = [info["role"] for info in regs.values()]
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
        p1 = _start_groot(8080)
        p2 = _start_groot(8081)
        try:
            assert _wait_health(8080), "实例1 未启动"
            assert _wait_health(8081), "实例2 未启动"
            time.sleep(0.5)

            # 确认 leader
            regs = _get_registration_files()
            sorted_ids = sorted(regs.keys())
            leader_port = regs[sorted_ids[0]]["port"]

            # 杀掉 Leader
            leader_proc = p1 if leader_port == 8080 else p2
            os.kill(leader_proc.pid, signal.SIGKILL)
            leader_proc.wait(timeout=5)

            # 等待 Follower 提升
            time.sleep(12)

            # 确认存活实例已是 Leader
            regs = _get_registration_files()
            assert len(regs) == 1
            assert list(regs.values())[0]["role"] == "leader"

            # 旧 Leader 重新启动（使用新端口）
            p3 = _start_groot(8082)
            try:
                assert _wait_health(8082), "重启的实例未启动"
                time.sleep(2)

                regs = _get_registration_files()
                assert len(regs) >= 2, f"重启后应有至少 2 个注册文件，实际: {len(regs)}"

                # 重启的实例应为 Follower (注册编号最大)
                sorted_ids = sorted(regs.keys())
                newest_id = sorted_ids[-1]
                assert regs[newest_id]["role"] == "follower", (
                    f"重启的旧 Leader 应为 follower，实际: {regs[newest_id]['role']}"
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


class TestHeartbeatFileUpdate:
    """心跳文件更新"""

    def test_leader_file_mtime_updates(self):
        """Leader 心跳应持续更新注册文件 mtime"""
        proc = _start_groot(8080)
        try:
            assert _wait_health(8080)

            members_dir = Path(CLUSTER_HOME) / "cluster" / "members"
            files = list(members_dir.iterdir())
            assert len(files) == 1

            mtime1 = files[0].stat().st_mtime

            # 等待超过一个心跳周期 (3s)
            time.sleep(5)

            # mtime 应已更新
            mtime2 = files[0].stat().st_mtime
            assert mtime2 > mtime1, (
                f"心跳应更新 mtime: {mtime1} -> {mtime2}"
            )
        finally:
            proc.send_signal(signal.SIGTERM)
            proc.wait(timeout=10)
