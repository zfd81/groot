"""FireRedLID 语种识别后端（MLX INT8，Apple Silicon）。

``mlx`` 与模型自带的 ``frontend.py``/``mlx_model.py`` 只在 :meth:`load`
中延迟导入，因此无 MLX 的环境仍可导入本模块并运行单元测试。
"""

from __future__ import annotations

import importlib.util
import os
import platform
import sys
import threading
from pathlib import Path
from types import ModuleType
from typing import Protocol

import numpy as np

from .audio import SAMPLE_RATE
from .labels import normalize_label

MODEL_REPO = "aufklarer/FireRedLID-887M-MLX-8bit"
MODEL_REVISION = "1ae1296262c51cfd38de0f134011e6fa4b657543"
REQUIRED_FILES = (
    "config.json",
    "cmvn.ark",
    "dict.txt",
    "model.safetensors",
    "frontend.py",
    "mlx_model.py",
)
BEAM_SIZE = 3
NBEST = 3

FRONTEND_MODULE_NAME = "_groot_asr_router_firered_frontend"
MLX_MODEL_MODULE_NAME = "_groot_asr_router_firered_mlx_model"


class LIDBackend(Protocol):
    """语种识别后端：加载一次，之后反复推理。"""

    def load(self) -> None: ...

    def detect(self, pcm: np.ndarray) -> list[tuple[str, str | None, float]]: ...


def model_dir() -> Path:
    """模型目录：优先 ``GROOT_SERVICE_MODELS_DIR``，否则子服务内 ``models/``。"""
    configured = os.environ.get("GROOT_SERVICE_MODELS_DIR")
    if configured:
        return Path(configured)
    return Path(__file__).resolve().parent.parent / "models"


def platform_supported() -> bool:
    """MLX 仅在 Apple Silicon 的 macOS 上可用。"""
    return platform.system() == "Darwin" and platform.machine() == "arm64"


def _download_hint(directory: Path) -> str:
    return (
        f"huggingface-cli download {MODEL_REPO} --revision {MODEL_REVISION} "
        f"--local-dir {directory}"
    )


def _load_module(name: str, path: Path) -> ModuleType:
    """以唯一扁平模块名从模型目录加载模块，避免与其他子服务重名。"""
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"无法加载模型模块 {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    try:
        spec.loader.exec_module(module)
    except Exception:
        sys.modules.pop(name, None)
        raise
    return module


class FireRedLIDBackend:
    """FireRedLID 887M MLX 后端；同一时间只执行一个推理。"""

    def __init__(
        self, frontend: object | None = None, model: object | None = None
    ) -> None:
        self._frontend = frontend
        self._model = model
        self._lock = threading.Lock()

    def load(self) -> None:
        if not platform_supported():
            raise RuntimeError(
                "FireRedLID MLX 后端仅支持 Apple Silicon 的 macOS（Darwin arm64）"
            )

        directory = model_dir()
        missing = [name for name in REQUIRED_FILES if not (directory / name).is_file()]
        if missing:
            raise RuntimeError(
                f"模型目录 {directory} 缺少 {'、'.join(missing)}；请先下载模型：\n"
                f"  {_download_hint(directory)}"
            )

        import mlx.core as mx

        frontend_module = _load_module(FRONTEND_MODULE_NAME, directory / "frontend.py")
        model_module = _load_module(MLX_MODEL_MODULE_NAME, directory / "mlx_model.py")

        weights = dict(mx.load(str(directory / "model.safetensors")))
        tokens = model_module.FireRedTokenDictionary.load(directory / "dict.txt")
        self._model = model_module.FireRedLIDModel(weights, tokens)
        self._frontend = frontend_module.FireRedFrontend(directory / "cmvn.ark")

    def _run_model(self, features: np.ndarray) -> list[tuple[str, float]]:
        """执行一次 MLX 推理，返回 (原始标签, 置信度) 列表。"""
        import mlx.core as mx

        hypotheses = self._model.identify_features(
            mx.array(features[None]), beam_size=BEAM_SIZE, nbest=NBEST
        )[0]
        return [
            (hypothesis.text, float(hypothesis.confidence))
            for hypothesis in hypotheses
        ]

    def detect(self, pcm: np.ndarray) -> list[tuple[str, str | None, float]]:
        """16kHz 单声道 float32 PCM → 按置信度降序的 (语言, 方言, 置信度) 列表。"""
        # 前端（kaldi fbank）与模型都有内部状态，整段推理串行执行。
        with self._lock:
            features = self._frontend.extract_pcm(pcm, SAMPLE_RATE)
            hypotheses = self._run_model(features)
        results = [
            (*normalize_label(text), confidence)
            for text, confidence in hypotheses
        ]
        return sorted(results, key=lambda item: item[2], reverse=True)


def create_backend() -> LIDBackend:
    """子服务使用的后端工厂。"""
    return FireRedLIDBackend()
