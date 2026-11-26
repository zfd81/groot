"""asr-router 语种标签归一化单元测试。"""

from app.labels import DIALECTS, normalize_label


def test_zh_mandarin():
    assert normalize_label("zh mandarin") == ("zh", "mandarin")


def test_en():
    assert normalize_label("en") == ("en", None)


def test_yue():
    assert normalize_label("zh yue") == ("zh", "yue")


def test_unknown_token():
    assert normalize_label("zzz unknown") == ("und", None)


def test_three_tokens_uses_first_two():
    assert normalize_label("zh mandarin extra") == ("zh", "mandarin")


def test_empty_text():
    assert normalize_label("") == ("und", None)


def test_unknown_dialect_omitted():
    assert normalize_label("en us") == ("en", None)


def test_dialects_contains_required_labels():
    assert {"mandarin", "yue", "wu", "xiang", "xinan", "north", "min"} <= DIALECTS


def test_unknown_language_never_carries_dialect():
    # 语言无法判定时方言没有意义，即使第二个字段在方言表里。
    assert normalize_label("zzz mandarin") == ("und", None)
