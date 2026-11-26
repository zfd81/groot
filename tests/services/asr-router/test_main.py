"""asr-router 子服务入口（main.handle）集成测试。

通过 SDK 的 ``build_app`` 加载真实子服务包，并把已加载模块的 ``create_backend``
替换成 fake 后端，从而在无 MLX / 无模型的情况下验证 HTTP 状态、错误信封与响应
字段。未就绪用例围绕 ``handle`` 构建最小 FastAPI 应用，直接触发其自身的 503
守卫（``build_app`` 的就绪依赖会先于 ``handle`` 拦截，无法覆盖该分支）。
"""

import io
import sys
from pathlib import Path

import numpy as np
import pytest

pytest.importorskip("numpy")
pytest.importorskip("soundfile")

import soundfile as sf
from fastapi import FastAPI
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from fastapi.testclient import TestClient
from starlette.datastructures import UploadFile as StarletteUploadFile

from groot_service_sdk import APIError, error_envelope
from groot_service_sdk.launcher import build_app

_REPO_ROOT = Path(__file__).resolve().parents[3]
_SERVICE_DIR = _REPO_ROOT / "services" / "asr-router"
TOKEN = "test-token"
TOKEN_HEADERS = {"X-Groot-Internal-Token": TOKEN}


def _wav_bytes(seconds: float = 2.0, sample_rate: int = 16000) -> bytes:
    """生成指定时长的 16kHz 单声道正弦波 WAV 字节。"""
    t = np.arange(int(seconds * sample_rate)) / sample_rate
    mono = (0.5 * np.sin(2 * np.pi * 440.0 * t)).astype(np.float32)
    buf = io.BytesIO()
    sf.write(buf, mono, sample_rate, format="WAV", subtype="PCM_16")
    return buf.getvalue()


class _FakeBackend:
    """记录 load/detect/close 调用并返回固定候选的假后端。"""

    def __init__(self, results: list[tuple[str, str | None, float]]) -> None:
        self._results = results
        self.loaded = 0
        self.closed = False
        self.detected: list[np.ndarray] = []

    def load(self) -> None:
        self.loaded += 1

    def detect(self, pcm: np.ndarray) -> list[tuple[str, str | None, float]]:
        self.detected.append(pcm)
        return list(self._results)

    def close(self) -> None:
        self.closed = True


@pytest.fixture
def services_root(tmp_path: Path) -> Path:
    """临时 services 根目录，用符号链接指向真实子服务，供 SDK 发现加载。"""
    root = tmp_path / "services"
    root.mkdir()
    (root / "asr-router").symlink_to(_SERVICE_DIR)
    return root


def _load(services_root: Path) -> tuple[FastAPI, dict[str, str], object]:
    """构建应用并取回被加载的 ``app.main`` 模块。"""
    app, results = build_app(services_root, ["asr-router"], TOKEN)
    module = sys.modules["groot_services.asr_router.app.main"]
    return app, results, module


def _install_fake(
    monkeypatch: pytest.MonkeyPatch,
    module: object,
    results: list[tuple[str, str | None, float]],
) -> _FakeBackend:
    fake = _FakeBackend(results)
    monkeypatch.setattr(module, "create_backend", lambda: fake)
    return fake


def _minimal_app(module: object) -> FastAPI:
    """仅注册 handle 的最小应用，复用 SDK 的错误信封语义。"""
    app = FastAPI()
    app.add_api_route("/asr-router", module.handle, methods=module.METHODS)

    @app.exception_handler(APIError)
    async def _api_error(_request, exc: APIError) -> JSONResponse:
        return JSONResponse(error_envelope(exc), status_code=exc.status_code)

    @app.exception_handler(RequestValidationError)
    async def _validation_error(_request, _exc) -> JSONResponse:
        envelope = error_envelope(
            APIError(
                400,
                "请求参数校验失败",
                type="invalid_request_error",
                param="file",
                code="invalid_request",
            )
        )
        return JSONResponse(envelope, status_code=400)

    return app


def test_detect_success(services_root, monkeypatch):
    app, results, module = _load(services_root)
    fake = _install_fake(
        monkeypatch,
        module,
        [("zh", "mandarin", 0.97), ("en", None, 0.02)],
    )

    with TestClient(app, headers=TOKEN_HEADERS) as client:
        response = client.post(
            "/asr-router",
            files={"file": ("speech.wav", _wav_bytes(2.0), "audio/wav")},
        )

    assert response.status_code == 200
    body = response.json()
    assert body["language"] == "zh"
    assert body["dialect"] == "mandarin"
    assert body["confidence"] == 0.97
    assert body["alternatives"] == [{"language": "en", "confidence": 0.02}]
    assert abs(body["duration_ms"] - 2000) <= 1
    assert set(body) == {"language", "dialect", "confidence", "alternatives", "duration_ms"}
    assert results["asr-router"] == "ok"
    assert fake.loaded == 1
    assert len(fake.detected) == 1


def test_detect_und(services_root, monkeypatch):
    app, _, module = _load(services_root)
    _install_fake(monkeypatch, module, [("und", None, 0.1)])

    with TestClient(app, headers=TOKEN_HEADERS) as client:
        response = client.post(
            "/asr-router",
            files={"file": ("speech.wav", _wav_bytes(2.0), "audio/wav")},
        )

    assert response.status_code == 200
    body = response.json()
    assert body["language"] == "und"
    assert "dialect" not in body
    assert body["confidence"] == 0.1
    assert body["alternatives"] == []


def test_detect_no_candidates(services_root, monkeypatch):
    app, _, module = _load(services_root)
    _install_fake(monkeypatch, module, [])

    with TestClient(app, headers=TOKEN_HEADERS) as client:
        response = client.post(
            "/asr-router",
            files={"file": ("speech.wav", _wav_bytes(2.0), "audio/wav")},
        )

    assert response.status_code == 200
    body = response.json()
    assert body["language"] == "und"
    assert "dialect" not in body
    assert body["confidence"] == 0.0
    assert body["alternatives"] == []


def test_missing_file(services_root, monkeypatch):
    app, _, module = _load(services_root)
    _install_fake(monkeypatch, module, [("zh", None, 0.9)])

    with TestClient(app, headers=TOKEN_HEADERS) as client:
        response = client.post("/asr-router")

    assert response.status_code == 400
    error = response.json()["error"]
    assert error["type"] == "invalid_request_error"
    assert error["param"] == "file"
    assert error["code"] == "invalid_request"


def test_unsupported_extension_rejected_before_read(services_root, monkeypatch):
    """非音频扩展名在读取请求体之前直接返回 400 unsupported_type。"""
    app, _, module = _load(services_root)
    fake = _install_fake(monkeypatch, module, [("zh", None, 0.9)])

    with TestClient(app, headers=TOKEN_HEADERS) as client:
        # 内容本身是可解码的 WAV；拒绝只能来自扩展名检查。
        response = client.post(
            "/asr-router",
            files={"file": ("speech.txt", _wav_bytes(2.0), "text/plain")},
        )

    assert response.status_code == 400
    error = response.json()["error"]
    assert error["type"] == "invalid_request_error"
    assert error["code"] == "unsupported_type"
    assert fake.detected == []


def test_backend_not_ready(services_root):
    _, _, module = _load(services_root)
    app = _minimal_app(module)

    with TestClient(app) as client:
        response = client.post(
            "/asr-router",
            files={"file": ("speech.wav", _wav_bytes(2.0), "audio/wav")},
        )

    assert response.status_code == 503
    error = response.json()["error"]
    assert error["type"] == "api_error"
    assert error["code"] == "service_unavailable"
    assert error["message"] == "子服务未就绪"


def test_startup_failure_yields_service_unavailable(services_root, monkeypatch):
    app, results, module = _load(services_root)

    def _boom():
        raise RuntimeError("模型缺失")

    monkeypatch.setattr(module, "create_backend", _boom)

    with TestClient(app, headers=TOKEN_HEADERS) as client:
        response = client.post(
            "/asr-router",
            files={"file": ("speech.wav", _wav_bytes(2.0), "audio/wav")},
        )

    assert results["asr-router"].startswith("error:")
    assert response.status_code == 503
    assert response.json()["error"]["code"] == "service_unavailable"


def test_identity_passed(services_root, monkeypatch):
    app, _, module = _load(services_root)
    _install_fake(monkeypatch, module, [("ja", None, 0.8)])
    headers = {
        **TOKEN_HEADERS,
        "X-Groot-Caller": "svc-x",
        "X-Groot-Tenant-Id": "tenant-1",
        "X-Groot-User-Id": "user-1",
        "X-Groot-Request-Id": "req-1",
        "X-Groot-Service": "asr-router",
    }

    with TestClient(app, headers=headers) as client:
        response = client.post(
            "/asr-router",
            files={"file": ("speech.wav", _wav_bytes(2.0), "audio/wav")},
        )

    assert response.status_code == 200
    assert response.json()["language"] == "ja"


def test_shutdown_closes_backend(services_root, monkeypatch):
    app, _, module = _load(services_root)
    fake = _install_fake(monkeypatch, module, [("zh", None, 0.9)])

    with TestClient(app, headers=TOKEN_HEADERS) as client:
        client.post(
            "/asr-router",
            files={"file": ("speech.wav", _wav_bytes(2.0), "audio/wav")},
        )

    assert fake.closed is True
    assert module._ready is False
    assert module._backend is None


def test_oversize_upload_rejected_before_read(services_root, monkeypatch):
    """超过 50MB 的上传依据 UploadFile.size 在读取内容前返回 413，不把整个文件读进内存。"""
    app, _, module = _load(services_root)
    fake = _install_fake(monkeypatch, module, [("zh", None, 0.9)])
    # handle 通过 SDK 命名空间加载，要 patch 它实际引用的 audio 模块。
    audio_module = sys.modules["groot_services.asr_router.app.audio"]
    monkeypatch.setattr(audio_module, "MAX_BYTES", 1024)

    read_called = []
    original_read = StarletteUploadFile.read

    async def _spy_read(self, size=-1):
        read_called.append(size)
        return await original_read(self, size)

    monkeypatch.setattr(StarletteUploadFile, "read", _spy_read)

    with TestClient(app, headers=TOKEN_HEADERS) as client:
        response = client.post(
            "/asr-router",
            files={"file": ("speech.wav", b"\x00" * 4096, "audio/wav")},
        )

    assert response.status_code == 413
    assert response.json()["error"]["code"] == "file_too_large"
    assert read_called == []
    assert fake.detected == []
