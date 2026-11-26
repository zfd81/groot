"""Python 子服务托管的系统测试（services init 布局 / init 不建布局 / 健康检查 / 路由鉴权）。

源码依据：
- ``internal/cmd/services.go`` 的 RunServicesInit 调用 ``pysvc.EnsureLayout`` 与
  ``pysvc.EnsureVenv``，创建 ``services/``、``services/.sdk/``、``services/_template/``、
  ``services/service.yaml``、``services/requirements.txt`` 与 ``services/.venv/``，并尝试安装基础依赖。
- ``internal/cmd/init.go`` 的 RunInit 不创建 ``services/``（Python 环境由 ``groot services init`` 负责）。
- ``internal/api/handler/health.go``：pyMgr 非空时 ``/web/health`` 的 checks 含 ``services``
  项，status 取 ``Status.HealthStatus()``，info 为 ``pysvc.Status``；无子服务时 status 为
  ``healthy``、``info.services`` 为空数组 ``[]``。
- ``internal/api/router.go``：为每个通过校验的子服务注册精确路径 ``/services/<name>``；
  未注册路径由 NoRoute 返回 404 ``invalid_request``；apiGroup 的鉴权中间件先于 handler 执行，
  缺 Key 返回 401 ``invalid_api_key``。

用例点：
- TC-PY-101 groot services init 创建 services 固定布局（含 .venv）
- TC-PY-102 /web/health 含 services 检查项（无子服务时 healthy、services 为空数组）
- TC-PY-103 未注册的 /services/<name> 返回 404 invalid_request
- TC-PY-104 已注册的 /services/asr-router 缺 Key 返回 401 invalid_api_key（鉴权先于 503）
- TC-PY-105 groot init 不创建 services 目录
"""
import os
import shutil
import socket
import subprocess
import tempfile
import time

import pytest
import requests

from conftest import BASE_URL, GROOT_BIN

pytestmark = pytest.mark.skipif(
    not os.path.exists(GROOT_BIN),
    reason=f"groot 二进制不存在: {GROOT_BIN}（先执行 go build -o dist/groot ./cmd/groot）",
)


def _run_groot(args, home, timeout=30):
    """以指定 GROOT_HOME 运行 groot 子命令，返回 CompletedProcess。"""
    env = os.environ.copy()
    env["GROOT_HOME"] = home
    return subprocess.run(
        [GROOT_BIN] + args, env=env, capture_output=True, text=True, timeout=timeout,
    )


def _free_port() -> int:
    """向内核申请一个空闲端口（bind 后立即释放）。"""
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _wait_health(port: int, timeout: int = 30) -> bool:
    """等待指定端口的 /web/health 就绪。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            if requests.get(f"http://127.0.0.1:{port}/web/health", timeout=2).status_code == 200:
                return True
        except requests.RequestException:
            pass
        time.sleep(0.5)
    return False


def test_services_init_creates_layout():
    """TC-PY-101: groot services init 在临时 GROOT_HOME 创建 services 固定布局。

    前置条件：本机 Python 3.9+ 且支持 venv/ensurepip；基础依赖安装失败时命令仍返回 0。
    """
    home = tempfile.mkdtemp(prefix="groot_pysvc_init_")
    try:
        result = _run_groot(["services", "init"], home, timeout=300)
        assert result.returncode == 0, result.stderr

        for rel in (
            "services",
            "services/.sdk",
            "services/_template",
            "services/service.yaml",
            "services/requirements.txt",
            "services/.venv",
        ):
            path = os.path.join(home, *rel.split("/"))
            assert os.path.exists(path), f"services init 未创建 {rel}"
    finally:
        shutil.rmtree(home, ignore_errors=True)


def test_init_does_not_create_services_layout():
    """TC-PY-105: groot init 不创建 services 目录（Python 环境由 services init 负责）。"""
    home = tempfile.mkdtemp(prefix="groot_init_no_services_")
    try:
        result = _run_groot(["init"], home)
        assert result.returncode == 0, result.stderr
        assert not os.path.exists(os.path.join(home, "services")), "init 不应创建 services 目录"
    finally:
        shutil.rmtree(home, ignore_errors=True)


def test_services_status_in_health(server):
    """TC-PY-102: /web/health 含 services 检查项，services 为数组。

    session ``server`` 的 TEST_HOME 通常没有子服务目录，此时整体为 healthy、数组为空；
    共享 home 中可能已放有子服务目录，因此允许 healthy 或 degraded 与非空数组，
    不假设会话 home 是干净的。
    """
    resp = requests.get(f"{BASE_URL}/web/health", timeout=10)
    assert resp.status_code == 200, resp.text

    checks = resp.json().get("checks", {})
    assert "services" in checks, f"健康检查缺少 services 项: {checks}"
    service_check = checks["services"]
    assert service_check["status"] in ("healthy", "degraded"), service_check
    services = service_check["info"]["services"]
    assert isinstance(services, list), service_check


def test_unknown_service_404(server, api_headers):
    """TC-PY-103: 未注册的 /services/<name> 返回 404 invalid_request。"""
    resp = requests.get(f"{BASE_URL}/services/nope", headers=api_headers, timeout=10)
    assert resp.status_code == 404, resp.text
    assert resp.json()["error"]["code"] == "invalid_request"


def test_asr_router_requires_auth():
    """TC-PY-104: 已注册子服务缺 Key 返回 401 invalid_api_key（鉴权先于 503）。

    独立临时实例：init 后创建最小 asr-router 子服务（解释器缺失使服务停用，但路由已注册），
    再以无 Authorization 请求验证鉴权在代理与就绪判断之前执行。
    """
    home = tempfile.mkdtemp(prefix="groot_pysvc_auth_")
    logs_dir = tempfile.mkdtemp(prefix="groot_pysvc_logs_")
    port = _free_port()
    proc = None
    try:
        assert _run_groot(["init"], home).returncode == 0

        # 把日志目录指向临时目录，避免在 TEST_HOME 下产生文件
        with open(os.path.join(home, "bootstrap.yaml"), "a") as f:
            f.write("\nlogging:\n  file:\n    directory: " + logs_dir + "\n")

        service_dir = os.path.join(home, "services", "asr-router")
        app_dir = os.path.join(service_dir, "app")
        os.makedirs(app_dir, exist_ok=True)
        open(os.path.join(app_dir, "__init__.py"), "w").close()
        with open(os.path.join(app_dir, "main.py"), "w") as f:
            f.write(
                'METHODS = ["POST"]\n\n\n'
                'async def handle() -> dict:\n'
                '    return {"ok": True}\n'
            )
        with open(os.path.join(service_dir, "requirements.txt"), "w") as f:
            f.write("# 无额外依赖\n")

        env = os.environ.copy()
        env["GROOT_HOME"] = home
        proc = subprocess.Popen(
            [GROOT_BIN, "-p", str(port)],
            env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        assert _wait_health(port), "独立实例启动失败"

        resp = requests.post(f"http://127.0.0.1:{port}/services/asr-router", timeout=10)
        assert resp.status_code == 401, resp.text
        assert resp.json()["error"]["code"] == "invalid_api_key"
    finally:
        if proc is not None:
            proc.terminate()
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill()
        shutil.rmtree(home, ignore_errors=True)
        shutil.rmtree(logs_dir, ignore_errors=True)
