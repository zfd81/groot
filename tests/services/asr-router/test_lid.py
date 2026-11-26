"""asr-router FireRedLID 后端单元测试。

测试不依赖 MLX：推理路径用 fake frontend 与替换后的 ``_run_model`` 驱动；
真实模型用例在缺少 MLX 或本地模型快照时 skip。
"""

import importlib.util
import sys
import types
from pathlib import Path

import numpy as np
import pytest

from app import lid

_REPO_ROOT = Path(__file__).resolve().parents[3]


class _FakeFrontend:
    """记录调用参数并返回固定形状特征的假前端。"""

    def __init__(self) -> None:
        self.calls: list[tuple[np.ndarray, int]] = []

    def extract_pcm(self, samples: np.ndarray, sample_rate: int) -> np.ndarray:
        self.calls.append((samples, sample_rate))
        return np.zeros((25, 80), dtype=np.float32)


def test_model_dir_env(monkeypatch):
    monkeypatch.setenv("GROOT_SERVICE_MODELS_DIR", "/tmp/x")

    assert lid.model_dir() == Path("/tmp/x")


def test_model_dir_default(monkeypatch):
    monkeypatch.delenv("GROOT_SERVICE_MODELS_DIR", raising=False)

    expected = (_REPO_ROOT / "services" / "asr-router" / "models").resolve()
    assert lid.model_dir() == expected
    assert lid.model_dir().is_absolute()


def test_platform_supported(monkeypatch):
    monkeypatch.setattr(lid.platform, "system", lambda: "Darwin")
    monkeypatch.setattr(lid.platform, "machine", lambda: "arm64")
    assert lid.platform_supported() is True

    monkeypatch.setattr(lid.platform, "system", lambda: "Linux")
    monkeypatch.setattr(lid.platform, "machine", lambda: "x86_64")
    assert lid.platform_supported() is False


def test_load_fails_without_model(monkeypatch, tmp_path):
    monkeypatch.setattr(lid, "platform_supported", lambda: True)
    missing = tmp_path / "missing"
    monkeypatch.setenv("GROOT_SERVICE_MODELS_DIR", str(missing))

    backend = lid.create_backend()

    with pytest.raises(RuntimeError) as exc_info:
        backend.load()

    message = str(exc_info.value)
    assert "huggingface-cli download" in message
    assert "aufklarer/FireRedLID-887M-MLX-8bit" in message
    assert "1ae1296262c51cfd38de0f134011e6fa4b657543" in message
    assert f"--local-dir {missing}" in message


def test_detect_parses_hypotheses(monkeypatch):
    frontend = _FakeFrontend()
    backend = lid.FireRedLIDBackend(frontend=frontend)
    seen_features: list[np.ndarray] = []

    def fake_run_model(features: np.ndarray) -> list[tuple[str, float]]:
        seen_features.append(features)
        return [("zh mandarin", 0.97), ("en", 0.02)]

    monkeypatch.setattr(backend, "_run_model", fake_run_model)
    pcm = np.zeros(32000, dtype=np.float32)

    assert backend.detect(pcm) == [("zh", "mandarin", 0.97), ("en", None, 0.02)]
    assert seen_features[0].shape == (25, 80)
    assert frontend.calls[0][0] is pcm
    assert frontend.calls[0][1] == 16000


def test_detect_sorts_by_confidence_descending(monkeypatch):
    frontend = _FakeFrontend()
    backend = lid.FireRedLIDBackend(frontend=frontend)
    monkeypatch.setattr(
        backend,
        "_run_model",
        lambda features: [("en", 0.02), ("zh yue", 0.31), ("zh mandarin", 0.97)],
    )

    assert backend.detect(np.zeros(32000, dtype=np.float32)) == [
        ("zh", "mandarin", 0.97),
        ("zh", "yue", 0.31),
        ("en", None, 0.02),
    ]


def test_create_backend_returns_firered_backend():
    assert isinstance(lid.create_backend(), lid.FireRedLIDBackend)


def test_namespaced_import_without_top_level_app(monkeypatch):
    """生产环境以 groot_services.asr_router.app.lid 加载，无顶层 app 包。"""
    service_dir = _REPO_ROOT / "services" / "asr-router"
    app_dir = service_dir / "app"

    monkeypatch.setattr(
        sys,
        "path",
        [
            entry
            for entry in sys.path
            if Path(entry or ".").resolve() != service_dir.resolve()
        ],
    )
    for name in ("app", "app.lid", "app.labels"):
        monkeypatch.delitem(sys.modules, name, raising=False)

    for name, path in (
        ("groot_services", service_dir),
        ("groot_services.asr_router", service_dir),
        ("groot_services.asr_router.app", app_dir),
    ):
        package = types.ModuleType(name)
        package.__path__ = [str(path)]
        monkeypatch.setitem(sys.modules, name, package)

    spec = importlib.util.spec_from_file_location(
        "groot_services.asr_router.app.lid", app_dir / "lid.py"
    )
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    monkeypatch.setitem(sys.modules, spec.name, module)
    spec.loader.exec_module(module)

    assert module.normalize_label("zh mandarin") == ("zh", "mandarin")
    assert "app" not in sys.modules


def test_real_model_smoke():
    pytest.importorskip("mlx")
    if not lid.platform_supported():
        pytest.skip("MLX 推理仅在 Apple Silicon macOS 上可用")
    directory = lid.model_dir()
    if not directory.is_dir() or not all(
        (directory / name).is_file() for name in lid.REQUIRED_FILES
    ):
        pytest.skip(f"未找到完整的本地模型快照 {directory}")

    backend = lid.create_backend()
    backend.load()

    t = np.arange(32000, dtype=np.float32) / 16000
    pcm = (0.2 * np.sin(2 * np.pi * 220.0 * t)).astype(np.float32)
    results = backend.detect(pcm)

    assert results
    assert all(len(language) == 2 for language, _, _ in results)
    assert 0.0 <= results[0][2] <= 1.0


def test_detect_runs_frontend_under_lock(monkeypatch):
    """特征提取与推理都在后端锁内执行：kaldi fbank 前端有内部状态，不能并发复用。"""
    frontend = _FakeFrontend()
    backend = lid.FireRedLIDBackend(frontend=frontend)

    locked_during_extract = []
    original_extract = frontend.extract_pcm

    def _spy_extract(samples, sample_rate):
        # threading.Lock 已被持有时 acquire(blocking=False) 返回 False
        acquired = backend._lock.acquire(blocking=False)
        if acquired:
            backend._lock.release()
        locked_during_extract.append(not acquired)
        return original_extract(samples, sample_rate)

    frontend.extract_pcm = _spy_extract
    monkeypatch.setattr(backend, "_run_model", lambda _features: [("zh", 0.9)])

    backend.detect(np.zeros(16000, dtype=np.float32))

    assert locked_during_extract == [True]
