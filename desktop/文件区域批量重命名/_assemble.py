# -*- coding: utf-8 -*-
"""在 Linux 侧组装出真正的 Windows 单文件 exe（PE）：
   [Zig 编译的 GUI 启动器][payload.zip][32B CRC 尾]
   纯内存收集，不落盘解包。"""
import hashlib
import io
import re
import struct
import subprocess
import sys
import zipfile
from pathlib import Path

ROOT = Path(__file__).parent
WHEELS = ROOT / "wheels"
EMBED = ROOT / "pe" / "python_embed-3.10.0" / "src3.10" / "python_embed" / "data.zip"
APP = Path("/data/workspace/FileAreaRenamer/app.py")
OUT = Path("/data/workspace/FileAreaRenamer.exe")

DROP_DIR = re.compile(
    r"(^|/)(mupdf-devel|include|examples|qml|translations|typesystems|glsl|support"
    r"|__pycache__|tests?|demos?|docs?|licenses?)(/|$)", re.I)
DROP_FILE = re.compile(r"\.(pyi|lib|pdb|chm|html?|md|rst|qm|qml|ts)$", re.I)
DROP_EXACT = {
    "Lib/site-packages/PySide6/resources/icudtl.dat",
    "Lib/site-packages/PySide6/QtDesigner.pyd",
    "Lib/site-packages/PySide6/Qt6Designer.dll",
    "Lib/site-packages/PySide6/QtUiTools.pyd",
    "Lib/site-packages/PySide6/Qt6UiTools.dll",
    "Lib/site-packages/PySide6/QtHelp.pyd",
    "Lib/site-packages/PySide6/Qt6Help.dll",
    "Lib/site-packages/PySide6/QtTest.pyd",
    "Lib/site-packages/PySide6/Qt6Test.dll",
}
KEEP_TXT = {"python310._pth", "LICENSE.txt", "METADATA", "RECORD", "WHEEL",
            "top_level.txt", "entry_points.txt", "LICENSE", "NOTICE"}


def crc32(data: bytes) -> int:
    import zlib
    return zlib.crc32(data) & 0xFFFFFFFF


def step(msg):
    print(f"[*] {msg}", flush=True)


def dropped(rel: str) -> bool:
    if rel in DROP_EXACT:
        return True
    if DROP_DIR.search("/" + rel):
        return True
    if rel.startswith("Lib/site-packages/") and rel.endswith(".dist-info/RECORD"):
        return True
    if DROP_FILE.search(rel) and Path(rel).name not in KEEP_TXT:
        return True
    return False


def collect() -> dict:
    step("收集 Windows CPython 3.10 运行时")
    entries = {}
    with zipfile.ZipFile(EMBED) as z:
        for n in z.namelist():
            if n.startswith("cp310/") and not n.endswith("/"):
                entries[n[len("cp310/"):]] = z.read(n)
    entries["python310._pth"] = (
        b"python310.zip\r\n.\r\nLib\\site-packages\r\n\r\nimport site\r\n")

    step("收集依赖 wheels")
    for whl in sorted(WHEELS.glob("*.whl")):
        cnt = 0
        with zipfile.ZipFile(whl) as z:
            for n in z.namelist():
                if n.endswith("/"):
                    continue
                entries["Lib/site-packages/" + n] = z.read(n)
                cnt += 1
        print(f"    + {whl.name}  ({cnt})")

    step("写入主程序 app.py + 内置中文 OCR 模型")
    entries["app.py"] = APP.read_bytes()
    entries["ocr/rec.onnx"] = (ROOT / "ocroot" / "ocr" / "rec.onnx").read_bytes()

    # VC++ 运行库放根目录（与 pythonw.exe 同目录），保证 numpy / onnxruntime 能加载
    for whl in WHEELS.glob("shiboken6*.whl"):
        with zipfile.ZipFile(whl) as z:
            for n in z.namelist():
                if n.lower().endswith((".dll",)) and Path(n).name.lower() in (
                        "msvcp140.dll", "vcamp140.dll", "vccorlib140.dll", "concrt140.dll",
                        "msvcp140_1.dll", "msvcp140_2.dll"):
                    entries[Path(n).name] = z.read(n)

    step("裁剪非运行必需文件")
    before = len(entries)
    entries = {k: v for k, v in entries.items() if not dropped(k)}
    print(f"    {before} -> {len(entries)} 项")
    return entries


def make_zip(entries: dict) -> bytes:
    step("生成 payload.zip")
    mem = io.BytesIO()
    with zipfile.ZipFile(mem, "w", zipfile.ZIP_DEFLATED, compresslevel=6) as z:
        dirs = set()
        for rel in entries:
            parts = rel.split("/")
            for i in range(1, len(parts)):
                dirs.add("/".join(parts[:i]) + "/")
        for d in sorted(dirs):
            zi = zipfile.ZipInfo(d)
            zi.compress_type = zipfile.ZIP_STORED
            zi.external_attr = 0x10 | 0o755 << 16
            z.writestr(zi, b"")
        for rel, data in sorted(entries.items()):
            z.writestr(rel, data, zipfile.ZIP_DEFLATED)
    return mem.getvalue()


def compile_launcher() -> bytes:
    step("Zig 交叉编译 Windows GUI 启动器")
    exe = ROOT / "launcher.exe"
    cmd = [sys.executable, "-m", "ziglang", "cc",
           "-target", "x86_64-windows-gnu", "-Os",
           "-Wl,--subsystem,windows",
           str(ROOT / "launcher.c"), "-o", str(exe),
           "-lkernel32", "-luser32", "-lshell32"]
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        print(r.stdout[-3000:]); print(r.stderr[-3000:])
        raise SystemExit("launcher 编译失败")
    return exe.read_bytes()


def verify(blob: bytes, payload: bytes, pcrc: int):
    step("静态校验产物")
    assert blob[:2] == b"MZ", "不是 PE (MZ)"
    pe = struct.unpack_from("<I", blob, 0x3C)[0]
    assert blob[pe:pe + 4] == b"PE\0\0", "PE 头缺失"
    machine = struct.unpack_from("<H", blob, pe + 4)[0]
    subsystem = struct.unpack_from("<H", blob, pe + 0x5C)[0]
    print(f"    Machine   = 0x{machine:04X}  (x86-64)")
    print(f"    Subsystem = {subsystem}  (2 = WINDOWS_GUI 无黑框)")
    assert machine == 0x8664 and subsystem == 2, "PE 头不符合 Windows GUI x64"
    assert blob[-32:-28] == b"FAR1", "CRC 尾缺失"
    size, crc, ver = struct.unpack_from("<QQQ", blob, len(blob) - 28)
    assert size == len(payload), "payload 长度不符"
    assert crc == pcrc == crc32(payload), "CRC32 不符"
    with zipfile.ZipFile(io.BytesIO(payload)) as z:
        names = set(z.namelist())
        need = ("pythonw.exe", "python310.dll", "python310.zip", "app.py", "python310._pth",
                "Lib/site-packages/PySide6/QtWidgets.pyd",
                "Lib/site-packages/PySide6/Qt6Widgets.dll",
                "Lib/site-packages/PySide6/plugins/platforms/qwindows.dll",
                "Lib/site-packages/pymupdf/_mupdf.pyd",
                "Lib/site-packages/pymupdf/mupdfcpp64.dll",
                "Lib/site-packages/openpyxl/__init__.py",
                "Lib/site-packages/xlrd/__init__.py",
                "Lib/site-packages/docx/__init__.py",
                "Lib/site-packages/win32com/client/__init__.py",
                "Lib/site-packages/pywin32.pth",
                "Lib/site-packages/docx/oxml/ns.py",
                "ocr/rec.onnx",
                "Lib/site-packages/numpy/__init__.py",
                "Lib/site-packages/onnxruntime/capi/onnxruntime_pybind11_state.pyd")
        for k in need:
            assert k in names, f"缺少 {k}"
        print(f"    payload {len(names)} 项，关键运行时全部齐全")
    print("    静态校验全部通过 ✔")


def main():
    entries = collect()
    payload = make_zip(entries)
    stub = compile_launcher()
    pcrc = crc32(payload)
    trailer = struct.pack("<4sQQQ", b"FAR1", len(payload), pcrc, 1) + b"\0" * 4
    assert len(trailer) == 32
    blob = stub + payload + trailer
    OUT.write_bytes(blob)

    print("\n================ 交付信息 ================")
    print(f"文件          : {OUT}")
    print(f"大小          : {len(blob):,} 字节 ({len(blob)/1024/1024:.1f} MB)")
    print(f"PE 启动器     : {len(stub):,} 字节")
    print(f"内置 payload  : {len(payload):,} 字节")
    print(f"payload CRC32 : {pcrc:08X}")
    print(f"整文件 CRC32  : {crc32(blob):08X}")
    print(f"整文件 SHA256 : {hashlib.sha256(blob).hexdigest().upper()}")
    print("=========================================\n")
    verify(blob, payload, pcrc)


if __name__ == "__main__":
    main()
