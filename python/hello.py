"""最小可运行示例：任何 Python 工具都可以从这里改起。"""
from __future__ import annotations


def greet(name: str = "world") -> str:
    return f"Hello, {name}!"


if __name__ == "__main__":
    print(greet())
