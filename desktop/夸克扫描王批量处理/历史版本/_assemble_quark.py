# -*- coding: utf-8 -*-
"""组装 QuarkBatch.exe：Windows 单文件 PE
   [Zig 编译的 GUI 启动器][payload.zip][32B CRC 尾]
   只打 Qt Widgets + Qt WebEngine 依赖闭包，不带 OCR / Office 解析库。
"""
import hashlib
import io
import json
import re
import struct
import subprocess
import sys
import zipfile
from pathlib import Path

ROOT = Path(__file__).parent
WHEELS = ROOT / "wheels_q"
EMBED = ROOT / "pe" / "python_embed-3.10.0" / "src3.10" / "python_embed" / "data.zip"
APP = Path("/data/workspace/QuarkBatch/app.py")
OUT = Path("/data/workspace/QuarkBatch.exe")
LAUNCHER_SRC = ROOT / "launcher_q.c"

QT_KEEP = set(json.loads((ROOT / "qt_keep.json").read_text()))
WEB_RES_KEEP = {
    "qtwebengine_resources.pak",
    "qtwebengine_resources_100p.pak",
    "qtwebengine_resources_200p.pak",
    "v8_context_snapshot.bin",
}
WEB_LOC_KEEP = {"qtwebengine_zh_CN.qm", "zh-CN.pak", "en-US.pak"}

DROP_DIR = re.compile(
    r"(^|/)(mupdf-devel|include|examples|glsl|support|__pycache__|tests?"
    r"|demos?|docs?|licenses?|typesystems|metatypes|designer)(/|$)", re.I)
DROP_FILE = re.compile(r"\.(pyi|lib|pdb|chm|html?|md|rst|ts)$", re.I)
KEEP_TXT = {"python310._pth", "LICENSE.txt", "METADATA", "RECORD", "WHEEL",
            "top_level.txt", "entry_points.txt", "LICENSE", "NOTICE"}


def crc32(data: bytes) -> int:
    import zlib
    return zlib.crc32(data) & 0xFFFFFFFF


def step(msg):
    print(f"[*] {msg}", flush=True)


def dropped(rel: str) -> bool:
    parts = rel.split("/")
    name = parts[-1]
    # PySide6 根目录下的二进制：只留 WebEngine 依赖闭包
    if (len(parts) == 4 and parts[:3] == ["Lib", "site-packages", "PySide6"]
            and name.lower().endswith((".dll", ".pyd", ".exe"))):
        return name not in QT_KEEP
    # 我们用 Widgets 而非 QML 界面，砍掉 QtQuick/3D/Charts 等上千个 QML 组件；
    # 只留 WebEngine 自己那点 QML 注册，避免个别版本初始化时找不到。
    if "PySide6/qml/" in rel:
        sub = rel.split("PySide6/qml/", 1)[1]
        return not (sub.startswith("QtWebEngine/") or
                    sub.startswith("QtWebChannel/"))
    # Qt 插件：TLS（HTTPS 必需）、窗口平台、图像、输入法、网络信息都留着，
    # 其余 3D/数据库/多媒体/传感器/设计器插件一律砍。
    if "PySide6/plugins/" in rel:
        sub = rel.split("PySide6/plugins/", 1)[1]
        return not (sub == "platforms/qwindows.dll" or
                    sub.startswith(("imageformats/", "tls/", "platforminputcontexts/",
                                    "iconengines/", "networkinformation/", "styles/")))
    # WebEngine 资源：丢 debug 包、devtools，只留运行必需的 4 个
    if "PySide6/resources/" in rel:
        if ".debug." in name:
            return True
        return name not in WEB_RES_KEEP
    # 语言包：只留中文 + en-US
    if "PySide6/translations/" in rel:
        return name not in WEB_LOC_KEEP
    if rel.startswith("Lib/site-packages/") and rel.endswith(".dist-info/RECORD"):
        return True
    if DROP_DIR.search("/" + rel):
        return True
    if DROP_FILE.search(name) and name not in KEEP_TXT:
        return True
    if name.lower().endswith(".qm"):
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

    step("收集依赖 wheels（Qt Widgets + WebEngine 闭包）")
    for whl in sorted(WHEELS.glob("*.whl")):
        cnt = 0
        with zipfile.ZipFile(whl) as z:
            for n in z.namelist():
                if n.endswith("/"):
                    continue
                entries["Lib/site-packages/" + n] = z.read(n)
                cnt += 1
        print(f"    + {whl.name}  ({cnt})")

    step("写入主程序 app.py")
    entries["app.py"] = APP.read_bytes()

    # VC++ 运行库放根目录（与 pythonw.exe 同目录）
    for whl in WHEELS.glob("shiboken6*.whl"):
        with zipfile.ZipFile(whl) as z:
            for n in z.namelist():
                if n.lower().endswith(".dll") and Path(n).name.lower() in (
                        "msvcp140.dll", "vcamp140.dll", "vccorlib140.dll",
                        "concrt140.dll", "msvcp140_1.dll", "msvcp140_2.dll",
                        "vcruntime140.dll", "vcruntime140_1.dll"):
                    entries[Path(n).name] = z.read(n)

    step("裁剪非运行必需文件")
    before = len(entries)
    raw_mb = sum(len(v) for v in entries.values()) / 1024 / 1024
    entries = {k: v for k, v in entries.items() if not dropped(k)}
    print(f"    {before} -> {len(entries)} 项，"
          f"{raw_mb:.1f} MB -> {sum(len(v) for v in entries.values())/1024/1024:.1f} MB (未压缩)")
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
    exe = ROOT / "launcher_q.exe"
    cmd = [sys.executable, "-m", "ziglang", "cc",
           "-target", "x86_64-windows-gnu", "-Os",
           "-Wl,--subsystem,windows",
           str(LAUNCHER_SRC), "-o", str(exe),
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
    # trailer 前置：紧跟 PE stub 之后，从文件开头搜 FAR1
    tp = -1
    start = 0
    while True:
        j = blob.find(b"FAR1", start, 256 * 1024)
        if j < 0:
            break
        if j + 32 <= len(blob):
            sz = struct.unpack_from("<Q", blob, j + 4)[0]
            if sz == len(payload):
                tp = j
                break
        start = j + 1
    assert tp >= 0, "前置校验信息缺失"
    size, crc, expected = struct.unpack_from("<QQQ", blob, tp + 4)
    assert size == len(payload), "payload 长度不符"
    assert crc == pcrc == crc32(payload), "CRC32 不符"
    assert expected == len(blob), "期望总大小不符"
    assert tp + 32 + size == len(blob), "布局不符（trailer 后应紧跟 payload）"
    print(f"    trailer 前置偏移 = {tp}，期望总大小 = {expected:,}")
    with zipfile.ZipFile(io.BytesIO(payload)) as z:
        names = set(z.namelist())
        need = ("pythonw.exe", "python310.dll", "python310.zip", "app.py",
                "python310._pth",
                "Lib/site-packages/PySide6/QtWidgets.pyd",
                "Lib/site-packages/PySide6/Qt6Widgets.dll",
                "Lib/site-packages/PySide6/plugins/platforms/qwindows.dll",
                "Lib/site-packages/PySide6/QtWebEngineWidgets.pyd",
                "Lib/site-packages/PySide6/QtWebEngineCore.pyd",
                "Lib/site-packages/PySide6/Qt6WebEngineCore.dll",
                "Lib/site-packages/PySide6/Qt6WebEngineWidgets.dll",
                "Lib/site-packages/PySide6/QtWebEngineProcess.exe",
                "Lib/site-packages/PySide6/resources/qtwebengine_resources.pak",
                "Lib/site-packages/PySide6/resources/v8_context_snapshot.bin",
                "Lib/site-packages/PySide6/translations/qtwebengine_locales/zh-CN.pak",
                "Lib/site-packages/pymupdf/_mupdf.pyd",
                "msvcp140.dll", "vcruntime140.dll")
        for k in need:
            assert k in names, f"缺少 {k}"
        # 确认没有把 debug 资源 / 多余语言包打进去
        bad = [n for n in names if not n.endswith("/") and
               (".debug." in n or
                ("PySide6/qml/" in n and not n.split("PySide6/qml/", 1)[1]
                 .startswith(("QtWebEngine/", "QtWebChannel/"))) or
                "PySide6/plugins/sqldrivers/" in n or
                "PySide6/plugins/sceneparsers/" in n or
                ("qtwebengine_locales/" in n and
                 Path(n).name not in ("zh-CN.pak", "en-US.pak")))]
        assert not bad, f"裁剪漏网: {bad[:5]}"
        assert any("PySide6/plugins/tls/" in n for n in names), "缺 TLS 插件（网页会打不开 https）"
        assert "Lib/site-packages/PySide6/plugins/platforms/qwindows.dll" in names
        print(f"    payload {len(names)} 项，WebEngine 运行时齐全且已精简")
    print("    静态校验全部通过 ✔")


def main():
    entries = collect()
    payload = make_zip(entries)
    stub = compile_launcher()
    pcrc = crc32(payload)
    expected = len(stub) + 32 + len(payload)
    trailer = (struct.pack("<4sQQQ", b"FAR1", len(payload), pcrc, expected)
               + b"\0" * 4)
    assert len(trailer) == 32
    # 校验信息前置：末尾被追加任何数据都不影响启动；被截断则精确报缺多少字节
    blob = stub + trailer + payload
    OUT.write_bytes(blob)
    print("\n================ 交付信息 ================")
    print(f"文件          : {OUT}")
    print(f"大小          : {len(blob):,} 字节 ({len(blob)/1024/1024:.1f} MB)")
    print(f"PE 启动器     : {len(stub):,} 字节")
    print(f"内置 payload  : {len(payload):,} 字节 ({len(payload)/1024/1024:.1f} MB)")
    print(f"payload CRC32 : {pcrc:08X}")
    print(f"整文件 CRC32  : {crc32(blob):08X}")
    print(f"整文件 SHA256 : {hashlib.sha256(blob).hexdigest().upper()}")
    print("=========================================\n")
    verify(blob, payload, pcrc)


if __name__ == "__main__":
    main()
