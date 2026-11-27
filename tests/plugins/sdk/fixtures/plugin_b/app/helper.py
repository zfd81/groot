"""plugin_b 的内部模块；由 main 以相对导入引用，验证 app 包注册。"""


def is_boom(kind: str) -> bool:
    return kind == "boom"
