# -*- coding: utf-8 -*-
"""
文件固定位置批量重命名工具  FileAreaRenamer  v3.9
=========================================================
两套模式，按「模板文件」自动切换：

【模式 ① PDF / Word】页面框选 + OCR
    PDF 直接用；Word（.doc/.docx）优先 LibreOffice/Word 原排版转 PDF，
    没有装 Office 时自动内置抽取 .doc/.docx 正文重排成页面 PDF，
    **不需要任何外部程序，.doc 一定有文字可看可框选**。
    优先尝试 LibreOffice / Word / WPS COM 导出「原排版」；都不行时走内置重排，
    且中英文分开用对应字体渲染、表格单元格/软换行不再断行，数字不会被拉成散字。
    预览是页面图，鼠标拖出框选区域；每个文件只对自己框内的图像
    做内置中文 OCR，结果带置信度，可在表格里核对 / 直接改。

【模式 ② Excel（.xls 与 .xlsx 都支持）】按 A1 / B1 / C2 选格子，格子里是什么就命名什么
    预览 1:1 还原原表：合并单元格、列宽、行高、黄色底色、居中、
    粗体、换行、数字格式（1.00 / 91.88%），文字完整不省略。
    绝不转 PDF，中文由 Qt 直接绘制，不会乱码。
    点选格子 / 拖选范围 / 直接填地址 / 从「单元格清单」双击选。
    其他 xlsx/xls 读自己同一个格子的显示值。

同批里模式不一致的文件标记「类型不符·跳过」。
导入方式：按钮选择 / 直接把文件、文件夹从资源管理器拖进窗口（支持一次多个）。
固定行为：识别为空的文件一律跳过（保持原名）；框选坐标改动自动生效，无需点按钮。
导入性能：主线程只扫路径、列表立即显示；Excel 工作表名与 Word 转 PDF 全在后台；
          LibreOffice / WPS COM 整批只启动一次（不再逐文件冷启动），
          COM 可用性与转换方式全局只探测一次，已读文件不会被重复读取。
OCR 增强：框边若切到半个字，自动只向外补到该字笔画结束，把完整的字带进来识别；
          自动擦除框内表格线、长框分片识别、多候选投票去掉幻觉尾字，并用框内
          文字层只做「去多识别的尾字」校验；识别内容列可双击手工校正。
"""

from __future__ import annotations

import difflib
import hashlib
import math
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time as _time
import zlib
from dataclasses import dataclass, field
from datetime import date, datetime, time as dtime
from pathlib import Path

from PySide6.QtCore import Qt, QRect, QPoint, QThread, Signal, QTimer, QEvent, QObject
from PySide6.QtGui import QPixmap, QImage, QPainter, QPen, QColor, QFont
from PySide6.QtWidgets import (
    QApplication, QMainWindow, QWidget, QLabel, QPushButton, QListWidget,
    QListWidgetItem, QTableWidget, QTableWidgetItem, QComboBox, QSpinBox,
    QDoubleSpinBox, QLineEdit, QCheckBox, QFileDialog,
    QMessageBox, QScrollArea, QSplitter, QHBoxLayout, QVBoxLayout,
    QGroupBox, QHeaderView, QAbstractItemView, QStatusBar, QProgressBar,
    QStackedWidget, QTableWidgetSelectionRange, QDialog, QDialogButtonBox,
)

try:
    import pymupdf as fitz
except ImportError:
    import fitz

SUPPORTED = {".pdf", ".doc", ".docx", ".xls", ".xlsx"}
WORD_EXT = {".doc", ".docx"}
EXCEL_EXT = {".xls", ".xlsx"}
BUNDLE_DIR = Path(os.environ.get("FAR_BUNDLE_DIR", str(Path(__file__).parent)))
OCR_MODEL = BUNDLE_DIR / "ocr" / "rec.onnx"
APP_TMP = Path(tempfile.gettempdir()) / "FileAreaRenamer"
APP_TMP.mkdir(parents=True, exist_ok=True)

INVALID_CHARS = re.compile(r'[<>:"/\\|?*\x00-\x1f]')
WIN_RESERVED = {"CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4",
                "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2",
                "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"}
NOWINDOW = getattr(subprocess, "CREATE_NO_WINDOW", 0)


# ============================ 基础工具 ============================
def _token(*parts) -> str:
    return hashlib.md5("|".join(str(p) for p in parts).encode("utf-8", "ignore")).hexdigest()[:12]


def clean_name(s: str, maxlen: int = 80) -> str:
    s = INVALID_CHARS.sub("", s or "")
    s = re.sub(r"\s+", " ", s).strip().strip(". ")
    if s.upper() in WIN_RESERVED:
        s = "_" + s
    return s[:maxlen].rstrip()


def col_name(i: int) -> str:
    s = ""
    i += 1
    while i:
        i, rem = divmod(i - 1, 26)
        s = chr(65 + rem) + s
    return s


def col_index(s: str) -> int:
    n = 0
    for ch in s.upper():
        n = n * 26 + (ord(ch) - 64)
    return n - 1


_SOFFICE_UNSET = object()
_SOFFICE_CACHE = _SOFFICE_UNSET


def find_soffice() -> str | None:
    """结果缓存：批量导入时每个文件都扫一遍磁盘会是纯浪费"""
    global _SOFFICE_CACHE
    if _SOFFICE_CACHE is not _SOFFICE_UNSET:
        return _SOFFICE_CACHE
    env_dir = os.environ.get("FAR_EXE_DIR", "").strip()
    if env_dir and Path(env_dir).exists():
        base = Path(env_dir)
    elif getattr(sys, "frozen", False):
        base = Path(sys.executable).parent
    else:
        base = Path(__file__).parent
    found = None
    for c in [base / "libreoffice" / "program" / "soffice.exe",
              base / "LibreOffice" / "program" / "soffice.exe",
              Path(r"C:\Program Files\LibreOffice\program\soffice.exe"),
              Path(r"C:\Program Files (x86)\LibreOffice\program\soffice.exe"),
              Path("/usr/bin/soffice"), Path("/usr/bin/libreoffice")]:
        if c.exists():
            found = str(c); break
    if not found:
        found = (shutil.which("soffice") or shutil.which("libreoffice")
                 or shutil.which("soffice.exe"))
    _SOFFICE_CACHE = found
    return found


def kind_of(path: Path) -> str:
    e = path.suffix.lower()
    if e == ".pdf":
        return "pdf"
    if e in WORD_EXT:
        return "word"
    if e in EXCEL_EXT:
        return "excel"
    return "other"


def mode_of(kind: str) -> str:
    return "page" if kind in ("pdf", "word") else "grid"


def sheet_names(path: Path) -> list[str]:
    try:
        if path.suffix.lower() == ".xlsx":
            import openpyxl
            wb = openpyxl.load_workbook(path, read_only=True, data_only=True)
            n = list(wb.sheetnames)
            wb.close()
            return n
        import xlrd
        for kw in ({"on_demand": True}, {"formatting_info": True}, {}):
            try:
                bk = xlrd.open_workbook(str(path), **kw)
                n = bk.sheet_names()
                try: bk.release_resources()
                except Exception: pass
                if n:
                    return n
            except Exception:
                continue
        return []
    except Exception:
        return []


# ============================ Word：文本抽取 + 工作 PDF ============================
WORD_PDF_STATE: dict[str, str] = {}     # 文件路径 -> "原排版" / "内置文本重排"


def _clean_doc_text(s: str) -> list[str]:
    """把 Word 文本流整理成行。

    WPS/Word 里 \\x07 是表格单元格结束、\\x0b 是软换行，两者都**不是**段落边界。
    旧版把它们一律当换行，于是「项目名称」「：」「内容」被拆成三行、
    冒号和括号单独占一行，看起来像乱码。这里改成同行空格。
    """
    if not s:
        return []
    out = []
    for ch in s:
        o = ord(ch)
        if ch in ("\r", "\n", "\x0c"):                 # 真段落 / 分页
            out.append("\n")
        elif ch in ("\x0b", "\x07", "\t", "\u00a0"):   # 同行分隔
            out.append(" ")
        elif o < 32 or ch in ("\x13", "\x14", "\x15", "\x01", "\x02", "\x08", "\ufffd"):
            continue
        else:
            out.append(ch)
    lines = []
    for x in "".join(out).split("\n"):
        x = re.sub(r" {2,}", " ", x).strip()
        # 「项目名称 ： 内容」→「项目名称：内容」，单元格分隔空格不该留在标点两侧
        x = re.sub(r"\s+([：:，,。、；;）)】\]])", r"\1", x)
        x = re.sub(r"([：:（(\[【])\s+", r"\1", x)
        if x:
            lines.append(x)
    # 把 WPS 表单里被拆开的「：」「）」「0 0」等碎片并回上一行，
    # 冒号结尾的行再接上下一行（冒号后面通常就是值）。最多连并 2 次，防止整篇粘成一行。
    merged: list[str] = []
    for x in lines:
        if merged and len(merged[-1]) < 200:
            frag = len(x) <= 2 and not re.search(r"[A-Za-z\u4e00-\u9fff]", x)
            tail = merged[-1].endswith(("：", ":", "（", "("))
            if frag or tail:
                merged[-1] = (merged[-1] + x).strip()
                continue
        merged.append(x)
    return merged


def _doc_text_piece_table(wd: bytes, tbl: bytes) -> str:
    """按 FIB → CLX → PlcPcd 精确还原 .doc 正文（ANSI/GBK 与 UTF-16 片段都支持）"""
    import struct
    if len(wd) < 64 or struct.unpack_from("<H", wd, 0)[0] != 0xA5EC:
        raise ValueError("不是 Word97 .doc")
    flags = struct.unpack_from("<H", wd, 0x0A)[0]
    pos = 0x20
    csw = struct.unpack_from("<H", wd, pos)[0]; pos += 2 + csw * 2
    cslw = struct.unpack_from("<H", wd, pos)[0]; pos += 2 + cslw * 4
    cb_rg = struct.unpack_from("<H", wd, pos)[0]; pos += 2
    if cb_rg < 34:
        raise ValueError("FIB 过短")
    fc_clx = struct.unpack_from("<I", wd, pos + 33 * 8)[0]
    lcb_clx = struct.unpack_from("<I", wd, pos + 33 * 8 + 4)[0]
    clx = tbl[fc_clx:fc_clx + lcb_clx]
    # 跳过 Prc，找到 Pcdt(0x02)
    i = 0
    while i < len(clx) and clx[i] == 0x01:
        cb = struct.unpack_from("<H", clx, i + 1)[0]
        i += 3 + cb
    if i >= len(clx) or clx[i] != 0x02:
        raise ValueError("CLX 无 piece table")
    lcb_plc = struct.unpack_from("<I", clx, i + 1)[0]
    plc = clx[i + 5:i + 5 + lcb_plc]
    n = (len(plc) - 4) // 12
    if n <= 0:
        raise ValueError("piece table 为空")
    cps = [struct.unpack_from("<I", plc, k * 4)[0] for k in range(n + 1)]
    pcd_base = 4 * (n + 1)
    parts = []
    for k in range(n):
        fc = struct.unpack_from("<I", plc, pcd_base + k * 8 + 2)[0]
        compressed = bool(fc & 0x40000000)
        off = fc & 0x3FFFFFFF
        ln = max(0, cps[k + 1] - cps[k])
        if compressed:
            raw = wd[off:off + ln]
            try:
                parts.append(raw.decode("gbk"))
            except Exception:
                parts.append(raw.decode("cp1252", "replace"))
        else:
            raw = wd[off:off + ln * 2]
            parts.append(raw.decode("utf-16-le", "replace"))
    return "".join(parts)


def _doc_text_heuristic(data: bytes) -> str:
    """olefile 不可用时的最后兜底：扫描 UTF-16LE / GBK 可打印串"""
    chunks = []
    # UTF-16LE 连续可打印（中文 / ASCII）
    i = 0
    while i + 1 < len(data):
        lo, hi = data[i], data[i + 1]
        if hi == 0 and (32 <= lo < 127):
            start_i = i
            s = bytearray()
            while i + 1 < len(data):
                lo, hi = data[i], data[i + 1]
                if hi == 0 and 32 <= lo < 127:
                    s += bytes([lo, 0]); i += 2
                elif (0x4E00 <= (hi << 8 | lo) <= 0x9FFF):
                    s += bytes([lo, hi]); i += 2
                else:
                    break
            if len(s) >= 8:
                chunks.append(s.decode("utf-16-le", "ignore"))
            if i == start_i:          # 一个字符都没吃到，必须推进否则死循环
                i += 2
            continue
        i += 2
    text = "\n".join(chunks)
    if len(text) >= 8:
        return text
    # GBK 扫描
    try:
        t = data.decode("gbk", "ignore")
        return "\n".join(x for x in re.findall(r"[\u4e00-\u9fff\w ,.:：/（）()\-]{4,}", t))
    except Exception:
        return ""


def doc_text_lines(path: Path) -> list[str]:
    """.doc / .docx 正文抽取：优先原格式解析，失败用启发式，绝不返回空"""
    if path.suffix.lower() == ".docx":
        try:
            import docx
            d = docx.Document(str(path))
            lines = [p.text for p in d.paragraphs]
            for t in d.tables:
                for row in t.rows:
                    lines.append(" ".join(c.text for c in row.cells))
            clean = _clean_doc_text("\n".join(lines))
            if clean:
                return clean
        except Exception:
            pass
        # zip 兜底：直接读 word/document.xml
        try:
            import zipfile
            with zipfile.ZipFile(path) as z:
                xml = z.read("word/document.xml").decode("utf-8", "ignore")
            xml = re.sub(r"</w:p>", "\n", xml)
            txt = re.sub(r"<[^>]+>", "", xml)
            clean = _clean_doc_text(txt)
            if clean:
                return clean
        except Exception:
            pass
        return []

    # ---- .doc：OLE2 ----
    try:
        import olefile
        ole = olefile.OleFileIO(str(path))
        streams = ["/".join(x) for x in ole.listdir()]
        lower = {s.lower(): s for s in streams}
        wd_name = lower.get("worddocument", "WordDocument")
        wd = ole.openstream(wd_name).read()
        import struct as _st
        flags = _st.unpack_from("<H", wd, 0x0A)[0]
        tname = "1Table" if ((flags >> 9) & 1) else "0Table"
        tname = lower.get(tname.lower(),
                          lower.get("1table", lower.get("0table", "")))
        tbl = ole.openstream(tname).read() if tname else b""
        ole.close()
        try:
            text = _doc_text_piece_table(wd, tbl)
        except Exception:
            text = _doc_text_heuristic(wd)
    except Exception:
        try:
            text = _doc_text_heuristic(path.read_bytes())
        except Exception:
            text = ""
    clean = _clean_doc_text(text)
    if clean:
        return clean
    # 仍为空：从 WordDocument 流里再扫一次 UTF-16 中文
    try:
        return _clean_doc_text(_doc_text_heuristic(path.read_bytes()))
    except Exception:
        return []


def doc_text_to_pdf(lines: list[str], out: Path) -> Path:
    """内置自绘：把抽出的正文按行重排成带文本层的工作 PDF。

    中文段用内置 china-s、ASCII 段用 helv 分别插入：
    旧版整行都用 china-s，ASCII 会按全角宽度画，于是「2026」被拉成「2 0 2 6」、
    「26A99ZJWZ091002242」被拉成一串散字，看着就像乱码。
    """
    import pymupdf as _fitz
    W, H, M, FS, LH = 595.0, 842.0, 52.0, 13.0, 21.0
    CN_W, EN_W = FS * 1.0, FS * 0.55
    doc = _fitz.open()
    page = doc.new_page(width=W, height=H)
    y = M + FS
    usable = W - 2 * M

    def flush(cur):
        nonlocal page, y
        if not cur:
            return
        if y + LH > H - M:
            page = doc.new_page(width=W, height=H); y = M + FS
        x = M
        i = 0
        while i < len(cur):
            kind = cur[i][0]
            s = ""
            while i < len(cur) and cur[i][0] == kind:
                s += cur[i][1]; i += 1
            fn = "china-s" if kind == "cn" else "helv"
            page.insert_text((x, y), s, fontsize=FS, fontname=fn)
            x += len(s) * (CN_W if kind == "cn" else EN_W)
        y += LH

    for line in lines:
        cur, w = [], 0.0
        for ch in line:
            kind = "cn" if ord(ch) > 0x2E80 else "en"
            cw = CN_W if kind == "cn" else EN_W
            if w + cw > usable and cur:
                flush(cur); cur, w = [], 0.0
            cur.append((kind, ch)); w += cw
        flush(cur)
    if doc.page_count == 0:
        doc.new_page(width=W, height=H)
    doc.save(out); doc.close()
    return out


def _pdf_has_text(p: Path) -> bool:
    """确认转出来的 PDF 不是空白/纯图（有文字层才认作原排版成功）"""
    try:
        d = fitz.open(p)
        has = any(d[i].get_text("text").strip()
                  for i in range(min(d.page_count, 3)))
        d.close()
        return has
    except Exception:
        return True          # 打不开就先当成功，交给后面 OCR 判断


# COM 探测结果全局缓存：整个程序只探测一次。
# 旧版每个 Word 文件都试 6 ProgID × 2 Dispatch × 4 方法 = 48 次，
# 文件夹里几十个文件就是几分钟（WPS 未注册时每次 Dispatch 还会卡顿/拉起进程）。
_COM_PROBE = {"done": False, "progid": "", "maker": "", "mname": "", "extra": ()}
_COM_DEADLINE = [0.0]     # 首次探测的绝对截止时刻（批量导入时收紧到 12 秒）
_LO_BATCH_OK = True       # 批量 LO 转换失败过就不再逐文件重试


def _com_word_to_pdf(path: Path, out: Path) -> str:
    """Windows 上试 Word / WPS COM 导出原排版 PDF；探测结果全局只做一次。"""
    if sys.platform != "win32":
        return ""
    P = _COM_PROBE
    if P["done"] and not P["progid"]:
        return ""                       # 已确认这台机器 COM 不通，立即跳过
    try:
        import pythoncom
        import win32com.client as win32
    except Exception:
        P["done"] = True; P["progid"] = ""
        return ""
    progids = ("Word.Application", "KWPS.Application", "KWps.Application",
               "kwps.Application", "WPS.Application", "wps.Application")
    makers = ("DispatchEx", "Dispatch")
    methods = (("ExportAsFixedFormat", (17,)),
               ("ExportAsFixedFormat", (0,)),
               ("SaveAs", (17,)),
               ("SaveAs2", (17,)))
    pairs = ([(P["progid"], P["maker"])] if P["done"] else
             [(a, b) for a in progids for b in makers])
    deadline = (_COM_DEADLINE[0] if _COM_DEADLINE[0] > _time.monotonic()
                else _time.monotonic() + 25.0)
    for progid, maker in pairs:
        if not P["done"] and _time.monotonic() > deadline:
            break
        app = doc = None
        inited = False
        try:
            pythoncom.CoInitialize(); inited = True
            app = getattr(win32, maker)(progid)
            try:
                app.Visible = False
            except Exception:
                pass
            doc = app.Documents.Open(str(path), ReadOnly=True)
            tries = ([(P["mname"], P["extra"])] if P["done"] else methods)
            for mname, extra in tries:
                try:
                    getattr(doc, mname)(str(out), *extra)
                except Exception:
                    continue
                if out.exists() and out.stat().st_size > 0 and _pdf_has_text(out):
                    P.update(done=True, progid=progid, maker=maker,
                             mname=mname, extra=extra)
                    return progid
                try:
                    out.unlink()
                except Exception:
                    pass
        except Exception:
            pass
        finally:
            try:
                if doc is not None:
                    doc.Close(False)
            except Exception:
                pass
            try:
                if app is not None:
                    app.Quit()
            except Exception:
                pass
            if inited:
                try:
                    pythoncom.CoUninitialize()
                except Exception:
                    pass
    if not P["done"]:
        P.update(done=True, progid="", maker="", mname="", extra=())
    return ""


_WORD_VER = "v3.9"


def _word_paths(path: Path):
    key = str(path.resolve())
    stamp = _token(_WORD_VER + "|" + key)
    return key, stamp, APP_TMP / ("wpdf_" + stamp + ".pdf")


def batch_word_to_pdf(paths: list[Path]) -> dict[str, Path]:
    """一次启动 LibreOffice 批量转换所有 Word 文件。

    旧版每个文件单独 `soffice --convert-to`（每次冷启动 3~10 秒，timeout 180s），
    文件夹里几十个 doc 就是几分钟。这里只启动一次，全部文件一起传进去。
    返回 {绝对路径: 工作 PDF}，没成功的留给 word_to_pdf 走 COM / 内置重排。
    """
    global _LO_BATCH_OK
    done: dict[str, Path] = {}
    if not paths:
        return done
    so = find_soffice()
    if so and _LO_BATCH_OK:
        todo: list[Path] = []
        seen_stems: set[str] = set()
        for p in paths:
            key, stamp, out = _word_paths(p)
            if out.exists() and out.stat().st_size > 0:
                done[key] = out
                WORD_PDF_STATE.setdefault(key, "已生成（缓存）")
                continue
            if p.stem in seen_stems:
                continue           # 同名不同目录：同一 outdir 会覆盖，留给 COM / 内置
            seen_stems.add(p.stem); todo.append(p)
        if todo:
            tmp = APP_TMP / ("wbatch_" + _token("|".join(str(x) for x in todo)))
            tmp.mkdir(exist_ok=True)
            cmd = [so, f"-env:UserInstallation={(APP_TMP / 'lo_profile').as_uri()}",
                   "--headless", "--nologo", "--nodefault", "--nofirststartwizard",
                   "--convert-to", "pdf", "--outdir", str(tmp)] + [str(p) for p in todo]
            try:
                subprocess.run(cmd, timeout=max(45, min(150, 6 * len(todo))),
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                               creationflags=NOWINDOW)
                ok = 0
                for p in todo:
                    cand = tmp / (p.stem + ".pdf")
                    key, stamp, out = _word_paths(p)
                    if cand.exists() and cand.stat().st_size > 0 and _pdf_has_text(cand):
                        try:
                            shutil.copyfile(cand, out)
                            done[key] = out
                            WORD_PDF_STATE[key] = "原排版（LibreOffice 批量）"
                            ok += 1
                        except Exception:
                            pass
                if ok == 0:
                    _LO_BATCH_OK = False
            except Exception:
                _LO_BATCH_OK = False    # 批量都失败，别再逐文件重试把界面拖死
    # LO 没覆盖到的（没装 / 转换失败 / 同名文件）再走一次 COM 批量
    rest = [p for p in paths if str(p.resolve()) not in done]
    if rest:
        done.update(batch_com_word_to_pdf(rest))
    return done


def batch_com_word_to_pdf(paths: list[Path]) -> dict[str, Path]:
    """一次 WPS/Word COM 会话批量导出；探测结果全局只做一次。

    没有 COM 时第一个文件的探测最多 8 秒，之后所有文件立刻走内置重排，
    不会像旧版那样每个文件都把 48 种组合重试一遍。
    """
    done: dict[str, Path] = {}
    if sys.platform != "win32" or not paths:
        return done
    P = _COM_PROBE
    if P["done"] and not P["progid"]:
        return done
    todo = []
    for p in paths:
        key, stamp, out = _word_paths(p)
        if out.exists() and out.stat().st_size > 0:
            done[key] = out
            WORD_PDF_STATE.setdefault(key, "已生成（缓存）")
        else:
            todo.append((p, key, out))
    if not todo:
        return done
    if not P["done"]:
        # 只有首次需要探测：拿第一个文件试，8 秒内定不了就全局放弃 COM
        p0, k0, o0 = todo[0]
        _COM_DEADLINE[0] = _time.monotonic() + 8.0
        if not _com_word_to_pdf(p0, o0):
            return done
        done[k0] = o0
        WORD_PDF_STATE[k0] = f"原排版（{P['progid']}）"
        jobs = todo[1:]
    else:
        jobs = todo               # 已确认可用，直接一次会话把整批导完
    if not jobs:
        return done
    # 复用同一个 WPS/Word 实例导出全部剩余文件，不再逐个冷启动
    try:
        import pythoncom
        import win32com.client as win32
        pythoncom.CoInitialize()
        app = getattr(win32, P["maker"])(P["progid"])
        try:
            app.Visible = False
        except Exception:
            pass
        for p, key, out in jobs:
            try:
                d = app.Documents.Open(str(p), ReadOnly=True)
                try:
                    getattr(d, P["mname"])(str(out), *P["extra"])
                finally:
                    d.Close(False)
                if out.exists() and out.stat().st_size > 0 and _pdf_has_text(out):
                    done[key] = out
                    WORD_PDF_STATE[key] = f"原排版（{P['progid']}）"
                else:
                    try:
                        out.unlink()
                    except Exception:
                        pass
            except Exception:
                pass
        try:
            app.Quit()
        except Exception:
            pass
        pythoncom.CoUninitialize()
    except Exception:
        pass
    return done


def word_to_pdf(path: Path) -> Path:
    """①缓存命中 → ②Word/WPS COM（探测结果全局只做一次）
    → ③内置文本自绘（很快、一定有字，且中英文不再被拉开）。
    LibreOffice 已由 batch_word_to_pdf 批量处理，这里不再逐文件冷启动。"""
    key, stamp, out = _word_paths(path)
    if out.exists() and out.stat().st_size > 0:
        WORD_PDF_STATE.setdefault(key, "已生成（缓存）")
        return out
    progid = _com_word_to_pdf(path, out)
    if progid:
        WORD_PDF_STATE[key] = f"原排版（{progid}）"
        return out
    # ③ 内置降级：直接抽正文自绘，Windows 上不需要装任何 Office
    lines = doc_text_lines(path)
    if not lines:
        raise RuntimeError(
            f"无法读取「{path.name}」的正文。\n"
            "该文件可能是加密/损坏的 .doc，或只含图片。\n"
            "建议用 WPS 另存为 .docx 或 PDF 后再导入。")
    out = APP_TMP / ("wpdf_fb_" + stamp + ".pdf")
    doc_text_to_pdf(lines, out)
    WORD_PDF_STATE[key] = f"内置文本重排（{len(lines)} 行，版式与 WPS 不同，请按本预览拖框）"
    return out


# ============================ Excel：1:1 读取原表 ============================
GRID_MAX_ROWS, GRID_MAX_COLS = 200, 50

_CTRL = re.compile(r"[\x00-\x08\x0b\x0c\x0e-\x1f\ufffd]")
_XESC = re.compile(r"_x[0-9A-Fa-f]{4}_")


def cell_text(s) -> str:
    """清理 WPS/Excel 转义与不可见控制字符，保留中文，绝不截断"""
    if s is None:
        return ""
    t = _XESC.sub("", str(s))
    t = _CTRL.sub("", t)
    return t.replace("\t", " ").strip()


def fmt_number(v, nf: str) -> str:
    """按单元格 number_format 还原 WPS 里看到的显示值（1.00 / 91.88% 等）"""
    nf = (nf or "").lower()
    try:
        if "%" in nf:
            m = re.search(r"0\.(0+)", nf)
            d = len(m.group(1)) if m else 0
            return f"{v * 100:.{d}f}%"
        if "e+" in nf:
            return f"{v:.2E}"
        m = re.search(r"0\.(0+)", nf)
        if m:
            return f"{v:.{len(m.group(1))}f}"
        if float(v).is_integer():
            return str(int(v))
        return f"{v:g}"
    except Exception:
        return str(v)


def fmt_value(v, nf: str = "") -> str:
    if v is None:
        return ""
    if isinstance(v, bool):
        return "TRUE" if v else "FALSE"
    if isinstance(v, datetime):
        return v.strftime("%Y-%m-%d %H:%M:%S") if (v.hour or v.minute or v.second) else v.strftime("%Y-%m-%d")
    if isinstance(v, date):
        return v.strftime("%Y-%m-%d")
    if isinstance(v, dtime):
        return v.strftime("%H:%M:%S")
    if isinstance(v, (int, float)):
        return fmt_number(v, nf)
    return cell_text(v)


@dataclass
class SheetData:
    """一张工作表的完整显示信息，用于 1:1 还原预览"""
    values: list[list[str]] = field(default_factory=list)
    merged: list[tuple[int, int, int, int]] = field(default_factory=list)  # r0,c0,r1,c1 (0-based)
    col_w: list[int] = field(default_factory=list)
    row_h: list[int] = field(default_factory=list)
    align: dict = field(default_factory=dict)
    bg: dict = field(default_factory=dict)
    bold: dict = field(default_factory=dict)
    fsize: dict = field(default_factory=dict)
    wrap: dict = field(default_factory=dict)
    owner: dict = field(default_factory=dict)   # 合并区任意格 -> 左上角 (r,c)
    sheet_name: str = ""
    note: str = ""

    @property
    def grid(self):
        return self.values

    def nrows(self):
        return len(self.values)

    def ncols(self):
        return max((len(r) for r in self.values), default=0)


def _read_xlsx(path: Path, sheet: str) -> SheetData:
    import openpyxl
    from openpyxl.utils import get_column_letter

    wb = openpyxl.load_workbook(path, data_only=True, read_only=False)
    # 公式无缓存值时用公式文本兜底，避免显示空白「少内容」
    try:
        wb_f = openpyxl.load_workbook(path, data_only=False, read_only=False)
    except Exception:
        wb_f = None
    ws = wb[sheet] if sheet in wb.sheetnames else wb.worksheets[0]
    wf = wb_f[ws.title] if wb_f is not None else None

    nr = max(1, min(int(ws.max_row or 1), GRID_MAX_ROWS))
    nc = max(1, min(int(ws.max_column or 1), GRID_MAX_COLS))
    sd = SheetData(sheet_name=ws.title)
    sd.values = [["" for _ in range(nc)] for _ in range(nr)]
    formula_cells = 0

    for r in range(1, nr + 1):
        for c in range(1, nc + 1):
            cell = ws.cell(row=r, column=c)
            v = cell.value
            if v is None and wf is not None:
                fv = wf.cell(row=r, column=c).value
                if isinstance(fv, str) and fv.startswith("="):
                    v = fv
                    formula_cells += 1
                elif fv is not None:
                    v = fv
            sd.values[r - 1][c - 1] = fmt_value(v, getattr(cell, "number_format", ""))

            al = getattr(cell, "alignment", None)
            if al is not None:
                h = (al.horizontal or "").lower()
                if h in ("center", "centre"):
                    sd.align[(r - 1, c - 1)] = "center"
                elif h == "right":
                    sd.align[(r - 1, c - 1)] = "right"
                if al.wrap_text:
                    sd.wrap[(r - 1, c - 1)] = True
            fl = getattr(cell, "fill", None)
            if fl is not None and getattr(fl, "patternType", None) == "solid":
                rgb = getattr(getattr(fl, "fgColor", None), "rgb", None)
                if isinstance(rgb, str) and len(rgb) >= 6:
                    h6 = rgb[-6:].upper()
                    if h6 not in ("FFFFFF", "000000"):
                        sd.bg[(r - 1, c - 1)] = "#" + h6
            ft = getattr(cell, "font", None)
            if ft is not None:
                if ft.bold:
                    sd.bold[(r - 1, c - 1)] = True
                try:
                    sz = int(float(ft.sz or 10))
                    if sz != 10:
                        sd.fsize[(r - 1, c - 1)] = sz
                except Exception:
                    pass

    # 合并单元格
    for mr in ws.merged_cells.ranges:
        r0 = max(0, int(mr.min_row) - 1); r1 = min(nr - 1, int(mr.max_row) - 1)
        c0 = max(0, int(mr.min_col) - 1); c1 = min(nc - 1, int(mr.max_col) - 1)
        if r0 > r1 or c0 > c1:
            continue
        sd.merged.append((r0, c0, r1, c1))
        # WPS/Excel 里合并区显示的是左上角那一格的格式（黄底、居中、粗体），
        # 但 openpyxl 的 MergedCell 不带样式 —— 把样式扩散到整块，预览才和原表一致
        for r in range(r0, r1 + 1):
            for c in range(c0, c1 + 1):
                sd.owner[(r, c)] = (r0, c0)
                if (r, c) == (r0, c0):
                    continue
                for attr, d in (("bg", sd.bg), ("bold", sd.bold),
                                ("align", sd.align), ("fsize", sd.fsize),
                                ("wrap", sd.wrap)):
                    if (r0, c0) in d and (r, c) not in d:
                        d[(r, c)] = d[(r0, c0)]
    # 列宽 / 行高
    for c in range(1, nc + 1):
        cd = ws.column_dimensions.get(get_column_letter(c))
        w = float(cd.width) if (cd is not None and cd.width) else 8.43
        sd.col_w.append(max(46, min(int(w * 7 + 10), 460)))
    for r in range(1, nr + 1):
        rd = ws.row_dimensions.get(r)
        h = float(rd.height) if (rd is not None and rd.height) else 15.0
        sd.row_h.append(max(22, min(int(h * 1.35), 150)))

    try:
        wb.close()
        if wb_f is not None:
            wb_f.close()
    except Exception:
        pass
    if formula_cells:
        sd.note = f"有 {formula_cells} 个公式格缺少缓存值，已显示公式文本（WPS 里显示的是计算结果）"
    return sd


def _read_xls(path: Path, sheet: str) -> SheetData:
    """xlrd 2.x 只支持 .xls；部分 WPS/加密旧表不支持 formatting_info，
    逐级降级到「不带格式」，保证至少能把格子里的内容读出来。"""
    import xlrd
    book, open_kw = None, None
    for kw in ({"formatting_info": True},
               {"formatting_info": True, "encoding_override": "gbk"},
               {"encoding_override": "gbk"},
               {}):
        try:
            book = xlrd.open_workbook(str(path), **kw)
            open_kw = kw
            break
        except Exception:
            continue
    if book is None:
        raise RuntimeError("xlrd 无法打开这个 .xls（可能加密或损坏）；"
                           "建议用 WPS 另存为 .xlsx 后再导入")
    try:
        sh = book.sheet_by_name(sheet)
    except Exception:
        sh = book.sheet_by_index(0)

    nr = max(1, min(sh.nrows, GRID_MAX_ROWS))
    nc = max(1, min(sh.ncols, GRID_MAX_COLS))
    sd = SheetData(sheet_name=sh.name)
    sd.values = [["" for _ in range(nc)] for _ in range(nr)]
    bad = 0
    for r in range(nr):
        for c in range(nc):
            try:
                cl = sh.cell(r, c)
            except Exception:
                continue
            v = cl.value
            if cl.ctype == xlrd.XL_CELL_DATE:
                try:
                    v = xlrd.xldate.xldate_as_datetime(v, book.datemode)
                except Exception:
                    pass
                s = fmt_value(v, "")
            elif cl.ctype == xlrd.XL_CELL_NUMBER:
                nf = ""
                try:
                    xf = book.xf_list[sh.cell_xf_index(r, c)]
                    nf = book.format_map[xf.format_key].format_str
                except Exception:
                    pass
                s = fmt_number(v, nf)
            elif cl.ctype == xlrd.XL_CELL_BOOLEAN:
                s = "TRUE" if v else "FALSE"
            elif cl.ctype in (xlrd.XL_CELL_EMPTY, xlrd.XL_CELL_BLANK):
                s = ""
            else:
                s = cell_text(v)
            if "\ufffd" in s:
                bad += 1
            sd.values[r][c] = s
            try:
                xf = book.xf_list[sh.cell_xf_index(r, c)]
                if xf.font.bold:
                    sd.bold[(r, c)] = True
                al = xf.alignment
                if al.hor_align == 2:
                    sd.align[(r, c)] = "center"
                elif al.hor_align == 3:
                    sd.align[(r, c)] = "right"
                if al.text_wrapped:
                    sd.wrap[(r, c)] = True
                pat = xf.background
                if pat.fill_pattern == 1 and pat.pattern_colour_index not in (0, 1, 64, 65):
                    rgb = book.colour_map.get(pat.pattern_colour_index)
                    if rgb:
                        sd.bg[(r, c)] = "#%02X%02X%02X" % rgb
            except Exception:
                pass

    for (rlo, rhi, clo, chi) in getattr(sh, "merged_cells", []):
        r0, r1, c0, c1 = rlo, min(nr - 1, rhi - 1), clo, min(nc - 1, chi - 1)
        if r0 > r1 or c0 > c1:
            continue
        sd.merged.append((r0, c0, r1, c1))
        for r in range(r0, r1 + 1):
            for c in range(c0, c1 + 1):
                sd.owner[(r, c)] = (r0, c0)
    colmap = getattr(sh, "colinfo_map", {}) or {}
    rowmap = getattr(sh, "rowinfo_map", {}) or {}
    for c in range(nc):
        ci = colmap.get(c)
        w = (ci.width / 256.0 * 7 + 10) if ci is not None else 68
        sd.col_w.append(max(46, min(int(w), 460)))
    for r in range(nr):
        ri = rowmap.get(r)
        h = (ri.height / 20.0 * 1.35) if ri is not None else 22
        sd.row_h.append(max(22, min(int(h), 150)))
    if not any("formatting_info" in k for k in (open_kw or {})):
        sd.note = "已兼容读取 .xls 内容（该文件不提供列宽/底色等格式信息）"
    if bad:
        # 老 .xls 常见 GBK 被误当 latin-1 解出「娀……」类乱码，用 GBK 重读一次
        try:
            bk2 = None
            for kw2 in ({"formatting_info": True, "encoding_override": "gbk"},
                        {"encoding_override": "gbk"}, {}):
                try:
                    bk2 = xlrd.open_workbook(str(path), **kw2); break
                except Exception:
                    continue
            sh2 = bk2.sheet_by_name(sh.name) if bk2 is not None else None
            fixed = 0
            for r in range(nr):
                if sh2 is None:
                    break
                for c in range(nc):
                    if "\ufffd" in sd.values[r][c]:
                        try:
                            t = cell_text(sh2.cell_value(r, c))
                            if "\ufffd" not in t:
                                sd.values[r][c] = t
                                fixed += 1
                        except Exception:
                            pass
            if fixed:
                sd.note = f"已用 GBK 编码修复 {fixed} 个乱码单元格"
            else:
                sd.note = f"检测到 {bad} 个可疑字符；建议用 WPS 另存为 .xlsx 后再导入"
        except Exception:
            sd.note = f"检测到 {bad} 个可疑字符；建议用 WPS 另存为 .xlsx 后再导入"
    return sd


def load_sheet(path: Path, sheet: str) -> SheetData:
    if path.suffix.lower() == ".xlsx":
        return _read_xlsx(path, sheet)
    return _read_xls(path, sheet)


# ============================ PDF：渲染 + 内置中文 OCR ============================
_OCR_SESSION = None
_OCR_CHARS = None
_OCR_ERR = ""


def ocr_engine():
    global _OCR_SESSION, _OCR_CHARS, _OCR_ERR
    if _OCR_SESSION is not None or _OCR_ERR:
        return _OCR_SESSION, _OCR_CHARS, _OCR_ERR
    try:
        import onnxruntime as ort
        if not OCR_MODEL.exists():
            raise RuntimeError("未找到内置 OCR 模型 ocr/rec.onnx")
        so = ort.SessionOptions()
        so.log_severity_level = 4
        so.intra_op_num_threads = min(4, os.cpu_count() or 2)
        _OCR_SESSION = ort.InferenceSession(
            str(OCR_MODEL), sess_options=so, providers=["CPUExecutionProvider"])
        ch = _OCR_SESSION.get_modelmeta().custom_metadata_map["character"].split("\n")
        ch.insert(0, "blank")
        ch.append(" ")
        _OCR_CHARS = ch
    except Exception as e:
        _OCR_ERR = f"{type(e).__name__}: {e}"
    return _OCR_SESSION, _OCR_CHARS, _OCR_ERR


def _bilinear(img, out_w, out_h):
    import numpy as np
    h, w, _ = img.shape
    if h == out_h and w == out_w:
        return img
    ys = np.linspace(0, h - 1, out_h); xs = np.linspace(0, w - 1, out_w)
    y0 = np.floor(ys).astype(int); y1 = np.minimum(y0 + 1, h - 1)
    x0 = np.floor(xs).astype(int); x1 = np.minimum(x0 + 1, w - 1)
    fy = (ys - y0)[:, None]; fx = (xs - x0)[None, :]
    a = img.astype(np.float32)
    top = a[y0[:, None], x0[None, :]] * (1 - fx)[:, :, None] + a[y0[:, None], x1[None, :]] * fx[:, :, None]
    bot = a[y1[:, None], x0[None, :]] * (1 - fx)[:, :, None] + a[y1[:, None], x1[None, :]] * fx[:, :, None]
    return (top * (1 - fy)[:, :, None] + bot * fy[:, :, None]).astype(np.uint8)


def _erase_table_lines(rgb):
    """擦掉贯穿整块的表格横/竖线。
    框选压到单元格边框时，旧版会把边框线认成「蒌 / 業 / 丨」这类怪字。"""
    import numpy as np
    g = rgb.mean(axis=2)
    thr = max(95.0, min(195.0, float(g.mean() - 0.65 * g.std())))
    ink = g < thr
    h, w = ink.shape
    out = rgb.copy()
    col = ink.mean(axis=0)
    row = ink.mean(axis=1)
    vlines = np.where(col > 0.82)[0]
    hlines = np.where(row > 0.82)[0]
    # 只擦「贯穿」的线：该列/行在两端也应有墨，避免误伤笔画
    for x in vlines:
        if ink[:max(2, h // 8), x].mean() > 0.6 and ink[-max(2, h // 8):, x].mean() > 0.6:
            out[:, max(0, x - 1):min(w, x + 2)] = 255
    for y in hlines:
        if ink[y, :max(2, w // 8)].mean() > 0.6 and ink[y, -max(2, w // 8):].mean() > 0.6:
            out[max(0, y - 1):min(h, y + 2), :] = 255
    return out, bool(len(vlines) or len(hlines))


def _trim_white(rgb):
    """裁掉四周空白，避免边缘那截表格竖线进入识别"""
    import numpy as np
    g = rgb.mean(axis=2)
    ink = g < 180
    h, w = ink.shape
    rows = np.where(ink.sum(1) > max(2, w * 0.008))[0]
    cols = np.where(ink.sum(0) > max(2, h * 0.008))[0]
    if not len(rows) or not len(cols):
        return rgb
    p = 3
    return rgb[max(0, rows[0] - p):min(h, rows[-1] + p + 1),
               max(0, cols[0] - p):min(w, cols[-1] + p + 1)]


def _ocr_array(arr, session, chars):
    """对任意 RGB 图块做 CTC 识别，返回 (文字, 每字置信, 平均置信)"""
    import numpy as np
    h, w = arr.shape[:2]
    if h < 8 or w < 8:
        return "", [], 0.0
    tw = max(16, min(320, int(round(48.0 * w / max(1, h)))))
    a = _bilinear(arr, tw, 48)
    norm = np.zeros((3, 48, 320), dtype=np.float32)
    norm[:, :, :tw] = (a.astype(np.float32).transpose(2, 0, 1) / 255.0 - 0.5) / 0.5
    out = session.run(None, {"x": norm[None, :]})[0][0]
    ids = out.argmax(1); conf = out.max(1)
    keep = np.ones(len(ids), dtype=bool)
    keep[1:] = ids[1:] != ids[:-1]
    keep &= ids != 0
    if not keep.any():
        return "", [], 0.0
    txt = "".join(chars[i] for i in ids[keep]).strip()
    cf = [float(x) for x in conf[keep]]
    # 尾部低置信孤立字基本都是线/摩尔纹幻觉，直接丢掉
    while cf and cf[-1] < 0.55 and len(txt) > 1:
        txt = txt[:-1]; cf.pop()
    return txt, cf, (float(np.mean(cf)) if cf else 0.0)


def _ocr_split(arr, session, chars):
    """长框按约 6 字宽分片识别，避免整行被压到 320px 后字挤在一起产生幻觉尾字"""
    import numpy as np
    h, w = arr.shape[:2]
    if w <= h * 7 or h < 8:
        t, cf, c = _ocr_array(arr, session, chars)
        return t, cf, c
    step = max(int(h * 5.0), 24)
    ov = max(int(h * 0.8), 8)
    n = max(2, int(math.ceil(w / step)))
    seg = w / n
    result, confs = "", []
    for k in range(n):
        x0 = max(0, int(k * seg - ov))
        x1 = min(w, int((k + 1) * seg + ov))
        t, cf, _ = _ocr_array(arr[:, x0:x1], session, chars)
        if not t:
            continue
        m = 0
        for L in range(min(4, len(t), len(result)), 0, -1):
            if result.endswith(t[:L]):
                m = L; break
        result += t[m:]
        confs += cf[m:]
    return result, confs, (float(np.mean(confs)) if confs else 0.0)


def _vote_text(cands, conf_map):
    """多候选投票：取被最多候选支持的最长前缀，孤立幻觉尾字会被自然截掉"""
    uniq = []
    for c in cands:
        c = (c or "").strip()
        if c and c not in uniq:
            uniq.append(c)
    if not uniq:
        return "", ""
    if len(uniq) == 1:
        return uniq[0], ""
    maxlen = max(len(x) for x in uniq)
    need = max(2, (len(uniq) + 1) // 2)      # 多数候选同意
    best = ""
    for c in uniq:
        support = sum(1 for o in uniq if o.startswith(c))
        # 达标（被多数候选支持）的里面取最长的；孤立幻觉尾字 support=1 不达标
        if support >= need and len(c) > len(best):
            best = c
    if best and len(best) >= 0.6 * maxlen:
        return best, "多候选一致"
    lcp = os.path.commonprefix(uniq)
    med = sorted(len(x) for x in uniq)[len(uniq) // 2]
    if len(lcp) >= 2 and len(lcp) >= 0.6 * med:
        return lcp, "已取多候选一致部分"
    return max(uniq, key=lambda x: conf_map.get(x, 0.0)), "按置信择优"


def _textlayer_ref(pg, clip):
    """只取框内「文字层」作为校验参考（不用于凑数、不参与替换整体结果）"""
    try:
        ws = pg.get_text("words", clip=clip)
    except Exception:
        return ""
    x0, y0, x1, y1 = clip.x0, clip.y0, clip.x1, clip.y1
    picked = []
    for w in ws:
        try:
            wx0, wy0, wx1, wy1, word = w[0], w[1], w[2], w[3], w[4]
        except Exception:
            continue
        if not str(word).strip():
            continue
        cx, cy = (wx0 + wx1) / 2, (wy0 + wy1) / 2
        if not (x0 - 1 <= cx <= x1 + 1 and y0 - 1 <= cy <= y1 + 1):
            continue
        iw = max(0, min(wx1, x1) - max(wx0, x0))
        ih = max(0, min(wy1, y1) - max(wy0, y0))
        if iw * ih < 0.45 * max(1, (wx1 - wx0) * (wy1 - wy0)):
            continue
        picked.append((round(cy / 3), wx0, str(word)))
    picked.sort()
    return re.sub(r"\s+", "", "".join(p[2] for p in picked))


def _trim_by_ref(ocr, ref):
    """OCR 比框内文字层多出尾字时只截尾，不替换整体，保持「OCR 为主」"""
    if not ref or not ocr:
        return ocr, ""
    o = re.sub(r"\s+", "", ocr)
    r = re.sub(r"\s+", "", ref)
    if not r or o == r:
        return ocr, ""
    # 只处理「多出 1~2 个尾字」这种典型幻觉；差异太大说明文字层本身不全，不动
    if o.startswith(r) and 0 < len(o) - len(r) <= 2:
        return r, "已去掉多识别的尾字"
    sm = difflib.SequenceMatcher(None, o, r)
    blocks = sm.get_matching_blocks()
    if blocks:
        a, b, size = max(blocks, key=lambda x: x.size)
        if (size >= 4 and 0 < len(o) - (a + size) <= 2
                and b + size >= len(r) - 1):
            return o[:a + size], "已去掉多识别的尾字"
    return ocr, ""


def _ocr_band(arr, session, chars):
    """对一行图块生成多个候选（原图 / 去线 / 裁白 / 分片 / 左右微裁）后投票"""
    clean, had_lines = _erase_table_lines(arr)
    trimmed = _trim_white(clean)
    cands, conf_map = [], {}

    def add(a, split=False):
        t, cf, c = (_ocr_split(a, session, chars) if split
                    else _ocr_array(a, session, chars))
        t = (t or "").strip()
        if t:
            cands.append(t)
            conf_map[t] = max(conf_map.get(t, 0.0), c)
    add(arr)
    add(clean)
    add(trimmed)
    add(trimmed, split=True)
    if trimmed.shape[1] > 60:
        m = max(1, int(trimmed.shape[1] * 0.02))
        add(trimmed[:, m:-m])
    text, vnote = _vote_text(cands, conf_map)
    conf = max(conf_map.values()) if conf_map else 0.0
    extras = []
    if had_lines:
        extras.append("已擦表格线")
    if vnote and vnote != "多候选一致":
        extras.append(vnote)
    return text, conf, extras


def _estimate_glyph_h(pg, clip) -> float:
    """估计框内字高（pt）：优先用文字层 words 高度中位数，扫描件按框高估"""
    try:
        ws = pg.get_text("words", clip=clip)
        hs = [float(w[3] - w[1]) for w in ws
              if (w[2] - w[0]) > 2 and (w[3] - w[1]) > 3]
        if hs:
            hs.sort()
            return hs[len(hs) // 2]
    except Exception:
        pass
    return max(12.0, min(40.0, clip.height * 0.62))


def _expand_ink_bbox(ink, box_px, pad_x: int, pad_y: int):
    """框边若切进了笔画（半个字），向外补到该笔画断开为止，最多 pad 像素。

    只沿「原始框的那一行 / 那一列」看墨迹：左右外扩只看 y0:y1 这一段，
    上下外扩只看 x0:x1 这一段，避免被框外上下的表格横线、相邻列误导。
    传入的 ink 已擦掉贯穿表格线，所以表格竖线不会被当成「半个字」。
    """
    import numpy as np
    H, W = ink.shape
    x0, y0, x1, y1 = [int(round(v)) for v in box_px]
    x0, y0 = max(0, x0), max(0, y0)
    x1, y1 = min(W, x1), min(H, y1)
    if x1 <= x0 or y1 <= y0:
        return box_px

    def col_has(x):
        return 0 <= x < W and int(ink[y0:y1, x].sum()) >= 2

    def row_has(y):
        return 0 <= y < H and int(ink[y, x0:x1].sum()) >= 2

    nx0, ny0, nx1, ny1 = x0, y0, x1, y1
    # 左边界切到笔画 → 往外走，直到连续 2 列都没墨
    if col_has(x0) or col_has(x0 + 1):
        lim = max(0, x0 - pad_x)
        x = x0
        while x > lim and (col_has(x - 1) or col_has(x - 2)):
            x -= 2
        nx0 = max(lim, x - 2)
    # 右边界切到笔画
    if col_has(x1 - 1) or col_has(x1 - 2):
        lim = min(W, x1 + pad_x)
        x = x1
        while x < lim and (col_has(x) or col_has(x + 1)):
            x += 2
        nx1 = min(lim, x + 2)
    # 上 / 下边界切到笔画（用擦线后的整幅行投影，范围限定在扩展后的列内）
    def row_has2(y, xa, xb):
        return 0 <= y < H and int(ink[y, xa:xb].sum()) >= 2
    if row_has(y0) or row_has(y0 + 1):
        lim = max(0, y0 - pad_y)
        y = y0
        while y > lim and (row_has2(y - 1, nx0, nx1) or row_has2(y - 2, nx0, nx1)):
            y -= 2
        ny0 = max(lim, y - 2)
    if row_has(y1 - 1) or row_has(y1 - 2):
        lim = min(H, y1 + pad_y)
        y = y1
        while y < lim and (row_has2(y, nx0, nx1) or row_has2(y + 1, nx0, nx1)):
            y += 2
        ny1 = min(lim, y + 2)
    return (nx0, ny0, nx1, ny1)


def ocr_clip(pdf: Path, page: int, rect_pt) -> tuple[str, str]:
    """只识别框内图像：框边补全半个字 + 去表格线 + 分片 + 多候选投票 + 文字层去尾校验"""
    import numpy as np
    if not rect_pt:
        return "", "未框选"
    session, chars, err = ocr_engine()
    if session is None:
        return "", f"OCR 不可用：{err}"
    try:
        doc = fitz.open(pdf)
        pg = doc[max(0, min(page, doc.page_count - 1))]
        clip0 = fitz.Rect(*rect_pt)
        scale = 3.0
        # ① 先渲染一个「带外扩余量」的探测图，用来判断框边是否切到了笔画
        gh = _estimate_glyph_h(pg, clip0)
        pad_x = max(6.0, min(32.0, gh * 0.9))
        pad_y = max(4.0, min(22.0, gh * 0.55))
        probe = fitz.Rect(clip0.x0 - pad_x, clip0.y0 - pad_y,
                          clip0.x1 + pad_x, clip0.y1 + pad_y)
        probe &= pg.rect                       # 不越出页面
        pix = pg.get_pixmap(matrix=fitz.Matrix(scale, scale), clip=probe, alpha=False)
        rgb = np.frombuffer(pix.samples, dtype=np.uint8).reshape(pix.height, pix.stride)[
            :, :pix.width * 3].reshape(pix.height, pix.width, 3)
        # 先擦掉贯穿表格线，避免把边框线误当成「被切半个字」
        clean, _ = _erase_table_lines(rgb)
        ink2 = clean.mean(axis=2) < 178

        bx0 = (clip0.x0 - probe.x0) * scale
        by0 = (clip0.y0 - probe.y0) * scale
        bx1 = (clip0.x1 - probe.x0) * scale
        by1 = (clip0.y1 - probe.y0) * scale
        nx0, ny0, nx1, ny1 = _expand_ink_bbox(
            ink2, (bx0, by0, bx1, by1),
            int(pad_x * scale), int(pad_y * scale))
        expanded = (nx0 < bx0 - 1 or ny0 < by0 - 1
                    or nx1 > bx1 + 1 or ny1 > by1 + 1)

        # ② 用扩展后的区域做识别；文字层参考也用扩展后的框，才能拿到补全的完整字
        region = rgb[ny0:ny1, nx0:nx1]
        clipx = fitz.Rect(probe.x0 + nx0 / scale, probe.y0 + ny0 / scale,
                          probe.x0 + nx1 / scale, probe.y0 + ny1 / scale)
        r, g, b = (region[:, :, 0].astype(np.int16),
                   region[:, :, 1].astype(np.int16),
                   region[:, :, 2].astype(np.int16))
        red = float(((r > 110) & (r > g + 45) & (r > b + 45)).mean())
        rclean, _ = _erase_table_lines(region)
        lineink = (rclean.mean(axis=2) < 175).sum(axis=1)
        thr = max(2, int(region.shape[1] * 0.010))
        bands, start = [], None
        for i, v in enumerate(lineink):
            if v > thr and start is None:
                start = i
            elif v <= thr and start is not None:
                if i - start >= 6:
                    bands.append((start, i))
                start = None
        if start is not None:
            bands.append((start, len(lineink)))
        if not bands:
            bands = [(0, len(lineink))]

        lines, confs, extras = [], [], []
        for y0, y1 in bands[:8]:
            core = region[max(0, y0 - 2):min(len(lineink), y1 + 2)]
            # 上下各补 20px 白边：汉字长竖线在紧贴文字的矮图里会被误判成
            # 「贯穿表格线」擦掉，补白后该列墨迹占比下降就不会误擦；
            # 真正的表格线依然贯穿整幅（含白边），照常被擦除。
            band = np.pad(core, ((20, 20), (0, 0), (0, 0)),
                          mode="constant", constant_values=255)
            t, c, ex = _ocr_band(band, session, chars)
            if t:
                lines.append(t); confs.append(c)
                for e in ex:
                    if e not in extras:
                        extras.append(e)
        text = " ".join(lines).strip()

        ref = _textlayer_ref(pg, clipx)
        text, trim_note = _trim_by_ref(text, ref)
        doc.close()

        note = f"OCR 置信 {min(confs):.2f}" if confs else "OCR 无结果"
        if expanded:
            note += "｜已补全框边半个字"
        for e in extras:
            note += "｜" + e
        if trim_note:
            note += "｜" + trim_note
        if red > 0.08:
            note += f"｜红章区{red:.0%}请核对"
        return text, note
    except Exception as e:
        return "", f"OCR 失败：{type(e).__name__}: {e}"


def render_page(pdf: Path, page: int, target_w: int = 760):
    doc = fitz.open(pdf)
    pg = doc[max(0, min(page, doc.page_count - 1))]
    zoom = target_w / max(1.0, pg.rect.width)
    pix = pg.get_pixmap(matrix=fitz.Matrix(zoom, zoom), alpha=False)
    img = QImage(pix.samples, pix.width, pix.height, pix.stride, QImage.Format_RGB888)
    pm = QPixmap.fromImage(img.copy())
    doc.close()
    return pm, zoom


def pdf_page_count(pdf: Path) -> int:
    try:
        d = fitz.open(pdf); n = d.page_count; d.close(); return n
    except Exception:
        return 0


def pdf_page_size(pdf: Path, page: int):
    try:
        d = fitz.open(pdf); r = d[max(0, min(page, d.page_count - 1))].rect
        d.close(); return (r.width, r.height)
    except Exception:
        return None


# ============================ 数据模型 / 锚点 ============================
@dataclass
class FileItem:
    path: Path
    kind: str
    sheet: str = ""
    sd: SheetData | None = None          # Excel 原表完整信息
    work_pdf: Path | None = None
    page_count: int = 0
    raw_text: str = ""
    new_name: str = ""
    status: str = "待处理"
    via: str = ""
    hint: str = ""
    work_note: str = ""           # Word 转换方式说明（原排版 / 内置文本重排）
    sheet_names: list[str] = field(default_factory=list)   # 后台读好后缓存，导入时不再解析
    prepared: bool = False        # 已完成耗时读取；再导入新文件时只处理没准备好的
    manual: bool = False          # 新文件名被手工改过
    raw_manual: bool = False      # 识别内容被手工校正过

    @property
    def mode(self) -> str:
        return mode_of(self.kind)


@dataclass
class Anchor:
    kind: str = ""
    rect: list[float] | None = None
    r0: int = -1
    c0: int = -1
    r1: int = -1
    c1: int = -1

    def valid(self) -> bool:
        if self.kind == "page":
            return bool(self.rect)
        if self.kind == "grid":
            return self.r0 >= 0 and self.c0 >= 0
        return False

    def desc(self) -> str:
        if self.kind == "page" and self.rect:
            r = self.rect
            return f"框选 {r[0]:.0f},{r[1]:.0f} → {r[2]:.0f},{r[3]:.0f} pt"
        if self.kind == "grid" and self.r0 >= 0:
            a = f"{col_name(self.c0)}{self.r0 + 1}"
            b = f"{col_name(self.c1)}{self.r1 + 1}"
            return f"格子 {a}" + (f":{b}" if (a != b) else "") + "（整格内容）"
        return "未定位"


def grid_extract(sd: SheetData | None, a: Anchor) -> tuple[str, str]:
    """格子里是什么就取什么（显示值原样）；合并区任意格归一到左上角"""
    if sd is None or not sd.values:
        return "", "无工作表数据"
    owner = sd.owner
    vals, cells, seen = [], [], set()
    r1 = min(a.r1, sd.nrows() - 1)
    for r in range(max(0, a.r0), r1 + 1):
        row = sd.values[r]
        c1 = min(a.c1, len(row) - 1)
        for c in range(max(0, a.c0), c1 + 1):
            rr, cc = owner.get((r, c), (r, c))
            if (rr, cc) in seen:
                continue
            seen.add((rr, cc))
            v = sd.values[rr][cc] if (0 <= rr < sd.nrows() and cc < len(sd.values[rr])) else ""
            if v.strip():
                vals.append(v.strip())
                cells.append(f"{col_name(cc)}{rr + 1}")
    text = " ".join(vals).strip()
    note = a.desc()
    if cells:
        note += f"｜命中 {cells[0]}" + (f"~{cells[-1]}" if len(cells) > 1 else "")
    return text, note


# ============================ 预览控件 ============================
class PageView(QLabel):
    rectChanged = Signal(object)

    def __init__(self):
        super().__init__()
        self.setAlignment(Qt.AlignTop | Qt.AlignLeft)
        self.setStyleSheet("background:#e9ecf1;")
        self.setFixedSize(560, 320)
        self.setText("请导入文件，或直接把文件 / 文件夹拖进窗口")
        self.setFont(QFont("Microsoft YaHei", 12))
        self.zoom = 1.0
        self.rect_pt = None
        self.sel_px = None
        self._drag = None
        self._dragging = False
        self.hint = ""

    def set_page(self, pm, zoom, rect_pt=None):
        self.hint = ""; self.setText("")
        self.zoom = zoom
        self.setPixmap(pm)
        self.setFixedSize(pm.size())
        self.rect_pt = list(rect_pt) if rect_pt else None
        self.sel_px = self._to_px(self.rect_pt) if self.rect_pt else None
        self.update()

    def set_hint(self, text):
        self.hint = text; self.setText("")
        self.setPixmap(QPixmap()); self.setFixedSize(560, 320)
        self.sel_px = None; self.update()

    def _to_px(self, r):
        z = self.zoom
        return QRect(int(r[0] * z), int(r[1] * z),
                     int((r[2] - r[0]) * z), int((r[3] - r[1]) * z))

    def set_rect_pt(self, r):
        self.rect_pt = list(r) if r else None
        self.sel_px = self._to_px(self.rect_pt) if self.rect_pt else None
        self.update()

    def mousePressEvent(self, e):
        if self.pixmap() is None or self.pixmap().isNull() or e.button() != Qt.LeftButton:
            return
        self._drag = e.pos(); self._dragging = True
        self.sel_px = QRect(e.pos(), e.pos()); self.update()

    def mouseMoveEvent(self, e):
        if self._dragging and self._drag:
            self.sel_px = QRect(self._drag, e.pos()).normalized(); self.update()

    def mouseReleaseEvent(self, e):
        if not self._dragging:
            return
        self._dragging = False
        if not self.sel_px or self.sel_px.width() < 4 or self.sel_px.height() < 4:
            self.sel_px = self._to_px(self.rect_pt) if self.rect_pt else None
            self.update(); return
        z = self.zoom
        self.rect_pt = [self.sel_px.left() / z, self.sel_px.top() / z,
                        self.sel_px.right() / z, self.sel_px.bottom() / z]
        self.rectChanged.emit(self.rect_pt); self.update()

    def paintEvent(self, e):
        super().paintEvent(e)
        if self.hint:
            p = QPainter(self)
            p.setPen(QColor("#555")); p.setFont(QFont("Microsoft YaHei", 11))
            p.drawText(self.rect().adjusted(20, 20, -20, -20),
                       Qt.AlignCenter | Qt.TextWordWrap, self.hint)
            p.end(); return
        if not self.sel_px:
            return
        p = QPainter(self)
        p.fillRect(self.sel_px, QColor(255, 60, 60, 55))
        p.setPen(QPen(QColor(220, 40, 40), 2, Qt.SolidLine))
        p.drawRect(self.sel_px)
        p.setBrush(QColor(220, 40, 40))
        for c in (self.sel_px.topLeft(), self.sel_px.topRight(),
                  self.sel_px.bottomLeft(), self.sel_px.bottomRight()):
            p.drawRect(QRect(c - QPoint(3, 3), c + QPoint(3, 3)))
        p.end()


class GridSelectView(QTableWidget):
    """Excel 原表视图：合并单元格 / 列宽 / 行高 / 底色 / 对齐 / 数字格式 1:1 还原"""
    selected = Signal(int, int, int, int)

    def __init__(self):
        super().__init__(0, 0)
        self.setSelectionMode(QAbstractItemView.ContiguousSelection)
        self.setEditTriggers(QAbstractItemView.NoEditTriggers)
        self.setFont(QFont("Microsoft YaHei", 10))
        self.setWordWrap(True)
        self.setTextElideMode(Qt.ElideNone)     # 关键：不再把「项目名…」截断
        self.setStyleSheet(
            "QTableWidget{gridline-color:#b9c0ca;background:#fff;color:#111827;}"
            "QHeaderView::section{background:#eef1f6;padding:4px;border:0;"
            "border-right:1px solid #d6dae1;border-bottom:1px solid #d6dae1;}"
            "QTableWidget::item:selected{background:#cfe0ff;color:#0b3d91;}")
        self.itemSelectionChanged.connect(self._on_sel)
        self._busy = False

    def set_sheet(self, sd: SheetData):
        self._busy = True
        self.clear()
        if sd is None or not sd.values:
            self.setRowCount(0); self.setColumnCount(0); self._busy = False; return
        nr, nc = sd.nrows(), sd.ncols()
        self.setRowCount(nr); self.setColumnCount(nc)
        self.setHorizontalHeaderLabels([col_name(c) for c in range(nc)])
        self.setVerticalHeaderLabels([str(r + 1) for r in range(nr)])

        for r in range(nr):
            self.setRowHeight(r, sd.row_h[r] if r < len(sd.row_h) else 22)
            for c in range(nc):
                v = sd.values[r][c] if c < len(sd.values[r]) else ""
                item = QTableWidgetItem(v)
                item.setToolTip(f"{col_name(c)}{r + 1}  =  {v}")   # 悬停看完整内容，绝不丢
                al = sd.align.get((r, c), "")
                qa = Qt.AlignVCenter | (Qt.AlignHCenter if al == "center" else
                                        Qt.AlignRight if al == "right" else Qt.AlignLeft)
                if sd.wrap.get((r, c)):
                    qa |= Qt.TextWordWrap
                item.setTextAlignment(qa)
                bg = sd.bg.get((r, c))
                if bg:
                    item.setBackground(QColor(bg))
                f = self.font()
                if sd.bold.get((r, c)) or sd.fsize.get((r, c)):
                    f = QFont("Microsoft YaHei", sd.fsize.get((r, c), 10))
                    f.setBold(bool(sd.bold.get((r, c))))
                    item.setFont(f)
                self.setItem(r, c, item)
        for c in range(nc):
            self.setColumnWidth(c, sd.col_w[c] if c < len(sd.col_w) else 68)
        for (r0, c0, r1, c1) in sd.merged:
            try:
                self.setSpan(r0, c0, r1 - r0 + 1, c1 - c0 + 1)
            except Exception:
                pass
        self._busy = False

    def _on_sel(self):
        if self._busy:
            return
        rs = self.selectedRanges()
        if rs:
            r = rs[0]
            self.selected.emit(r.topRow(), r.leftColumn(), r.bottomRow(), r.rightColumn())

    def show_anchor(self, a: Anchor):
        if a.r0 < 0:
            return
        self._busy = True
        self.clearSelection()
        try:
            self.setRangeSelected(QTableWidgetSelectionRange(
                a.r0, a.c0, max(a.r0, a.r1), max(a.c0, a.c1)), True)
        except Exception:
            pass
        self._busy = False


class CellListDialog(QDialog):
    """列出全部非空单元格，用来核对「原表内容有没有少 / 有没有乱」"""
    cellChosen = Signal(int, int)

    def __init__(self, sd: SheetData, parent=None):
        super().__init__(parent)
        self.setWindowTitle("单元格清单 — 核对原表内容")
        self.resize(820, 560)
        v = QVBoxLayout(self)
        cnt = sum(1 for row in sd.values for x in row if x.strip())
        v.addWidget(QLabel(f"工作表：{sd.sheet_name}　非空单元格：{cnt}　"
                           "（双击一行即可用该格子定位；悬停/选中可看完整内容）"))
        self.ed = QLineEdit()
        self.ed.setPlaceholderText("输入关键词筛选，例如「项目」或「A2」")
        self.ed.textChanged.connect(self._fill)
        v.addWidget(self.ed)
        self.lst = QListWidget()
        self.lst.itemDoubleClicked.connect(self._choose)
        v.addWidget(self.lst, 1)
        if sd.note:
            n = QLabel("⚠ " + sd.note)
            n.setStyleSheet("color:#b26a00;")
            n.setWordWrap(True)
            v.addWidget(n)
        bb = QDialogButtonBox(QDialogButtonBox.Close)
        bb.rejected.connect(self.reject)
        v.addWidget(bb)
        self._rows = []
        for r, row in enumerate(sd.values):
            for c, val in enumerate(row):
                if val.strip():
                    self._rows.append((r, c, f"{col_name(c)}{r + 1}", val))
        self._fill("")

    def _fill(self, kw):
        self.lst.clear()
        k = (kw or "").strip().lower()
        for r, c, addr, val in self._rows:
            if k and k not in val.lower() and k not in addr.lower():
                continue
            it = QListWidgetItem(f"{addr:<6s} = {val}")
            it.setToolTip(f"{addr} = {val}")
            it.setData(Qt.UserRole, (r, c))
            self.lst.addItem(it)

    def _choose(self, item):
        r, c = item.data(Qt.UserRole)
        self.cellChosen.emit(r, c)
        self.accept()


# ============================ 后台线程 ============================
class PrepareWorker(QThread):
    one_done = Signal(int, str)
    log = Signal(str)
    finished_all = Signal()

    def __init__(self, items, parent=None):
        super().__init__(parent)
        self.items = items

    def run(self):
        # 只处理「还没准备好」的文件：再导入一个文件夹时不会把旧文件全量重读一遍
        todo = [(i, it) for i, it in enumerate(self.items) if not it.prepared]
        if not todo:
            self.finished_all.emit()
            return
        total = len(todo)
        # 一次启动 LibreOffice / 一次启动 WPS，把这一批 Word 全转完。
        # 旧版每个文件单独冷启动转换器，是「导入文件夹要几分钟」的根因。
        word_paths = [it.path for _, it in todo if it.kind == "word"]
        if word_paths:
            self.log.emit(f"批量转换 Word（{len(word_paths)} 个，只启动一次转换器）…")
            try:
                batch_word_to_pdf(word_paths)
            except Exception:
                pass
        for n, (i, it) in enumerate(todo, 1):
            self.log.emit(f"读取中 {n}/{total}：{it.path.name}")
            err = ""
            try:
                if it.kind == "excel":
                    if not it.sheet_names:
                        it.sheet_names = sheet_names(it.path)
                    names = it.sheet_names
                    if not it.sheet:
                        it.sheet = names[0] if names else ""
                    it.sd = load_sheet(it.path, it.sheet)
                    it.work_pdf = None
                    it.page_count = 1
                    it.work_note = (".xls 旧格式" if it.path.suffix.lower() == ".xls"
                                    else ".xlsx 原表")
                    if it.sd is not None and it.sd.note:
                        it.work_note += "｜" + it.sd.note
                elif it.kind == "word":
                    it.work_pdf = word_to_pdf(it.path)
                    it.page_count = pdf_page_count(it.work_pdf)
                    it.work_note = WORD_PDF_STATE.get(str(it.path.resolve()), "")
                else:
                    it.work_pdf = it.path
                    it.page_count = pdf_page_count(it.path)
                it.hint = ""
                it.status = "待定位"
                it.prepared = True
            except Exception as e:
                err = f"{type(e).__name__}: {e}"
                it.hint = err
                it.status = "转换失败"
                it.prepared = True      # 失败也标记，避免下次导入反复重试卡住
            self.one_done.emit(i, err)
        self.finished_all.emit()


class OcrWorker(QThread):
    one_done = Signal(int, str, str)
    log = Signal(str)
    finished_all = Signal()

    def __init__(self, jobs, page, parent=None):
        super().__init__(parent)
        self.jobs = jobs
        self.page = page

    def run(self):
        for k, (idx, pdf, rect) in enumerate(self.jobs):
            self.log.emit(f"OCR 识别中 {k + 1}/{len(self.jobs)}")
            t, note = ocr_clip(pdf, self.page, rect)
            self.one_done.emit(idx, t, note)
        self.finished_all.emit()


# ============================ 拖放导入 ============================
class DropFilter(QObject):
    """把任意子控件上的拖放统一转给主窗口，避免 QLineEdit 等把文件路径插成文字"""
    def __init__(self, win):
        super().__init__(win)
        self.win = win

    def eventFilter(self, obj, ev):
        t = ev.type()
        md = getattr(ev, "mimeData", lambda: None)()
        if t in (QEvent.DragEnter, QEvent.DragMove):
            if md is not None and md.hasUrls():
                ev.acceptProposedAction()
                return True
            return False
        if t == QEvent.Drop:
            if md is not None and md.hasUrls():
                self.win.handle_drop(md.urls())
                ev.acceptProposedAction()
                return True
        return super().eventFilter(obj, ev)


# ============================ 主窗口 ============================
class MainWindow(QMainWindow):
    def __init__(self):
        super().__init__()
        self.setWindowTitle("文件固定位置批量重命名工具  v3.9")
        self.resize(1360, 900)
        self.items: list[FileItem] = []
        self.page = 0
        self.template_idx = 0
        self.cur_idx = -1
        self.anchor = Anchor()
        self.tpl_page_size = None
        self.worker = None
        self.ocr_worker = None
        self.last_ops = []
        self._loading = False
        self._size_warn = ""
        self._pending_reload = False
        self._pending_anchor = False
        self._rect_timer = QTimer(self)
        self._rect_timer.setSingleShot(True)
        self._rect_timer.setInterval(450)
        self._rect_timer.timeout.connect(self.apply_rect_spin)
        # 批量导入时，后台每读完一个文件都重建整张结果表会拖慢界面；合并成 180ms 一次
        self._ui_flush = QTimer(self)
        self._ui_flush.setSingleShot(True)
        self._ui_flush.setInterval(180)
        self._ui_flush.timeout.connect(self.refresh_table)
        self._build_ui()
        self._enable_drop()
        self.lbl_crc = QLabel("")
        self.lbl_crc.setStyleSheet("color:#1a5fb4;font-weight:bold;")
        self.statusBar().addPermanentWidget(self.lbl_crc)
        self._refresh_crc()
        self.statusBar().showMessage(
            "可直接把文件 / 文件夹拖进窗口 → 按模板自动切换模式 → 定位 → 核对 → 一键重命名", 12000)

    # ---------------- UI ----------------
    def _build_ui(self):
        root = QWidget(); self.setCentralWidget(root)
        v = QVBoxLayout(root); v.setContentsMargins(10, 10, 10, 8); v.setSpacing(8)

        bar = QHBoxLayout()
        b1 = QPushButton("导入文件"); b1.clicked.connect(self.add_files)
        b1.setToolTip("也可以直接把文件 / 文件夹从资源管理器拖进窗口")
        b2 = QPushButton("导入文件夹"); b2.clicked.connect(self.add_dir)
        b3 = QPushButton("清空"); b3.clicked.connect(self.clear_all)
        bar.addWidget(b1); bar.addWidget(b2); bar.addWidget(b3)
        bar.addSpacing(16)
        self.lbl_mode = QLabel("模式：未导入")
        self.lbl_mode.setStyleSheet("font-weight:bold;color:#1a73e8;font-size:13px;")
        bar.addWidget(self.lbl_mode)
        bar.addStretch(1)
        self.progress = QProgressBar(); self.progress.setFixedWidth(200); self.progress.hide()
        bar.addWidget(self.progress)
        self.lbl_env = QLabel(); bar.addWidget(self.lbl_env)
        v.addLayout(bar)
        self._refresh_env()

        split = QSplitter(Qt.Horizontal)
        left = QWidget(); lv = QVBoxLayout(left); lv.setContentsMargins(0, 0, 0, 0)
        lv.addWidget(QLabel("文件列表（单击预览，双击设为模板；可直接拖入文件 / 文件夹）"))
        self.lst = QListWidget()
        self.lst.currentRowChanged.connect(self.on_select)
        self.lst.itemDoubleClicked.connect(lambda _: self.set_template())
        lv.addWidget(self.lst, 1)
        hb = QHBoxLayout()
        bt = QPushButton("设为模板"); bt.clicked.connect(self.set_template)
        hb.addWidget(bt); hb.addStretch(1)
        lv.addLayout(hb)
        self.lbl_tpl = QLabel("模板：未设置"); lv.addWidget(self.lbl_tpl)
        split.addWidget(left)

        right = QWidget(); rv = QVBoxLayout(right); rv.setContentsMargins(0, 0, 0, 0)

        self.ctl_stack = QStackedWidget(); self.ctl_stack.setFixedHeight(42)

        # --- 模式① 控制 ---
        w_page = QWidget(); h = QHBoxLayout(w_page); h.setContentsMargins(0, 0, 0, 0)
        h.addWidget(QLabel("页码"))
        self.sp_page = QSpinBox(); self.sp_page.setMinimum(1); self.sp_page.setMaximum(1)
        self.sp_page.valueChanged.connect(self.on_page_changed); h.addWidget(self.sp_page)
        self.lbl_pages = QLabel("/ 1"); h.addWidget(self.lbl_pages)
        h.addWidget(QLabel("缩放"))
        self.sp_zoom = QSpinBox(); self.sp_zoom.setRange(400, 1600)
        self.sp_zoom.setSingleStep(100); self.sp_zoom.setValue(760)
        self.sp_zoom.valueChanged.connect(self.render_current); h.addWidget(self.sp_zoom)
        h.addSpacing(10); h.addWidget(QLabel("框选 pt"))
        self.sp_rect = []
        for nm in ("x0", "y0", "x1", "y1"):
            h.addWidget(QLabel(nm))
            s = QDoubleSpinBox(); s.setRange(0, 3000); s.setDecimals(1)
            s.setFixedWidth(74); s.valueChanged.connect(self._rect_spin_changed)
            s.setToolTip("拖动框选后自动填入；也可直接改数字，停顿片刻自动重新识别")
            self.sp_rect.append(s); h.addWidget(s)
        bc = QPushButton("清除"); bc.clicked.connect(self.clear_anchor); h.addWidget(bc)
        lb = QLabel("框选自动按页面比例套用到每个文件")
        lb.setStyleSheet("color:#1a7f37;")
        lb.setToolTip("PDF / Word 页面尺寸不同时，框选坐标按宽高比例自动换算，无需设置")
        h.addWidget(lb)
        h.addStretch(1); self.ctl_stack.addWidget(w_page)

        # --- 模式② 控制 ---
        w_grid = QWidget(); h = QHBoxLayout(w_grid); h.setContentsMargins(0, 0, 0, 0)
        h.addWidget(QLabel("工作表"))
        self.cmb_sheet = QComboBox(); self.cmb_sheet.setMinimumWidth(120)
        self.cmb_sheet.setToolTip("切换工作表默认应用到本批全部 Excel 文件")
        self.cmb_sheet.currentTextChanged.connect(self.on_sheet_changed); h.addWidget(self.cmb_sheet)
        h.addSpacing(10)
        h.addWidget(QLabel("点选格子，或填"))
        self.ed_cell = QLineEdit(); self.ed_cell.setPlaceholderText("A1 / B1 / C2 / B2:D4")
        self.ed_cell.setFixedWidth(150)
        self.ed_cell.returnPressed.connect(self.apply_cell_text); h.addWidget(self.ed_cell)
        bcell = QPushButton("定位"); bcell.clicked.connect(self.apply_cell_text); h.addWidget(bcell)
        bcl = QPushButton("单元格清单"); bcl.clicked.connect(self.show_cell_list); h.addWidget(bcl)
        bc = QPushButton("清除"); bc.clicked.connect(self.clear_anchor); h.addWidget(bc)
        h.addStretch(1); self.ctl_stack.addWidget(w_grid)

        self.ctl_stack.addWidget(QWidget())
        rv.addWidget(self.ctl_stack)

        ah = QHBoxLayout()
        self.lbl_anchor = QLabel("定位：未设置")
        self.lbl_anchor.setStyleSheet("color:#1a7f37;font-weight:bold;")
        ah.addWidget(self.lbl_anchor)
        self.lbl_cellval = QLabel("")
        self.lbl_cellval.setStyleSheet("color:#7c3a00;")
        self.lbl_cellval.setWordWrap(True)
        ah.addWidget(self.lbl_cellval, 1)
        rv.addLayout(ah)

        self.view_stack = QStackedWidget()
        self.scroll = QScrollArea(); self.scroll.setWidgetResizable(False)
        self.scroll.setAlignment(Qt.AlignCenter)
        self.page_view = PageView(); self.page_view.rectChanged.connect(self.on_rect_drawn)
        self.scroll.setWidget(self.page_view)
        self.view_stack.addWidget(self.scroll)
        self.grid_view = GridSelectView()
        self.grid_view.selected.connect(self.on_cell_selected)
        self.view_stack.addWidget(self.grid_view)
        self.view_stack.addWidget(QWidget())
        rv.addWidget(self.view_stack, 1)

        self.lbl_cur = QLabel("当前文件：—"); rv.addWidget(self.lbl_cur)
        split.addWidget(right)
        split.setStretchFactor(0, 1); split.setStretchFactor(1, 4)
        v.addWidget(split, 3)

        gb = QGroupBox("批量预览对比（识别不准可双击「识别内容」直接改，或改「新文件名」）")
        gv = QVBoxLayout(gb)
        self.tbl = QTableWidget(0, 4)
        self.tbl.setHorizontalHeaderLabels(["原文件名", "识别内容（双击可校正）", "新文件名", "状态"])
        for c in (0, 1, 2):
            self.tbl.horizontalHeader().setSectionResizeMode(c, QHeaderView.Stretch)
        self.tbl.horizontalHeader().setSectionResizeMode(3, QHeaderView.ResizeToContents)
        self.tbl.setEditTriggers(QAbstractItemView.DoubleClicked | QAbstractItemView.EditKeyPressed)
        self.tbl.itemChanged.connect(self.on_table_edit)
        self.tbl.currentCellChanged.connect(lambda r, c, *_: self.on_select(r) if r >= 0 else None)
        gv.addWidget(self.tbl)
        v.addWidget(gb, 2)

        op = QHBoxLayout()
        lb_skip = QLabel("识别为空自动跳过")
        lb_skip.setStyleSheet("color:#1a7f37;")
        lb_skip.setToolTip("没取到内容的文件不参与重命名，保持原名不变")
        op.addWidget(lb_skip)
        op.addSpacing(14)
        op.addWidget(QLabel("名称上限"))
        self.sp_len = QSpinBox(); self.sp_len.setRange(10, 200); self.sp_len.setValue(80)
        op.addWidget(self.sp_len)
        op.addStretch(1)
        br = QPushButton("重新识别全部"); br.clicked.connect(self.apply_anchor); op.addWidget(br)
        bdg = QPushButton("定位说明"); bdg.clicked.connect(self.show_diagnose); op.addWidget(bdg)
        bu = QPushButton("撤销上次"); bu.clicked.connect(self.undo); op.addWidget(bu)
        self.btn_go = QPushButton("一键批量重命名")
        self.btn_go.setStyleSheet("QPushButton{background:#1a73e8;color:white;padding:7px 18px;"
                                  "font-weight:bold;border-radius:4px;}")
        self.btn_go.clicked.connect(self.do_rename); op.addWidget(self.btn_go)
        v.addLayout(op)
        self.sp_len.valueChanged.connect(lambda _: self.compute_names())

        root.setStyleSheet("""
            QWidget{font-family:'Microsoft YaHei','Segoe UI';font-size:12px;}
            QGroupBox{font-weight:bold;border:1px solid #d6dae1;border-radius:5px;margin-top:8px;padding-top:6px;}
            QGroupBox::title{subcontrol-origin:margin;left:10px;padding:0 4px;}
            QPushButton{padding:5px 12px;}
            QTableWidget{gridline-color:#e3e6ea;}
            QHeaderView::section{background:#f2f4f7;padding:5px;border:0;border-bottom:1px solid #d6dae1;}
        """)

    # ---------------- 环境 / CRC ----------------
    def _refresh_env(self):
        so = find_soffice()
        self.lbl_env.setText("● PDF / Word / Excel 全部免外部依赖" if not so
                             else "● 已就绪（Word 原排版转换可用）")
        self.lbl_env.setStyleSheet("color:#1a7f37;")
        self.lbl_env.setToolTip(
            "模式① PDF / Word：页面图框选 + 内置中文 OCR\n"
            "  .doc / .docx 不需装 Office：优先试 LibreOffice / Word / WPS 导出原排版，"
            "都不行时自动内置抽取正文重排（中英文正常显示，按本预览拖框即可）。\n"
            "模式② Excel：.xls / .xlsx 都支持，按原表还原，中文不乱码。")

    def _refresh_crc(self):
        p = os.environ.get("FAR_SELF", "")
        if not p or not Path(p).exists():
            return
        try:
            c = 0
            with open(p, "rb") as f:
                while True:
                    b = f.read(1 << 20)
                    if not b:
                        break
                    c = zlib.crc32(b, c)
            self.lbl_crc.setText(f" EXE CRC32：{c & 0xffffffff:08X} ")
        except Exception:
            pass

    # ---------------- 拖放导入 ----------------
    def _enable_drop(self):
        self.setAcceptDrops(True)
        self._drop_filter = DropFilter(self)
        for wd in self.findChildren(QWidget):
            wd.setAcceptDrops(True)
            wd.installEventFilter(self._drop_filter)

    def dragEnterEvent(self, e):
        if e.mimeData().hasUrls():
            e.acceptProposedAction()

    def dragMoveEvent(self, e):
        if e.mimeData().hasUrls():
            e.acceptProposedAction()

    def dropEvent(self, e):
        if e.mimeData().hasUrls():
            self.handle_drop(e.mimeData().urls())
            e.acceptProposedAction()

    def handle_drop(self, urls):
        """支持一次拖入多个文件 / 文件夹；文件夹只取顶层受支持文件"""
        paths: list[Path] = []
        for u in urls:
            local = u.toLocalFile()
            if not local:
                continue
            p = Path(local)
            try:
                if p.is_dir():
                    paths.extend(x for x in sorted(p.iterdir())
                                 if x.is_file() and x.suffix.lower() in SUPPORTED)
                elif p.is_file() and p.suffix.lower() in SUPPORTED:
                    paths.append(p)
            except Exception:
                continue
        n0 = len(self.items)
        self._append(paths)
        n = len(self.items) - n0
        if n:
            self.statusBar().showMessage(f"拖入成功：新增 {n} 个文件", 7000)
        elif paths:
            self.statusBar().showMessage("拖入的文件都已存在，或没有可识别的格式", 7000)
        else:
            self.statusBar().showMessage("没有可导入的文件（支持 PDF / Word / Excel）", 7000)

    # ---------------- 导入 ----------------
    def add_files(self):
        fs, _ = QFileDialog.getOpenFileNames(
            self, "选择文件", "", "支持的文件 (*.pdf *.doc *.docx *.xls *.xlsx);;所有文件 (*)")
        self._append([Path(f) for f in fs])

    def add_dir(self):
        d = QFileDialog.getExistingDirectory(self, "选择文件夹")
        if d:
            # 只扫路径（毫秒级），绝不在这步解析文件内容
            try:
                with os.scandir(d) as it:
                    paths = [Path(e.path) for e in it
                             if e.is_file() and
                             Path(e.name).suffix.lower() in SUPPORTED]
            except Exception:
                paths = [p for p in Path(d).iterdir()
                         if p.suffix.lower() in SUPPORTED]
            self._append(paths)

    def _append(self, paths):
        exist = {str(i.path.resolve()) for i in self.items}
        added = 0
        for p in paths:
            if p.suffix.lower() not in SUPPORTED or str(p.resolve()) in exist:
                continue
            # 导入阶段只记录路径：Excel 工作表名、Word 转 PDF 全部交给后台线程，
            # 主线程不做任何解析，文件列表立刻就能显示出来。
            self.items.append(FileItem(path=p, kind=kind_of(p)))
            exist.add(str(p.resolve())); added += 1
        if not added:
            return
        self.rebuild_list()
        if self.template_idx >= len(self.items):
            self.template_idx = 0
        self.mark_template()
        self.statusBar().showMessage(
            f"已加入 {added} 个文件，共 {len(self.items)} 个；后台读取中，列表可立即点选", 6000)
        self.prepare_all()                   # worker 在跑时内部会自动排队

    def clear_all(self):
        self.items.clear()
        self.anchor = Anchor(); self.tpl_page_size = None
        self.cur_idx = -1; self.template_idx = 0
        self.rebuild_list(); self.tbl.setRowCount(0)
        self.page_view.set_hint("请导入文件，或直接把文件 / 文件夹拖进窗口")
        self.grid_view.set_sheet(None)
        self.lbl_anchor.setText("定位：未设置")
        self.lbl_cellval.setText("")
        self.lbl_cur.setText("当前文件：—")
        self.lbl_mode.setText("模式：未导入")
        self.mark_template()

    def rebuild_list(self):
        self.lst.blockSignals(True); self.lst.clear()
        for i, it in enumerate(self.items):
            item = QListWidgetItem(it.path.name, self.lst)   # 纯文件名，前面不加任何符号
            if i == self.template_idx:
                f = item.font(); f.setBold(True); item.setFont(f)
                item.setBackground(QColor("#dbeafe"))
                item.setToolTip("模板文件（用于设定框选 / 格子位置）")
        self.lst.blockSignals(False)

    def mark_template(self):
        self.rebuild_list()
        if 0 <= self.template_idx < len(self.items):
            it = self.items[self.template_idx]
            self.lbl_tpl.setText(f"模板：{it.path.name}")
            self.lbl_mode.setText(
                "模式 ① PDF / Word（含 .doc）：页面框选 + OCR" if it.mode == "page"
                else "模式 ② Excel（含 .xls）：选格子，格子里是什么就命名什么")
        else:
            self.lbl_tpl.setText("模板：未设置")
            self.lbl_mode.setText("模式：未导入")

    def set_template(self):
        i = self.cur_idx if self.cur_idx >= 0 else self.lst.currentRow()
        if 0 <= i < len(self.items):
            old = self.items[self.template_idx].mode if self.items else ""
            self.template_idx = i
            if self.items[i].mode != old:
                self.anchor = Anchor()
                self.tpl_page_size = None
            self.mark_template(); self.render_current()
            self.statusBar().showMessage(f"模板已设为「{self.items[i].path.name}」", 5000)

    # ---------------- 准备 ----------------
    def prepare_all(self):
        if not self.items:
            return
        if self.worker and self.worker.isRunning():
            self._pending_reload = True      # 统一排队：导入 / 切表 / 拖入都走这里
            return
        self.progress.setRange(0, len(self.items)); self.progress.setValue(0)
        self.progress.show(); self.btn_go.setEnabled(False)
        self.worker = PrepareWorker(self.items)
        self.worker.one_done.connect(self.on_prepare_one)
        self.worker.log.connect(lambda s: self.statusBar().showMessage(s))
        self.worker.finished_all.connect(self.on_prepare_done)
        self.worker.start()

    def on_prepare_one(self, idx, err):
        self.progress.setValue(idx + 1)
        if idx == self.cur_idx:
            self._sync_sheet_combo()
            self.render_current()
        if not self._ui_flush.isActive():
            self._ui_flush.start()

    def on_prepare_done(self):
        self._ui_flush.stop()
        self.progress.hide(); self.btn_go.setEnabled(True)
        self.mark_template()
        if 0 <= self.cur_idx < len(self.items):
            self._sync_sheet_combo()
        if self.cur_idx < 0 and self.items:
            self.lst.blockSignals(True); self.lst.setCurrentRow(0); self.lst.blockSignals(False)
            self.on_select(0)
        else:
            self.render_current()
        self.refresh_table()
        mode = self.items[self.template_idx].mode
        self.statusBar().showMessage(
            "模式①：在模板预览图上拖框选要 OCR 的区域" if mode == "page"
            else "模式②：点选格子（如 C2），或填 A1 / B1 / C2；预览已按原表还原合并与格式",
            9000)
        if self._pending_reload:
            self._pending_reload = False
            QTimer.singleShot(0, self.prepare_all)
            return
        if self._pending_anchor or self.anchor.valid():
            self._pending_anchor = False
            self.worker = None
            QTimer.singleShot(80, self.apply_anchor)

    # ---------------- 预览 ----------------
    def _sync_sheet_combo(self):
        """只用后台缓存好的工作表名，点文件时不再同步打开 Excel"""
        if not (0 <= self.cur_idx < len(self.items)):
            return
        it = self.items[self.cur_idx]
        self.cmb_sheet.blockSignals(True); self.cmb_sheet.clear()
        if it.kind == "excel":
            names = it.sheet_names
            if names:
                self.cmb_sheet.addItems(names)
                cur = it.sheet if it.sheet in names else names[0]
                self.cmb_sheet.setCurrentText(cur)
        self.cmb_sheet.setEnabled(it.kind == "excel")
        self.cmb_sheet.blockSignals(False)

    def on_select(self, row):
        if not (0 <= row < len(self.items)) or row == self.cur_idx:
            return
        self.cur_idx = row
        self._sync_sheet_combo()
        self.render_current(); self.refresh_table(highlight=row)

    def render_current(self):
        i = self.cur_idx
        if not (0 <= i < len(self.items)):
            return
        it = self.items[i]
        self.lbl_cur.setText(f"当前文件：{it.path.name}   状态：{it.status}   "
                             f"识别内容：{it.raw_text[:70]}")
        if it.hint:
            self.view_stack.setCurrentIndex(0)
            self.page_view.set_hint(it.hint)
            return
        if it.mode == "page":
            self.view_stack.setCurrentIndex(0); self.ctl_stack.setCurrentIndex(0)
            if not it.work_pdf:
                self.page_view.set_hint("尚未读取，点「重新识别全部」"); return
            try:
                pm, z = render_page(it.work_pdf, self.page, self.sp_zoom.value())
                self.page_view.set_page(pm, z, self.anchor.rect)
                if i == self.template_idx:
                    self.tpl_page_size = pdf_page_size(it.work_pdf, self.page)
                n = max(1, it.page_count)
                self.lbl_pages.setText(f"/ {n}")
                self.sp_page.blockSignals(True)
                self.sp_page.setMaximum(n); self.sp_page.setValue(min(self.page + 1, n))
                self.sp_page.blockSignals(False)
                if it.kind == "word" and it.work_note:
                    if "内置" in it.work_note:
                        tag = "【Word · 内置文本重排：版式与 WPS 不同，请按本预览重新拖框】"
                    else:
                        tag = "【Word · 原排版，与 WPS 版式一致】"
                    self.lbl_cur.setText(
                        f"当前文件：{it.path.name}   {tag} {it.work_note} ｜ "
                        f"状态：{it.status} ｜ 识别内容：{it.raw_text[:60]}")
            except Exception as e:
                self.page_view.set_hint(f"预览失败：{e}")
        elif it.mode == "grid":
            self.view_stack.setCurrentIndex(1); self.ctl_stack.setCurrentIndex(1)
            if it.sd is None:
                self.view_stack.setCurrentIndex(0)
                self.page_view.set_hint("尚未读取，点「重新识别全部」"); return
            self.grid_view.set_sheet(it.sd)
            if self.anchor.kind == "grid" and self.anchor.valid():
                self.grid_view.show_anchor(self.anchor)
            n = sum(1 for row in it.sd.values for x in row if x.strip())
            ext_tag = ".xls 旧格式" if it.path.suffix.lower() == ".xls" else ".xlsx"
            self.lbl_cur.setText(
                f"当前文件：{it.path.name}   【Excel 原表视图 · {ext_tag}】{it.sd.sheet_name} ｜ "
                f"{it.sd.nrows()} 行 × {it.sd.ncols()} 列 ｜ 非空 {n} 格 ｜ "
                f"合并 {len(it.sd.merged)} 处 ｜ 中文由程序绘制，不转 PDF 不会乱码"
                + (f" ｜ {it.sd.note}" if it.sd.note else ""))
        else:
            self.view_stack.setCurrentIndex(2); self.ctl_stack.setCurrentIndex(2)

    def on_page_changed(self, val):
        self.page = max(0, val - 1)
        self.tpl_page_size = None
        self.render_current(); self.apply_anchor()

    def on_sheet_changed(self, text):
        if not text or self._loading:
            return
        targets = [x for x in self.items if x.kind == "excel"]
        for t in targets:
            t.sheet = text; t.sd = None; t.prepared = False
        if targets:
            self.prepare_all()

    # ---------------- 定位锚点 ----------------
    def on_rect_drawn(self, r):
        w, h = r[2] - r[0], r[3] - r[1]
        if w < 12 or h < 6:
            self.statusBar().showMessage("框太小，请重新拖一个能包住整行文字的框", 6000)
            return
        self.anchor = Anchor(kind="page", rect=list(r))
        self._sync_rect_spin()
        self.apply_anchor()

    def _rect_spin_changed(self, *_):
        if self._loading:
            return
        self.lbl_anchor.setText("坐标调整中…（停顿片刻自动重新识别）")
        self._rect_timer.start()      # 防抖：停止输入 450ms 后自动套用，无需点按钮

    def _sync_rect_spin(self):
        self._loading = True
        self._rect_timer.stop()
        r = (self.anchor.rect or [0, 0, 0, 0])
        for s, x in zip(self.sp_rect, r):
            s.setValue(x)
        self._loading = False

    def apply_rect_spin(self):
        """坐标数值改动后的自动套用入口（不再有「应用」按钮）"""
        r = [s.value() for s in self.sp_rect]
        if r[2] <= r[0] or r[3] <= r[1]:
            self.statusBar().showMessage("x1/y1 必须大于 x0/y0，已忽略本次坐标修改", 5000)
            return
        self.anchor = Anchor(kind="page", rect=r)
        self.page_view.set_rect_pt(r)
        self.apply_anchor()

    def _norm_grid_anchor(self, a: Anchor) -> Anchor:
        """单格落在合并区时归一到左上角，跟 WPS 里点合并格的行为一致"""
        if not (0 <= self.cur_idx < len(self.items)):
            return a
        sd = self.items[self.cur_idx].sd
        if sd is None or (a.r0, a.c0) != (a.r1, a.c1):
            return a
        rr, cc = sd.owner.get((a.r0, a.c0), (a.r0, a.c0))
        if (rr, cc) != (a.r0, a.c0):
            a.r0, a.c0, a.r1, a.c1 = rr, cc, rr, cc
        return a

    def on_cell_selected(self, r0, c0, r1, c1):
        self.anchor = self._norm_grid_anchor(
            Anchor(kind="grid", r0=r0, c0=c0, r1=r1, c1=c1))
        self.ed_cell.setText(f"{col_name(self.anchor.c0)}{self.anchor.r0 + 1}"
                             + (f":{col_name(c1)}{r1 + 1}"
                                if (self.anchor.r0, self.anchor.c0) != (self.anchor.r1, self.anchor.c1) else ""))
        self.apply_anchor()

    def apply_cell_text(self):
        s = self.ed_cell.text().strip()
        if not s:
            return
        m = re.match(r"^([A-Za-z]+)(\d+)(?::([A-Za-z]+)(\d+))?$", s)
        if not m:
            QMessageBox.information(self, "格式", "请填 A1 / B1 / C2 或 B2:D4 这样的格子地址"); return
        c0 = col_index(m.group(1)); r0 = int(m.group(2)) - 1
        c1 = col_index(m.group(3)) if m.group(3) else c0
        r1 = int(m.group(4)) - 1 if m.group(4) else r0
        self.anchor = self._norm_grid_anchor(
            Anchor(kind="grid", r0=min(r0, r1), c0=min(c0, c1),
                   r1=max(r0, r1), c1=max(c0, c1)))
        self.grid_view.show_anchor(self.anchor)
        self.apply_anchor()

    def show_cell_list(self):
        i = self.cur_idx if 0 <= self.cur_idx < len(self.items) else 0
        if not (0 <= i < len(self.items)):
            QMessageBox.information(self, "提示", "请先导入 Excel 文件"); return
        it = self.items[i]
        if it.mode != "grid" or it.sd is None:
            QMessageBox.information(self, "提示", "单元格清单只在 Excel 模式下可用"); return
        d = CellListDialog(it.sd, self)
        d.cellChosen.connect(self._anchor_from_cell)
        d.exec()

    def _anchor_from_cell(self, r, c):
        self.anchor = self._norm_grid_anchor(Anchor(kind="grid", r0=r, c0=c, r1=r, c1=c))
        self.ed_cell.setText(f"{col_name(self.anchor.c0)}{self.anchor.r0 + 1}")
        self.grid_view.show_anchor(self.anchor)
        self.apply_anchor()

    def clear_anchor(self):
        self.anchor = Anchor()
        self.page_view.set_rect_pt(None)
        self._sync_rect_spin()
        self.grid_view.clearSelection()
        self.ed_cell.clear()
        for it in self.items:
            it.raw_text = ""; it.via = ""
            it.raw_manual = False
            if not it.manual:
                it.status = "待定位"
        self.lbl_anchor.setText("定位：未设置")
        self.lbl_cellval.setText("")
        self.refresh_table(); self.compute_names()

    # ---------------- 抽取 ----------------
    def rects_all(self):
        base = self.anchor.rect
        if not base:
            return [None] * len(self.items)
        if not self.tpl_page_size and 0 <= self.template_idx < len(self.items):
            t = self.items[self.template_idx]
            if t.mode == "page" and t.work_pdf:
                self.tpl_page_size = pdf_page_size(t.work_pdf, self.page)
        out = []
        for it in self.items:
            if (not self.tpl_page_size
                    or it.mode != "page" or not it.work_pdf):
                out.append(base); continue
            sz = pdf_page_size(it.work_pdf, self.page)
            if not sz or not sz[0] or not sz[1]:
                out.append(base); continue
            tw, th = self.tpl_page_size
            sx, sy = sz[0] / tw, sz[1] / th
            out.append([base[0] * sx, base[1] * sy, base[2] * sx, base[3] * sy])
        return out

    def _update_cellval(self):
        a = self.anchor
        if a.kind != "grid" or not (0 <= self.cur_idx < len(self.items)):
            self.lbl_cellval.setText("")
            return
        sd = self.items[self.cur_idx].sd
        if sd is None:
            self.lbl_cellval.setText(""); return
        rr, cc = sd.owner.get((a.r0, a.c0), (a.r0, a.c0))
        v = sd.values[rr][cc] if (0 <= rr < sd.nrows() and cc < len(sd.values[rr])) else ""
        txt = f"当前格子 {col_name(cc)}{rr + 1} 完整内容：{v}"
        texts = [x.raw_text.strip() for x in self.items
                 if x.mode == "grid" and x.raw_text.strip()]
        if len(texts) >= 2 and len(set(texts)) == 1:
            txt += "　⚠ 所有文件这个格子内容相同（多为表头），重名会自动加（1）；建议改选项目编码/项目名称所在格"
        if sd.note:
            txt += "　｜ " + sd.note
        self.lbl_cellval.setText(txt)

    def apply_anchor(self):
        a = self.anchor
        self.lbl_anchor.setText("定位：" + a.desc() if a.valid() else "定位：未设置")
        if not self.items:
            return
        if (self.worker and self.worker.isRunning()) or (self.ocr_worker and self.ocr_worker.isRunning()):
            self._pending_anchor = True      # 读取/OCR 中用户改了定位，结束后自动补算
            return
        tpl_mode = self.items[self.template_idx].mode
        rects = self.rects_all()
        tpl_size = self.tpl_page_size
        jobs, mismatch = [], []
        for i, it in enumerate(self.items):
            if it.raw_manual:
                continue      # 用户已手工校正这一格的识别内容，重新识别不覆盖它
            if it.mode != tpl_mode or it.mode != a.kind:
                it.raw_text = ""; it.via = ""
                if not it.manual:
                    it.status = "类型不符·跳过" if it.mode != tpl_mode else "待定位"
                continue
            if not a.valid():
                it.raw_text = ""; it.via = ""
                if not it.manual:
                    it.status = "待定位"
                continue
            if it.status == "转换失败":
                continue
            if it.mode == "grid":
                t, note = grid_extract(it.sd, a)
                it.raw_text, it.via = t, note
                if not it.manual:
                    it.status = "已取格子" if t else "该格为空"
            else:
                if it.work_pdf:
                    if tpl_size:
                        sz = pdf_page_size(it.work_pdf, self.page)
                        if sz and sz[0] and sz[1] and (
                            abs(sz[0] / tpl_size[0] - 1) > 0.30
                            or abs(sz[1] / tpl_size[1] - 1) > 0.30
                        ):
                            mismatch.append(it.path.name)
                    jobs.append((i, it.work_pdf, rects[i]))
                    it.raw_text = ""; it.via = ""
                    if not it.manual:
                        it.status = "OCR 中…"
        self.refresh_table(); self.compute_names(); self._update_cellval()
        self._size_warn = (
            f"｜{len(mismatch)} 个文件页面尺寸差异大，已自动按比例换算，识别为空请重拖框选"
            if mismatch else "")
        if jobs:
            self.progress.setRange(0, len(jobs)); self.progress.setValue(0); self.progress.show()
            self.ocr_worker = OcrWorker(jobs, self.page)
            self.ocr_worker.one_done.connect(self.on_ocr_done)
            self.ocr_worker.log.connect(lambda s: self.statusBar().showMessage(s))
            self.ocr_worker.finished_all.connect(self.on_ocr_done_all)
            self.ocr_worker.start()

    def on_ocr_done(self, idx, text, note):
        it = self.items[idx]
        if not it.raw_manual:
            it.raw_text, it.via = text, note
            if not it.manual:
                it.status = "OCR 识别" if text else "OCR 无结果"
        self.progress.setValue(min(self.progress.value() + 1, self.progress.maximum()))
        self.refresh_table(); self.compute_names()
        if idx == self.cur_idx:
            self.render_current()

    def on_ocr_done_all(self):
        self.progress.hide()
        self.statusBar().showMessage(
            "OCR 完成：不对可双击「识别内容」直接改（新文件名会跟着变），或重新拖框"
            + self._size_warn, 9000)
        self.refresh_table(); self.compute_names()
        if self._pending_anchor:
            self._pending_anchor = False
            self.ocr_worker = None
            QTimer.singleShot(80, self.apply_anchor)

    # ---------------- 表格 / 命名 ----------------
    def refresh_table(self, highlight=-1):
        self._loading = True
        self.tbl.blockSignals(True)
        self.tbl.setRowCount(len(self.items))
        for i, it in enumerate(self.items):
            vals = [it.path.name, it.raw_text, it.new_name or "(待生成)", it.status]
            for c, val in enumerate(vals):
                cell = QTableWidgetItem(val)
                if c in (0, 3):
                    cell.setFlags(cell.flags() & ~Qt.ItemIsEditable)
                if c in (1, 2):
                    cell.setToolTip(val)
                if c == 1 and it.raw_manual:
                    cell.setForeground(QColor("#1a7f37"))
                if c == 3 and it.status.startswith(("该格为空", "OCR 无结果", "转换失败", "类型不符")):
                    cell.setForeground(QColor("#c62828"))
                self.tbl.setItem(i, c, cell)
        r = highlight if highlight >= 0 else self.cur_idx
        if 0 <= r < len(self.items):
            self.tbl.selectRow(r)
        self.tbl.blockSignals(False); self._loading = False

    def on_table_edit(self, item):
        if self._loading or item.column() not in (1, 2):
            return
        i = item.row()
        if not (0 <= i < len(self.items)):
            return
        it = self.items[i]
        if item.column() == 1:
            it.raw_text = item.text().strip()
            it.raw_manual = True
            it.manual = False
            it.status = "手工校正识别内容" if it.raw_text else "跳过(未取到内容)"
            QTimer.singleShot(0, self.compute_names)     # 同步刷新新文件名
        else:
            it.new_name = item.text().strip()
            it.status = "手工编辑"
            it.manual = True

    def compute_names(self):
        maxlen = self.sp_len.value()
        used = set()
        src_set = {str(it.path.resolve()).lower() for it in self.items}
        for it in self.items:
            if it.manual and it.new_name:
                used.add(it.new_name.lower()); continue
            if it.status.startswith(("类型不符", "转换失败")):
                it.new_name = ""
                continue
            base = clean_name(it.raw_text, maxlen)
            if not base:
                # 固定策略：识别为空就跳过，保持原名不变
                it.new_name, it.status = "", "跳过(未取到内容)"; continue
            it.status = "待重命名·" + (it.via or "")[:20]
            ext = it.path.suffix          # 固定保留原扩展名
            cand = base + ext; k = 1
            while cand.lower() in used or (
                (it.path.parent / cand).exists()
                and str((it.path.parent / cand).resolve()).lower() not in src_set):
                cand = f"{base}（{k}）{ext}"; k += 1
            used.add(cand.lower()); it.new_name = cand; it.manual = False
        self.refresh_table()

    # ---------------- 执行 ----------------
    def do_rename(self):
        if not self.items:
            return
        self.compute_names()
        todo = [(it, it.path.parent / it.new_name) for it in self.items
                if it.new_name and it.new_name != it.path.name
                and not it.status.startswith("跳过")]
        if not todo:
            QMessageBox.information(self, "提示", "没有需要重命名的文件（可能都没定位到内容）"); return
        ok, fail, ops, staged = 0, 0, [], []
        for k, (it, _) in enumerate(todo):
            tmp = it.path.parent / f"__far_tmp_{k}_{_token(it.path)}{it.path.suffix}"
            try:
                os.rename(it.path, tmp)
                staged.append((it, tmp, it.path.parent / it.new_name, it.path))
            except Exception as e:
                it.status = f"失败：{e}"; fail += 1
        for it, tmp, dst, old in staged:
            try:
                os.rename(tmp, dst)
                ops.append((old, dst)); it.path = dst; it.status = "✔ 已重命名"; ok += 1
            except Exception as e:
                try: os.rename(tmp, old)
                except Exception: pass
                it.status = f"失败：{e}"; fail += 1
        self.last_ops = ops
        self.refresh_table(); self.rebuild_list()
        QMessageBox.information(self, "完成", f"成功 {ok} 个，失败 {fail} 个。可用「撤销上次」回滚。")

    def undo(self):
        if not self.last_ops:
            QMessageBox.information(self, "提示", "没有可撤销的重命名"); return
        n = 0
        for old, new in reversed(self.last_ops):
            try:
                if new.exists(): os.rename(new, old); n += 1
            except Exception: pass
        for it in self.items:
            for old, new in reversed(self.last_ops):
                if str(it.path) == str(new):
                    it.path = old; it.status = "已撤销"
        self.last_ops = []
        self.refresh_table(); self.rebuild_list()
        QMessageBox.information(self, "撤销", f"已回滚 {n} 个文件")

    # ---------------- 定位说明 ----------------
    def show_diagnose(self):
        if not self.items:
            QMessageBox.information(self, "提示", "请先导入文件"); return
        tpl = self.items[self.template_idx]
        it = self.items[max(0, self.cur_idx)]
        a = self.anchor
        L = [f"模板：{tpl.path.name}", f"当前模式：{tpl.mode}", f"当前定位：{a.desc()}", ""]
        if tpl.mode == "page":
            L += ["模式 ① PDF / Word：页面框选 + OCR",
                  "  PDF 直接用；Word 先无头转成工作 PDF，两种共用同一套框选坐标。",
                  "  每个文件只对自己框内的图像做内置中文 OCR，不读整页文本层凑数。",
                  "  框边切到半个字时只向外补到该字笔画结束，把完整字带进来，不会整列乱扩。",
                  "  已自动擦除框内表格线、长框分片识别、多候选投票，避免把边框线认成多余的字。",
                  "  框内若有文字层，只用来去掉 OCR 多识别的尾字，不替换整体结果。",
                  "  页面尺寸不同时框选自动按宽高比例换算，无需设置。",
                  "  仍认错可双击「识别内容」直接改，新文件名会跟着变。",
                  "  Word 优先试 LibreOffice / Word / WPS COM 导出原排版；都不行才内置抽取正文重排。",
                  "  若显示「内置文本重排」：版式与 WPS 不同，请按本软件预览图重新拖框；"
                  "中英文已正常显示，数字不会再被拉成散字。",
                  f"  当前转换方式：{it.work_note or '—'}", ""]
            if it.work_pdf:
                try:
                    d = fitz.open(it.work_pdf)
                    pg = d[max(0, min(self.page, d.page_count - 1))]
                    L += [f"  当前文件：{it.path.name}",
                          f"  页码 {self.page + 1}/{d.page_count}  尺寸 {pg.rect.width:.0f}×{pg.rect.height:.0f} pt"]
                    d.close()
                except Exception as e:
                    L.append(f"  诊断失败：{e}")
        else:
            sd = it.sd
            if sd:
                nonempty = [(f"{col_name(c)}{r + 1}", v)
                            for r, row in enumerate(sd.values) for c, v in enumerate(row) if v.strip()]
                L += ["模式 ② Excel：选格子，格子里是什么就命名什么",
                      "  预览 1:1 还原原表：合并单元格 / 列宽 / 行高 / 底色 / 居中 / 粗体 / 换行 / 数字格式。",
                      "  中文由 Qt 直接绘制，绝不转 PDF，不会乱码；文字完整不省略（悬停格子可看全文）。",
                      "  点选/拖选格子，或填 A1、B1、C2、B2:D4；也可点「单元格清单」双击选。",
                      "  合并区点任意位置会自动归一到左上角那个真实有值的格子。", "",
                      f"  当前文件：{it.path.name}  工作表：{sd.sheet_name}",
                      f"  {sd.nrows()} 行 × {sd.ncols()} 列 ｜ 非空 {len(nonempty)} 格 ｜ 合并 {len(sd.merged)} 处",
                      f"  合并区域：{', '.join(f'{col_name(c0)}{r0+1}:{col_name(c1)}{r1+1}' for r0,c0,r1,c1 in sd.merged[:12]) or '无'}", ""]
                if sd.note:
                    L += ["  ⚠ " + sd.note, ""]
                L += ["  全部非空单元格（核对内容有没有少 / 有没有乱）："]
                L += [f"    {addr:<6s} = {v}" for addr, v in nonempty[:120]]
                if len(nonempty) > 120:
                    L.append(f"    …… 其余 {len(nonempty) - 120} 格请用「单元格清单」查看")
        L += ["", "定位：", a.desc(), "状态：", it.status]
        dlg = QMessageBox(self)
        dlg.setWindowTitle("定位说明 / 诊断")
        dlg.setIcon(QMessageBox.Information)
        dlg.setText("当前用的是哪一种定位方式")
        dlg.setDetailedText("\n".join(L))
        dlg.setStyleSheet("QTextEdit{min-width:700px;min-height:420px;"
                          "font-family:Consolas,monospace;font-size:11px;}")
        dlg.exec()


# ============================ 入口 ============================
def self_build():
    deps = ["PySide6", "pymupdf", "python-docx", "openpyxl", "xlrd==2.0.1",
            "numpy", "onnxruntime", "pyinstaller"] + (["pywin32"] if sys.platform == "win32" else [])
    subprocess.check_call([sys.executable, "-m", "pip", "install", "-q"] + deps)
    subprocess.check_call([sys.executable, "-m", "PyInstaller", "--onefile", "--windowed",
                           "--clean", "--name", "FileAreaRenamer",
                           "--collect-all", "PySide6", "--collect-all", "pymupdf",
                           "--collect-all", "openpyxl", "--collect-all", "xlrd",
                           str(Path(__file__).resolve())])
    print("\n完成：dist/FileAreaRenamer.exe（OCR 模型需另放 ocr/rec.onnx）")


if __name__ == "__main__":
    if "--build" in sys.argv:
        self_build(); sys.exit(0)
    if "--selftest" in sys.argv:
        os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")
    app = QApplication(sys.argv)
    w = MainWindow()
    if "--selftest" not in sys.argv:
        w.show()
    else:
        print("selftest: MainWindow 构建成功，模块导入正常")
        sys.exit(0)
    sys.exit(app.exec())
