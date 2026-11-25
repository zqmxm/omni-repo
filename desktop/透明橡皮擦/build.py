"""一键打包 Windows 单文件 exe。

用法：python desktop/透明橡皮擦/build.py
产物：desktop/透明橡皮擦/dist/透明橡皮擦.exe
"""
from __future__ import annotations

import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent
ENTRY = ROOT / "src" / "main.py"


def main() -> int:
    command = [
        sys.executable,
        "-m",
        "PyInstaller",
        "--noconfirm",
        "--clean",
        "--onefile",
        "--windowed",
        "--name",
        "透明橡皮擦",
        "--distpath",
        str(ROOT / "dist"),
        "--workpath",
        str(ROOT / "build"),
        "--specpath",
        str(ROOT / "build"),
        str(ENTRY),
    ]
    print("构建命令：", " ".join(command), flush=True)
    completed = subprocess.run(command, cwd=ROOT, check=False)
    exe = ROOT / "dist" / "透明橡皮擦.exe"
    if completed.returncode == 0 and exe.exists():
        print(f"构建完成：{exe}（{exe.stat().st_size / 1024 / 1024:.1f} MB）")
        return 0
    print("构建失败，请检查上面的 PyInstaller 输出")
    return completed.returncode or 1


if __name__ == "__main__":
    raise SystemExit(main())
