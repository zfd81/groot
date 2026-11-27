"""语种标签归一化：模型文本标签 → ISO 639-1 语言码与可选方言。"""

from __future__ import annotations

import re

DIALECTS: set[str] = {
    "mandarin",
    "yue",
    "wu",
    "xiang",
    "xinan",
    "north",
    "min",
}

UNKNOWN_LANGUAGE = "und"

_LANGUAGE_RE = re.compile(r"^[a-z]{2}$")


def normalize_label(text: str) -> tuple[str, str | None]:
    """把 ``zh mandarin``、``en`` 这类假设文本拆成 (语言, 方言)。

    首个字段必须是两字母小写语言码，否则返回 ``("und", None)``；第二个字段
    只有在 :data:`DIALECTS` 中才作为方言返回，否则为 ``None``。
    """
    tokens = text.split()
    if not tokens:
        return UNKNOWN_LANGUAGE, None

    first = tokens[0]
    if not _LANGUAGE_RE.match(first):
        return UNKNOWN_LANGUAGE, None  # 语言无法判定时方言没有意义
    dialect = tokens[1] if len(tokens) > 1 and tokens[1] in DIALECTS else None
    return first, dialect
