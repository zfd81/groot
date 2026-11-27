"""SDK 必须能在宿主接受的最低 Python 版本（3.9）上导入。

3.9 在 def 时求值函数注解，``str | None`` 会抛 TypeError；所有模块必须用
``from __future__ import annotations`` 推迟求值。本机通常没有 3.9，用 AST 静态检查。
"""

import ast
from pathlib import Path

import groot_plugin_sdk

SDK_DIR = Path(groot_plugin_sdk.__file__).parent


def _has_future_annotations(tree: ast.Module) -> bool:
    for node in tree.body:
        if isinstance(node, ast.ImportFrom) and node.module == "__future__":
            if any(alias.name == "annotations" for alias in node.names):
                return True
    return False


def _uses_pep604_union(tree: ast.Module) -> bool:
    return any(
        isinstance(node, ast.BinOp) and isinstance(node.op, ast.BitOr)
        for node in ast.walk(tree)
    )


def test_every_module_with_union_annotations_defers_evaluation():
    offenders = []
    for path in sorted(SDK_DIR.glob("*.py")):
        tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
        if _uses_pep604_union(tree) and not _has_future_annotations(tree):
            offenders.append(path.name)
    assert offenders == [], f"缺少 from __future__ import annotations: {offenders}"
