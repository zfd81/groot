"""SDK 启动器单元测试：加载、令牌、身份、错误信封、启动隔离与逆序停止。"""

import io
import json
import os
import queue
import subprocess
import sys
import threading
import time
from pathlib import Path

import httpx
import pytest
from fastapi.testclient import TestClient

from groot_service_sdk import SDK_VERSION
from groot_service_sdk.launcher import build_app, load_service, read_handshake

FIXTURES = Path(__file__).parent / "fixtures"
TOKEN = "test-token"
TOKEN_HEADERS = {"X-Groot-Internal-Token": TOKEN}
READY_PREFIX = "GROOT_SERVICE_READY "


def _build(names):
    return build_app(FIXTURES, names, TOKEN)


def test_load_service_reads_entry_contract():
    svc = load_service(FIXTURES, "svc_ok")

    assert svc.name == "svc_ok"
    assert svc.methods == ["POST"]
    assert callable(svc.handle)
    assert callable(svc.startup)
    assert callable(svc.shutdown)
    assert svc.ready is False


def test_build_app_registers_handle():
    app, results = _build(["svc_ok"])

    with TestClient(app, headers=TOKEN_HEADERS) as client:
        response = client.post("/svc_ok")

    assert response.status_code == 200
    assert response.json()["ok"] is True
    assert results["svc_ok"] == "ok"


def test_token_required():
    app, _ = _build(["svc_ok"])

    with TestClient(app) as client:
        missing = client.post("/svc_ok")
        wrong = client.post("/svc_ok", headers={"X-Groot-Internal-Token": "wrong"})
        health = client.get("/healthz")

    assert missing.status_code == 401
    assert missing.json()["error"]["code"] == "invalid_api_key"
    assert wrong.status_code == 401
    assert health.status_code == 200
    assert health.json() == {"status": "ok", "sdk": SDK_VERSION, "services": {"svc_ok": "ok"}}


def test_identity_dependency():
    app, _ = _build(["svc_ok"])
    headers = {
        **TOKEN_HEADERS,
        "X-Groot-Caller": "svc-x",
        "X-Groot-Tenant-Id": "tenant-1",
        "X-Groot-User-Id": "user-1",
        "X-Groot-Request-Id": "req-1",
        "X-Groot-Service": "svc_ok",
    }

    with TestClient(app) as client:
        response = client.post("/svc_ok", headers=headers)

    assert response.status_code == 200
    assert response.json() == {
        "ok": True,
        "caller": "svc-x",
        "tenant_id": "tenant-1",
        "user_id": "user-1",
        "service": "svc_ok",
    }


def test_apierror_envelope():
    app, _ = _build(["svc_b"])

    with TestClient(app, headers=TOKEN_HEADERS) as client:
        response = client.post("/svc_b", params={"kind": "boom"})

    assert response.status_code == 400
    assert response.json() == {
        "error": {
            "message": "不支持的音频格式",
            "type": "invalid_request_error",
            "param": None,
            "code": "unsupported_type",
        }
    }


def test_validation_error_envelope():
    app, _ = _build(["svc_b"])

    with TestClient(app, headers=TOKEN_HEADERS) as client:
        response = client.post("/svc_b")

    assert response.status_code == 400
    body = response.json()
    assert body["error"]["type"] == "invalid_request_error"
    assert body["error"]["param"] == "kind"


def test_startup_failure_isolated():
    app, results = _build(["svc_ok", "svc_fail"])

    with TestClient(app, headers=TOKEN_HEADERS) as client:
        healthy = client.post("/svc_ok")
        failed = client.post("/svc_fail")

    assert healthy.status_code == 200
    assert failed.status_code == 503
    assert failed.json()["error"]["code"] == "service_unavailable"
    assert results["svc_ok"] == "ok"
    assert results["svc_fail"].startswith("error:")


def test_shutdown_reverse_order(monkeypatch, tmp_path):
    events = tmp_path / "events.log"
    monkeypatch.setenv("GROOT_TEST_EVENTS", str(events))
    app, _ = _build(["svc_ok", "svc_b"])

    with TestClient(app, headers=TOKEN_HEADERS) as client:
        assert client.post("/svc_ok").status_code == 200

    assert events.read_text(encoding="utf-8").splitlines() == [
        "startup:svc_b",
        "startup:svc_ok",
        "shutdown:svc_ok",
        "shutdown:svc_b",
    ]


def test_unknown_method_405():
    app, _ = _build(["svc_ok"])

    with TestClient(app, headers=TOKEN_HEADERS) as client:
        response = client.get("/svc_ok")

    assert response.status_code == 405
    assert response.json()["error"]["code"] == "invalid_request"


def _sdk_root() -> Path:
    return Path(__file__).resolve().parents[3] / "internal" / "pysvc" / "sdk"


def _child_env() -> dict:
    env = dict(os.environ)
    env["PYTHONPATH"] = str(_sdk_root())
    env["GROOT_LOG_LEVEL"] = "WARNING"
    return env


def _read_ready_line(
    proc: subprocess.Popen, stderr_path: Path, timeout: float = 30.0
) -> str:
    lines: queue.Queue = queue.Queue()

    def _pump() -> None:
        for line in proc.stdout:
            lines.put(line)
        lines.put(None)

    threading.Thread(target=_pump, daemon=True).start()
    deadline = time.monotonic() + timeout
    while True:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise AssertionError("等待就绪行超时")
        try:
            line = lines.get(timeout=remaining)
        except queue.Empty:
            raise AssertionError("等待就绪行超时") from None
        if line is None:
            proc.kill()
            stderr = stderr_path.read_text(encoding="utf-8") if stderr_path.exists() else ""
            raise AssertionError("进程在就绪前退出: " + stderr)
        if line.startswith(READY_PREFIX):
            return line.strip()
        print(line, end="")


def test_main_ready_line_and_lifeline(tmp_path):
    stderr_path = tmp_path / "launcher.stderr"
    with open(stderr_path, "w", encoding="utf-8") as stderr_file:
        proc = subprocess.Popen(
            [sys.executable, "-m", "groot_service_sdk.launcher"],
            cwd=str(FIXTURES),
            env=_child_env(),
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=stderr_file,
            text=True,
            bufsize=1,
        )
        try:
            proc.stdin.write(TOKEN + "\n")
            proc.stdin.write(json.dumps(["svc_ok", "svc_fail"]) + "\n")
            proc.stdin.flush()

            ready = _read_ready_line(proc, stderr_path)
            payload = json.loads(ready[len(READY_PREFIX):])

            assert payload["sdk"] == SDK_VERSION
            assert payload["pid"] == proc.pid
            assert payload["services"]["svc_ok"] == "ok"
            assert payload["services"]["svc_fail"].startswith("error:")
            base = f"http://127.0.0.1:{payload['port']}"

            health = httpx.get(base + "/healthz", timeout=5.0)
            assert health.status_code == 200
            assert health.json() == {"status": "ok", "sdk": SDK_VERSION, "services": payload["services"]}

            unauthorized = httpx.post(base + "/svc_ok", timeout=5.0)
            assert unauthorized.status_code == 401

            authorized = httpx.post(base + "/svc_ok", headers=TOKEN_HEADERS, timeout=5.0)
            assert authorized.status_code == 200
            assert authorized.json()["ok"] is True
        finally:
            try:
                proc.stdin.close()
            except Exception:
                pass
            try:
                proc.wait(timeout=15)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=5)

    assert proc.returncode == 0


def test_main_ready_line_with_non_utf8_stdout(tmp_path):
    """stdout 为非 UTF-8 编码时，含 CJK 的启动错误仍要上报就绪行而不是挂死。"""
    stderr_path = tmp_path / "launcher.stderr"
    env = _child_env()
    # 模拟不支持的终端编码：写 CJK 会抛 UnicodeEncodeError。
    env["PYTHONIOENCODING"] = "ascii:strict"
    with open(stderr_path, "w", encoding="utf-8") as stderr_file:
        proc = subprocess.Popen(
            [sys.executable, "-m", "groot_service_sdk.launcher"],
            cwd=str(FIXTURES),
            env=env,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=stderr_file,
            text=True,
            bufsize=1,
        )
        try:
            proc.stdin.write(TOKEN + "\n")
            # svc_fail 的 startup 抛出「模型加载失败」，CJK 文本会进入就绪行。
            proc.stdin.write(json.dumps(["svc_fail"]) + "\n")
            proc.stdin.flush()

            ready = _read_ready_line(proc, stderr_path, timeout=10.0)
            payload = json.loads(ready[len(READY_PREFIX):])

            assert payload["sdk"] == SDK_VERSION
            assert payload["services"]["svc_fail"].startswith("error:")
        finally:
            try:
                proc.stdin.close()
            except Exception:
                pass
            try:
                proc.wait(timeout=15)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=5)

    assert proc.returncode == 0


def test_read_handshake_reads_token_and_names(monkeypatch):
    stdin = io.TextIOWrapper(io.BytesIO(b'secret\n["svc_ok"]\n'))
    monkeypatch.setattr(sys, "stdin", stdin)

    token, names = read_handshake()

    assert token == "secret"
    assert names == ["svc_ok"]


def test_read_handshake_rejects_empty_token(monkeypatch):
    stdin = io.TextIOWrapper(io.BytesIO(b"\n[]\n"))
    monkeypatch.setattr(sys, "stdin", stdin)

    with pytest.raises(SystemExit) as excinfo:
        read_handshake()

    assert "令牌" in str(excinfo.value)


def test_main_rejects_empty_token(tmp_path):
    stderr_path = tmp_path / "launcher.stderr"
    with open(stderr_path, "w", encoding="utf-8") as stderr_file:
        proc = subprocess.run(
            [sys.executable, "-m", "groot_service_sdk.launcher"],
            cwd=str(FIXTURES),
            env=_child_env(),
            input="\n[]\n",
            stdout=subprocess.PIPE,
            stderr=stderr_file,
            text=True,
            timeout=30,
        )

    assert proc.returncode != 0
    assert "令牌" in stderr_path.read_text(encoding="utf-8")


def test_import_failure_returns_503():
    app, results = _build(["svc_import_fail"])

    with TestClient(app, headers=TOKEN_HEADERS) as client:
        response = client.post("/svc_import_fail")
        get_response = client.get("/svc_import_fail")

    assert response.status_code == 503
    assert response.json()["error"]["code"] == "service_unavailable"
    assert get_response.status_code == 503
    assert results["svc_import_fail"].startswith("error:")


def test_route_registration_failure_isolated():
    """路由注册阶段失败（如参数类型无法解析、缺少 python-multipart）只影响该子服务。"""
    app, results = _build(["svc_bad_route", "svc_ok"])

    with TestClient(app, headers=TOKEN_HEADERS) as client:
        bad = client.post("/svc_bad_route")
        ok = client.post("/svc_ok")

    assert bad.status_code == 503
    assert bad.json()["error"]["code"] == "service_unavailable"
    assert results["svc_bad_route"].startswith("error:")
    assert ok.status_code == 200
    assert results["svc_ok"] == "ok"


def test_healthz_reports_service_readiness():
    """/healthz 返回进程与各子服务的就绪状态（设计 1.5.4），不只是常量 ok。"""
    app, _results = _build(["svc_ok", "svc_import_fail"])

    with TestClient(app) as client:
        payload = client.get("/healthz").json()

    assert payload["status"] == "ok"
    assert payload["sdk"] == SDK_VERSION
    assert payload["services"]["svc_ok"] == "ok"
    assert payload["services"]["svc_import_fail"].startswith("error:")


def test_scan_services_skips_reserved_healthz(tmp_path):
    """--dev 扫描与 Go 侧一致：healthz 是保留名，不作为子服务。"""
    from groot_service_sdk.launcher import scan_services

    for name in ("healthz", "good"):
        (tmp_path / name / "app").mkdir(parents=True)
        (tmp_path / name / "app" / "__init__.py").write_text("")
        (tmp_path / name / "app" / "main.py").write_text("")
        (tmp_path / name / "requirements.txt").write_text("")

    assert scan_services(tmp_path) == ["good"]
