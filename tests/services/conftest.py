"""tests/services 的共享导入路径。

让 ``pytest tests/services`` 无需手动设置 ``PYTHONPATH`` 即可导入被测代码：

- ``groot_service_sdk``：来自 ``internal/pysvc/sdk``（随 Groot 嵌入的 SDK 源码）
- ``app``：来自 ``services/asr-router``（子服务自身的包）
"""

from __future__ import annotations

import sys
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parents[2]

_PATHS = (
    _REPO_ROOT / "internal" / "pysvc" / "sdk",
    _REPO_ROOT / "services" / "asr-router",
)

for _path in _PATHS:
    _value = str(_path)
    if _value not in sys.path:
        sys.path.insert(0, _value)
