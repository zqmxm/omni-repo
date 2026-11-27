# -*- coding: utf-8 -*-
"""
夸克扫描王 · 批量文件入口（内嵌版）  QuarkBatch v1.0
=====================================================================
软件只做三件事，边界写死，绝不越线：

  ① 批量入口：管理一份 PDF 队列，把「当前文件」放进剪贴板、
     在资源管理器里选中，方便你在右侧网页 Ctrl+V 粘贴上传。
  ② 内嵌网页：窗口里直接嵌夸克扫描王网页（Qt WebEngine）。
     「登录」「点去水印 / 去手写」「点下载 PDF」全部由你本人点。
     登录态由浏览器组件自己持久化在软件专用 profile 里，扫码一次即可。
  ③ 批量出口：网页下载一旦完成，软件自动把成品 PDF 归集到你指定的
     输出文件夹并改名；随后自动切到下一个、备好下一个文件。

软件不保存账号、不读 Cookie、不注入 JS、不模拟点击、不抓接口、不绕过会员。
处理完全由你的会员账号在夸克扫描王网站上完成。仅处理有权限改的资料。

技术说明：
  · 内嵌页用 PySide6 QtWebEngineWidgets（自带 Chromium 内核，随 exe 打包）。
  · 登录态存在 %APPDATA%\\QuarkBatch\\webprofile，重启软件不用再登。
  · 下载通过 QWebEngineProfile.downloadRequested 接管，直接落到输出目录。
  · 若 WebEngine 组件加载失败，右侧给出占位说明，自动降级为
    「用系统浏览器打开 + 轮询下载目录归集」，队列与归集功能仍可用。
"""

from __future__ import annotations

# —— 必须在 import PySide6 / 创建 QApplication 之前设好 ——
import os
import sys

os.environ.setdefault("QTWEBENGINE_DISABLE_SANDBOX", "1")
os.environ.setdefault("QTWEBENGINE_CHROMIUM_FLAGS", "--no-sandbox --disable-gpu-sandbox")
os.environ.setdefault("PYTHONUTF8", "1")
os.environ.setdefault("PYTHONIOENCODING", "utf-8")


def _setup_qtwebengine_paths():
    """自解压运行时里，显式告诉 WebEngine 资源/语言包在哪，避免内嵌页白屏。"""
    cand = []
    bd = os.environ.get("FAR_BUNDLE_DIR", "").strip()
    if bd:
        cand.append(os.path.join(bd, "Lib", "site-packages", "PySide6"))
    try:
        import PySide6  # noqa
        cand.append(os.path.dirname(PySide6.__file__))
    except Exception:
        pass
    for d in cand:
        r = os.path.join(d, "resources")
        l = os.path.join(d, "translations", "qtwebengine_locales")
        if os.path.isdir(r):
            os.environ.setdefault("QTWEBENGINE_RESOURCES_PATH", r)
        if os.path.isdir(l):
            os.environ.setdefault("QTWEBENGINE_LOCALES_PATH", l)
        if os.path.isdir(r):
            break


_setup_qtwebengine_paths()

import json
import re
import shutil
import subprocess
import time
import webbrowser
import zlib
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path

from PySide6.QtCore import Qt, QUrl, QTimer, QMimeData, QSize
from PySide6.QtGui import QColor, QFont, QDesktopServices
from PySide6.QtWidgets import (
    QApplication, QMainWindow, QWidget, QLabel, QPushButton, QLineEdit,
    QTableWidget, QTableWidgetItem, QFileDialog, QMessageBox, QHBoxLayout,
    QVBoxLayout, QGroupBox, QHeaderView, QAbstractItemView, QCheckBox,
    QTextEdit, QSplitter, QStatusBar, QFrame, QComboBox, QToolButton,
)

try:
    from PySide6.QtWebEngineWidgets import QWebEngineView
    from PySide6.QtWebEngineCore import (
        QWebEngineProfile, QWebEnginePage, QWebEngineDownloadRequest,
    )
    HAS_WEB = True
    WEB_ERR = ""
except Exception as e:                       # Linux 自测 / 极端加载失败时降级
    HAS_WEB = False
    WEB_ERR = f"{type(e).__name__}: {e}"
    QWebEngineView = None
    QWebEngineProfile = QWebEnginePage = QWebEngineDownloadRequest = None

APP_NAME = "QuarkBatch"
VERSION = "v1.0"
HOME_URL = "https://scan.quark.cn/"


# ============================ 路径 / 配置 ============================
def exe_dir() -> Path:
    d = os.environ.get("FAR_EXE_DIR", "").strip()
    if d and Path(d).exists():
        return Path(d)
    if getattr(sys, "frozen", False):
        return Path(sys.executable).parent
    return Path(__file__).parent


def appdata_dir() -> Path:
    base = os.environ.get("APPDATA") or os.environ.get("LOCALAPPDATA")
    p = Path(base) / APP_NAME if base else (Path.home() / f".{APP_NAME}")
    try:
        p.mkdir(parents=True, exist_ok=True)
    except Exception:
        p = exe_dir()
    return p


CONFIG_PATH = appdata_dir() / "config.json"
PROFILE_PATH = appdata_dir() / "webprofile"

DEFAULTS = {
    "out_dir": "",
    "web_url": HOME_URL,
    "name_template": "{stem}_已处理",
    "auto_next": True,
    "auto_clip": True,
    "poll_fallback": True,
}


def load_config() -> dict:
    c = dict(DEFAULTS)
    try:
        if CONFIG_PATH.exists():
            c.update(json.loads(CONFIG_PATH.read_text("utf-8")))
    except Exception:
        pass
    return c


def save_config(c: dict):
    try:
        CONFIG_PATH.write_text(json.dumps(c, ensure_ascii=False, indent=2), "utf-8")
    except Exception:
        pass


def clean_name(s: str, limit: int = 120) -> str:
    s = re.sub(r'[<>:"/\\|?*\x00-\x1f]', "", str(s or "")).strip(" .")
    s = re.sub(r"\s+", " ", s)[:limit].strip(" .")
    if s.upper() in ("CON", "PRN", "AUX", "NUL", "COM1", "LPT1"):
        s = "_" + s
    return s or "扫描件"


def human_size(n: int) -> str:
    if n < 1024:
        return f"{n}B"
    if n < 1024 * 1024:
        return f"{n / 1024:.1f}KB"
    return f"{n / 1024 / 1024:.1f}MB"


def pdf_pages(p: Path) -> int:
    try:
        import pymupdf as fitz
        d = fitz.open(p)
        n = d.page_count
        d.close()
        return n
    except Exception:
        try:
            import fitz
            d = fitz.open(p); n = d.page_count; d.close()
            return n
        except Exception:
            return 0


@dataclass
class Job:
    path: Path
    pages: int = 0
    size: str = ""
    status: str = "待处理"
    out_name: str = ""
    note: str = ""

    def __post_init__(self):
        try:
            self.size = human_size(self.path.stat().st_size)
        except Exception:
            self.size = "—"
        self.pages = pdf_pages(self.path)


# ============================ 主窗口 ============================
class MainWindow(QMainWindow):
    def __init__(self):
        super().__init__()
        self.cfg = load_config()
        self.jobs: list[Job] = []
        self.cur = -1                  # 当前处理中的行
        self.running = False
        self._seen_downloads: set[str] = set()
        self._batch_started = 0.0
        self._collected_for_current = False
        self.web = None
        self.profile = None
        self.setWindowTitle(f"夸克扫描王 · 批量文件入口（内嵌版）  {APP_NAME} {VERSION}")
        self.resize(1180, 760)
        self.setAcceptDrops(True)
        self._build()
        self._cfg_to_ui()
        QTimer.singleShot(250, self._refresh_crc)
        QTimer.singleShot(120, self._init_web)
        self._poll = QTimer(self)
        self._poll.setInterval(1200)
        self._poll.timeout.connect(self._poll_downloads)
        self._poll.start()
        self._log("欢迎使用。① 点「登录」在内嵌网页里扫码/验证码登录你的会员账号（一次即可）")
        self._log("② 导入 PDF → 选输出文件夹 → 点「开始」；右侧网页里粘贴 → 点去水印/去手写 → 点下载 PDF")
        self._log("③ 下载完成软件自动归集改名，并自动切到下一个、备好下一个文件")
        self._log("软件不保存账号、不读 Cookie、不注入网页、不模拟点击。仅处理有权限改的资料。")
        if not HAS_WEB:
            self._log(f"⚠ 内嵌网页组件未加载（{WEB_ERR}），已降级：可用「用系统浏览器打开」+ 下载目录归集")

    # ---------------- 界面 ----------------
    def _build(self):
        split = QSplitter(Qt.Horizontal)
        split.addWidget(self._left_panel())
        split.addWidget(self._right_panel())
        split.setStretchFactor(0, 0)
        split.setStretchFactor(1, 1)
        split.setSizes([430, 730])
        self.setCentralWidget(split)

        self.setStatusBar(QStatusBar())
        self.lbl_crc = QLabel("CRC32：计算中…")
        self.statusBar().addPermanentWidget(self.lbl_crc)
        self.lbl_stat = QLabel("就绪")
        self.statusBar().addWidget(self.lbl_stat)

    def _left_panel(self) -> QWidget:
        w = QWidget(); v = QVBoxLayout(w)
        v.setContentsMargins(8, 8, 8, 8)

        # 队列按钮
        h = QHBoxLayout()
        for text, slot in (("添加 PDF 文件", self.add_files),
                           ("添加文件夹", self.add_dir),
                           ("移除选中", self.remove_selected),
                           ("清空队列", self.clear_jobs)):
            b = QPushButton(text); b.clicked.connect(slot); h.addWidget(b)
        v.addLayout(h)

        # 队列表
        self.tbl = QTableWidget(0, 5)
        self.tbl.setHorizontalHeaderLabels(["#", "文件名", "页", "大小", "状态"])
        hh = self.tbl.horizontalHeader()
        hh.setSectionResizeMode(0, QHeaderView.ResizeToContents)
        hh.setSectionResizeMode(1, QHeaderView.Stretch)
        hh.setSectionResizeMode(2, QHeaderView.ResizeToContents)
        hh.setSectionResizeMode(3, QHeaderView.ResizeToContents)
        hh.setSectionResizeMode(4, QHeaderView.ResizeToContents)
        self.tbl.setEditTriggers(QAbstractItemView.NoEditTriggers)
        self.tbl.setSelectionBehavior(QAbstractItemView.SelectRows)
        self.tbl.setAlternatingRowColors(True)
        self.tbl.setMinimumHeight(190)
        v.addWidget(self.tbl, 3)

        # 路径
        g = QGroupBox("路径"); gv = QVBoxLayout(g)
        r1 = QHBoxLayout(); r1.addWidget(QLabel("输出文件夹"))
        self.ed_out = QLineEdit(); self.ed_out.setPlaceholderText("成品 PDF 自动落到这里")
        self.ed_out.textChanged.connect(self._on_out)
        b1 = QPushButton("选择…"); b1.setFixedWidth(64); b1.clicked.connect(self.pick_out)
        r1.addWidget(self.ed_out, 1); r1.addWidget(b1); gv.addLayout(r1)

        r2 = QHBoxLayout(); r2.addWidget(QLabel("网页地址"))
        self.ed_url = QLineEdit(HOME_URL)
        b2 = QPushButton("打开页面"); b2.clicked.connect(self.open_page)
        b3 = QPushButton("刷新"); b3.clicked.connect(self.refresh_page)
        r2.addWidget(self.ed_url, 1); r2.addWidget(b2); r2.addWidget(b3)
        gv.addLayout(r2); v.addWidget(g)

        # 勾选项
        self.chk_auto_next = QCheckBox("归集成功后自动切到下一个")
        self.chk_auto_clip = QCheckBox("切到下一个时自动把文件放进剪贴板并在资源管理器选中")
        self.chk_auto_next.setChecked(True); self.chk_auto_clip.setChecked(True)
        v.addWidget(self.chk_auto_next); v.addWidget(self.chk_auto_clip)

        # 控制行
        h2 = QHBoxLayout()
        self.btn_start = QPushButton("开始"); self.btn_start.clicked.connect(self.start_batch)
        self.btn_start.setStyleSheet("QPushButton{background:#1a73e8;color:white;font-weight:bold;padding:6px 10px;border-radius:4px;}")
        self.btn_next = QPushButton("下一个 →"); self.btn_next.clicked.connect(lambda: self.next_job(True))
        self.btn_skip = QPushButton("跳过当前"); self.btn_skip.clicked.connect(self.skip_current)
        self.btn_done = QPushButton("标记完成"); self.btn_done.clicked.connect(self.mark_done)
        self.btn_stop = QPushButton("■ 停止"); self.btn_stop.clicked.connect(self.stop_batch)
        self.btn_stop.setEnabled(False)
        for b in (self.btn_start, self.btn_next, self.btn_skip, self.btn_done, self.btn_stop):
            h2.addWidget(b)
        v.addLayout(h2)

        h3 = QHBoxLayout()
        for text, slot in (("定位当前文件", self.locate_current),
                           ("复制文件到剪贴板", self.copy_current),
                           ("打开输出文件夹", self.open_out)):
            b = QPushButton(text); b.clicked.connect(slot); h3.addWidget(b)
        v.addLayout(h3)

        # 更多设置（默认收起，保持主界面跟参考图一致）
        self.more = QToolButton()
        self.more.setText("▸ 更多设置（命名模板 / 清除登录态 / 兼容模式）")
        self.more.setToolButtonStyle(Qt.ToolButtonTextOnly)
        self.more.setCheckable(True); self.more.setChecked(False)
        self.more.toggled.connect(self._toggle_more)
        v.addWidget(self.more)
        self.more_panel = QWidget(); self.more_panel.setVisible(False)
        mv = QVBoxLayout(self.more_panel)
        r4 = QHBoxLayout(); r4.addWidget(QLabel("输出命名模板"))
        self.ed_tpl = QLineEdit("{stem}_已处理")
        self.ed_tpl.setToolTip("{stem} = 原文件名；成品固定 .pdf")
        r4.addWidget(self.ed_tpl, 1); mv.addLayout(r4)
        self.chk_poll = QCheckBox("兼容模式：同时轮询系统下载文件夹归集（内嵌页下载未被接管时）")
        self.chk_poll.setChecked(True); mv.addWidget(self.chk_poll)
        r5 = QHBoxLayout()
        bcl = QPushButton("清除登录态（下次需重新扫码）"); bcl.clicked.connect(self.clear_profile)
        bs = QPushButton("保存设置"); bs.clicked.connect(self._save_cfg)
        r5.addWidget(bcl); r5.addWidget(bs); r5.addStretch(1); mv.addLayout(r5)
        v.addWidget(self.more_panel)

        # 日志
        self.log = QTextEdit(); self.log.setReadOnly(True)
        self.log.setMinimumHeight(110)
        self.log.setStyleSheet("QTextEdit{font-family:Consolas,'Microsoft YaHei';font-size:11px;}")
        v.addWidget(self.log, 2)

        note = QLabel("网页内「粘贴/拖入 → 点去水印或去手写 → 点下载 PDF」由你本人完成。\n"
                      "软件不保存账号、不读 Cookie、不注入网页、不模拟点击。仅处理有权限改的资料。")
        note.setStyleSheet("color:#8a8f98;font-size:10px;")
        note.setWordWrap(True)
        v.addWidget(note)
        return w

    def _right_panel(self) -> QWidget:
        w = QWidget(); v = QVBoxLayout(w)
        v.setContentsMargins(6, 8, 8, 8)

        top = QHBoxLayout()
        lb = QLabel("夸克扫描王 · 网页"); lb.setStyleSheet("font-weight:bold;")
        self.lbl_login = QLabel("● 未登录")
        self.lbl_login.setStyleSheet("color:#b26a00;font-weight:bold;")
        self.btn_login = QPushButton("登录 / 打开官网")
        self.btn_login.setStyleSheet("QPushButton{background:#1a73e8;color:white;padding:5px 14px;"
                                     "font-weight:bold;border-radius:4px;}")
        self.btn_login.clicked.connect(self.do_login)
        self.btn_ext = QPushButton("用系统浏览器打开")
        self.btn_ext.setToolTip("排障用；系统浏览器的登录态与内嵌页相互独立")
        self.btn_ext.clicked.connect(self.open_external)
        top.addWidget(lb); top.addWidget(self.lbl_login); top.addStretch(1)
        top.addWidget(self.btn_login); top.addWidget(self.btn_ext)
        v.addLayout(top)

        bar = QHBoxLayout()
        self.ed_addr = QLineEdit(HOME_URL); self.ed_addr.setReadOnly(True)
        self.ed_addr.setStyleSheet("QLineEdit{background:#f5f6f8;border:1px solid #e3e6ea;"
                                   "border-radius:3px;padding:4px;color:#555;}")
        bar.addWidget(self.ed_addr, 1); v.addLayout(bar)

        self.web_host = QWidget(); hv = QVBoxLayout(self.web_host)
        hv.setContentsMargins(0, 0, 0, 0)
        self.web_frame = QFrame()
        self.web_frame.setFrameShape(QFrame.StyledPanel)
        self.web_frame.setStyleSheet("QFrame{background:#fff;border:1px solid #e3e6ea;border-radius:4px;}")
        hv.addWidget(self.web_frame)
        v.addWidget(self.web_host, 1)

        tip = QLabel("↑ 以上是软件窗口内嵌的网页，不是系统浏览器；登录态留在软件里，扫码一次即可。\n"
                     "网页内「选择处理能力 → 去水印 / 去手写」和「下载 PDF」由你本人点，软件只负责队列与归集。")
        tip.setStyleSheet("color:#8a8f98;font-size:10px;")
        tip.setWordWrap(True)
        v.addWidget(tip)
        return w

    def _toggle_more(self, on):
        self.more.setText(("▾" if on else "▸") + " 更多设置（命名模板 / 清除登录态 / 兼容模式）")
        self.more_panel.setVisible(on)

    # ---------------- 内嵌网页 ----------------
    def _init_web(self):
        if not HAS_WEB:
            lay = QVBoxLayout(self.web_frame)
            lab = QLabel("内嵌网页组件未能加载。\n\n"
                         "Windows 版 exe 自带 Chromium 内核，正常会直接显示夸克扫描王网页。\n"
                         "此时可点右上「用系统浏览器打开」，把下载目录设为输出文件夹，\n"
                         "软件仍会自动归集改名并切到下一个文件。")
            lab.setAlignment(Qt.AlignCenter); lab.setWordWrap(True)
            lab.setStyleSheet("color:#666;font-size:12px;")
            lay.addWidget(lab)
            return
        try:
            PROFILE_PATH.mkdir(parents=True, exist_ok=True)
            self.profile = QWebEngineProfile("quark-scan", self)
            self.profile.setPersistentStoragePath(str(PROFILE_PATH))
            try:
                self.profile.setPersistentCookiesPolicy(
                    QWebEngineProfile.PersistentCookiesPolicy.ForcePersistentCookies)
            except Exception:
                try:
                    self.profile.setPersistentCookiesPolicy(
                        QWebEngineProfile.ForcePersistentCookies)
                except Exception:
                    pass
            self.profile.downloadRequested.connect(self._on_download)

            page = QWebEnginePage(self.profile, self)
            self.web = QWebEngineView(self)
            self.web.setPage(page)
            lay = QVBoxLayout(self.web_frame)
            lay.setContentsMargins(0, 0, 0, 0)
            lay.addWidget(self.web)
            self.web.urlChanged.connect(lambda u: self.ed_addr.setText(u.toString()))
            self.web.loadFinished.connect(self._on_load_finished)
            self.web.setUrl(QUrl(self.cfg.get("web_url") or HOME_URL))
            self._log("内嵌网页已就绪，登录态保存于本机 webprofile（扫码一次即可）")
        except Exception as e:
            self._log(f"⚠ 内嵌网页初始化失败：{e}；请用「用系统浏览器打开」降级")

    def _on_load_finished(self, ok):
        if not ok:
            self._log("⚠ 网页加载失败，检查网络或点「刷新」")
            return
        url = self.web.url().toString() if self.web else ""
        if "scan.quark.cn" in url or "quark.cn" in url:
            self.lbl_login.setText("● 网页已打开（登录状态以网页右上角为准）")
            self.lbl_login.setStyleSheet("color:#1a7f37;font-weight:bold;")
        self.ed_addr.setText(url)

    def do_login(self):
        url = self.ed_url.text().strip() or HOME_URL
        if self.web:
            self.web.setUrl(QUrl(url))
            self._log(f"已在内嵌网页打开：{url}")
        else:
            webbrowser.open(url)
            self._log(f"已在系统浏览器打开：{url}（系统浏览器登录态与内嵌页独立）")
        self._log("👉 请在网页里用「手机号验证码」或「夸克 App 扫码」登录你的会员账号。")
        self._log("   软件不读取密码 / 验证码 / Cookie；登录态由网页组件自己保存。")

    def open_external(self):
        webbrowser.open(self.ed_url.text().strip() or HOME_URL)
        self._log("已用系统浏览器打开（排障用；其登录态不共享给内嵌页）")

    def open_page(self):
        u = self.ed_url.text().strip() or HOME_URL
        if self.web:
            self.web.setUrl(QUrl(u)); self._log(f"打开页面：{u}")
        else:
            webbrowser.open(u); self._log(f"系统浏览器打开：{u}")

    def refresh_page(self):
        if self.web:
            self.web.reload(); self._log("已刷新内嵌网页")
        else:
            self._log("内嵌网页不可用")

    def clear_profile(self):
        r = QMessageBox.question(self, "清除登录态",
                                 "将删除本软件保存的网页登录态，下次需要重新扫码登录。\n继续？",
                                 QMessageBox.Yes | QMessageBox.No)
        if r != QMessageBox.Yes:
            return
        try:
            if PROFILE_PATH.exists():
                shutil.rmtree(PROFILE_PATH, ignore_errors=True)
            self._log("✔ 已清除登录态，重启软件后需重新扫码（建议先关闭软件再重开）")
        except Exception as e:
            self._log(f"清除失败：{e}")

    # ---------------- 配置 ----------------
    def _cfg_to_ui(self):
        self.ed_out.setText(str(self.cfg.get("out_dir", "")))
        self.ed_url.setText(str(self.cfg.get("web_url", HOME_URL)))
        self.ed_tpl.setText(str(self.cfg.get("name_template", "{stem}_已处理")))
        self.chk_auto_next.setChecked(bool(self.cfg.get("auto_next", True)))
        self.chk_auto_clip.setChecked(bool(self.cfg.get("auto_clip", True)))
        self.chk_poll.setChecked(bool(self.cfg.get("poll_fallback", True)))

    def _ui_to_cfg(self):
        self.cfg.update(
            out_dir=self.ed_out.text().strip(),
            web_url=self.ed_url.text().strip() or HOME_URL,
            name_template=self.ed_tpl.text().strip() or "{stem}_已处理",
            auto_next=self.chk_auto_next.isChecked(),
            auto_clip=self.chk_auto_clip.isChecked(),
            poll_fallback=self.chk_poll.isChecked(),
        )
        save_config(self.cfg)

    def _save_cfg(self):
        self._ui_to_cfg()
        self._log("✔ 设置已保存（%APPDATA%\\QuarkBatch\\config.json）")

    def _on_out(self, t):
        self.cfg["out_dir"] = t.strip()

    def out_path(self) -> Path | None:
        t = self.ed_out.text().strip()
        return Path(t) if t else None

    # ---------------- 队列 ----------------
    def add_files(self):
        fs, _ = QFileDialog.getOpenFileNames(self, "选择 PDF", "", "PDF 文件 (*.pdf)")
        self._append([Path(x) for x in fs])

    def add_dir(self):
        d = QFileDialog.getExistingDirectory(self, "选择装有 PDF 的文件夹")
        if not d:
            return
        try:
            with os.scandir(d) as it:
                ps = [Path(e.path) for e in it
                      if e.is_file() and e.name.lower().endswith(".pdf")]
        except Exception:
            ps = [p for p in Path(d).iterdir() if p.suffix.lower() == ".pdf"]
        self._append(ps)

    def dragEnterEvent(self, e):
        if e.mimeData().hasUrls():
            e.acceptProposedAction()

    def dropEvent(self, e):
        ps: list[Path] = []
        for u in e.mimeData().urls():
            p = Path(u.toLocalFile())
            if p.is_dir():
                try:
                    ps += [Path(x.path) for x in os.scandir(p)
                           if x.is_file() and x.name.lower().endswith(".pdf")]
                except Exception:
                    pass
            elif p.suffix.lower() == ".pdf":
                ps.append(p)
        self._append(ps)

    def _append(self, paths: list[Path]):
        exist = {str(j.path.resolve()) for j in self.jobs}
        n = 0
        for p in paths:
            if p.suffix.lower() != ".pdf" or str(p.resolve()) in exist:
                continue
            self.jobs.append(Job(path=p)); exist.add(str(p.resolve())); n += 1
        if n:
            self._refresh_table()
            self._log(f"已加入 {n} 个 PDF，共 {len(self.jobs)} 个")

    def remove_selected(self):
        rows = sorted({i.row() for i in self.tbl.selectedIndexes()}, reverse=True)
        if not rows:
            return
        for r in rows:
            if 0 <= r < len(self.jobs):
                self.jobs.pop(r)
                if self.cur == r:
                    self.cur = -1
                elif self.cur > r:
                    self.cur -= 1
        self._refresh_table()
        self._log(f"已移除 {len(rows)} 项")

    def clear_jobs(self):
        self.jobs.clear(); self.cur = -1; self.running = False
        self._refresh_table(); self._set_running(False)
        self._log("已清空队列")

    def _refresh_table(self):
        self.tbl.setRowCount(len(self.jobs))
        for i, j in enumerate(self.jobs):
            vals = [str(i + 1), j.path.name, str(j.pages) if j.pages else "—",
                    j.size, j.status]
            for c, x in enumerate(vals):
                it = QTableWidgetItem(x)
                if i == self.cur:
                    it.setBackground(QColor("#dbeafe"))
                    f = it.font(); f.setBold(True); it.setFont(f)
                if c == 4:
                    if j.status == "已完成":
                        it.setForeground(QColor("#1a7f37"))
                    elif j.status == "处理中":
                        it.setForeground(QColor("#1a73e8"))
                    elif j.status.startswith("跳过") or j.status.startswith("失败"):
                        it.setForeground(QColor("#c62828"))
                if c == 0:
                    it.setTextAlignment(Qt.AlignCenter)
                self.tbl.setItem(i, c, it)
        done = sum(1 for j in self.jobs if j.status in ("已完成", "跳过"))
        self.lbl_stat.setText(f"队列 {len(self.jobs)} 个 ｜ 已处理 {done} ｜ "
                              f"当前 {self.cur + 1 if self.cur >= 0 else '—'}")

    # ---------------- 输出 / 辅助动作 ----------------
    def pick_out(self):
        d = QFileDialog.getExistingDirectory(self, "选择成品 PDF 输出文件夹")
        if d:
            self.ed_out.setText(d); self._ui_to_cfg()
            self._snapshot_downloads()
            self._log(f"输出文件夹：{d}")

    def open_out(self):
        p = self.out_path()
        if not p:
            self._log("还没选输出文件夹"); return
        try:
            p.mkdir(parents=True, exist_ok=True)
            if sys.platform == "win32":
                os.startfile(str(p))  # noqa
            else:
                QDesktopServices.openUrl(QUrl.fromLocalFile(str(p)))
        except Exception as e:
            self._log(f"打开失败：{e}")

    def _windows_select(self, p: Path):
        if sys.platform != "win32":
            self._log(f"（非 Windows，跳过资源管理器选中）{p.name}")
            return
        try:
            subprocess.Popen(f'explorer /select,"{p}"', shell=True)
            self._log(f"已在资源管理器选中：{p.name}")
        except Exception as e:
            self._log(f"资源管理器选中失败：{e}")

    def copy_current(self, auto: bool = False):
        if not (0 <= self.cur < len(self.jobs)):
            self._log("当前没有文件"); return
        p = self.jobs[self.cur].path
        md = QMimeData()
        md.setUrls([QUrl.fromLocalFile(str(p))])
        QApplication.clipboard().setMimeData(md)
        self._log(f"已把文件放进剪贴板：{p.name}（在右侧网页 Ctrl+V 粘贴）")
        if auto and self.chk_auto_clip.isChecked():
            self._windows_select(p)

    def locate_current(self):
        if not (0 <= self.cur < len(self.jobs)):
            self._log("当前没有文件"); return
        self._windows_select(self.jobs[self.cur].path)

    # ---------------- 批量状态机 ----------------
    def _set_running(self, on: bool):
        self.running = on
        self.btn_start.setEnabled(not on)
        self.btn_stop.setEnabled(on)

    def start_batch(self):
        if not self.jobs:
            self._log("❌ 先导入 PDF"); return
        if not self.ed_out.text().strip():
            self._log("❌ 先选「输出文件夹」"); return
        self._ui_to_cfg()
        out = self.out_path()
        try:
            out.mkdir(parents=True, exist_ok=True)
        except Exception as e:
            self._log(f"❌ 输出文件夹不可用：{e}"); return
        self._snapshot_downloads()
        self._batch_started = time.time()
        for j in self.jobs:
            if j.status != "已完成":
                j.status = "待处理"
                j.out_name = ""; j.note = ""
        self.cur = -1
        self._set_running(True)
        self._log("—— 开始批量辅助 ——")
        self.next_job(prepare=True)

    def stop_batch(self):
        self._set_running(False)
        self._log("■ 已停止（队列与已完成文件保留）")

    def next_job(self, prepare: bool = True):
        if not self.running and prepare:
            self._log("点「开始」后才进入批量流程"); return
        nxt = -1
        for i in range(self.cur + 1, len(self.jobs)):
            if self.jobs[i].status == "待处理":
                nxt = i; break
        if nxt < 0:
            self.cur = -1
            self._refresh_table()
            self._log("✔ 队列全部处理完，点「打开输出文件夹」看成品")
            self._set_running(False)
            return
        self.cur = nxt
        self._collected_for_current = False
        j = self.jobs[nxt]
        j.status = "处理中"; j.out_name = ""; j.note = ""
        self._refresh_table()
        self._log(f"[{nxt + 1}/{len(self.jobs)}] 在右侧网页 Ctrl+V 粘贴 → 点去水印 / 去手写 → 点下载 PDF。")
        self._log("下载会自动落到输出文件夹并改名。")
        if prepare:
            self.copy_current(auto=True)

    def skip_current(self):
        if not (0 <= self.cur < len(self.jobs)):
            self._log("当前没有文件"); return
        self.jobs[self.cur].status = "跳过"
        self.jobs[self.cur].note = "手动跳过"
        self._log(f"已跳过：{self.jobs[self.cur].path.name}")
        if self.chk_auto_next.isChecked() and self.running:
            self.next_job(prepare=True)
        else:
            self._refresh_table()

    def mark_done(self):
        """网页已下载但软件没捕获到时，手动标记当前完成"""
        if not (0 <= self.cur < len(self.jobs)):
            self._log("当前没有文件"); return
        j = self.jobs[self.cur]
        j.status = "已完成"; j.note = "手动标记完成"
        self._log(f"已标记完成：{j.path.name}")
        if self.chk_auto_next.isChecked() and self.running:
            self.next_job(prepare=True)
        else:
            self._refresh_table()

    # ---------------- 下载归集 ----------------
    def _target_name(self, j: Job) -> str:
        tpl = self.ed_tpl.text().strip() or "{stem}_已处理"
        try:
            base = clean_name(tpl.format(stem=clean_name(j.path.stem)))
        except Exception:
            base = clean_name(j.path.stem) + "_已处理"
        out = self.out_path()
        cand = base + ".pdf"
        k = 1
        while out and (out / cand).exists():
            cand = f"{base}（{k}）.pdf"; k += 1
        return cand

    def _on_download(self, dl):
        """网页点「下载 PDF」后由组件回调：直接落到输出目录并按队列命名。"""
        out = self.out_path()
        if not (0 <= self.cur < len(self.jobs)):
            name = clean_name(dl.suggestedFileName() or "扫描件.pdf")
        else:
            name = self._target_name(self.jobs[self.cur])
        try:
            if out:
                dl.setDownloadDirectory(str(out))
            dl.setDownloadFileName(name)
            dl.accept()
            self._log(f"已接管下载 → {name}（等待写入完成）")
        except Exception as e:
            self._log(f"接管下载失败：{e}")
            try:
                dl.accept()
            except Exception:
                pass
            return
        holder = {"dl": dl, "name": name}
        for sig in ("isFinishedChanged", "stateChanged"):
            try:
                getattr(dl, sig).connect(lambda *a, h=holder: self._check_download(h))
                break
            except Exception:
                continue
        QTimer.singleShot(600, lambda h=holder: self._check_download(h))
        QTimer.singleShot(2500, lambda h=holder: self._check_download(h))

    def _check_download(self, h):
        dl, name = h["dl"], h["name"]
        if not self.running or not (0 <= self.cur < len(self.jobs)):
            return
        if self._collected_for_current:
            return
        finished = False
        try:
            finished = bool(dl.isFinished())
        except Exception:
            pass
        if not finished and HAS_WEB:
            try:
                st = dl.state()
                comp = QWebEngineDownloadRequest.DownloadState.DownloadCompleted
                finished = (st == comp)
            except Exception:
                try:
                    finished = (dl.state() == QWebEngineDownloadRequest.DownloadCompleted)
                except Exception:
                    pass
        if not finished:
            return
        out = self.out_path()
        if out and not (out / name).exists():
            self._log(f"⚠ 下载完成但没在输出目录找到 {name}，可点「标记完成」或用兼容模式")
            return
        self._collect(name, "网页下载自动归集")

    def _collect(self, name: str, how: str):
        if not (0 <= self.cur < len(self.jobs)) or self._collected_for_current:
            return
        self._collected_for_current = True
        j = self.jobs[self.cur]
        j.status = "已完成"; j.out_name = name; j.note = how
        self._refresh_table()
        self._log(f"已归集 → {name}")
        self._seen_downloads.add(name)
        if self.chk_auto_next.isChecked() and self.running:
            self.next_job(prepare=True)

    def _snapshot_downloads(self):
        seen = set()
        for d in self._watch_dirs():
            try:
                for p in d.iterdir():
                    if p.is_file() and p.suffix.lower() == ".pdf":
                        seen.add(p.name)
            except Exception:
                pass
        self._seen_downloads = seen

    def _watch_dirs(self) -> list[Path]:
        ds = []
        o = self.out_path()
        if o:
            ds.append(o)
        dl = Path.home() / "Downloads"
        if dl.exists() and dl not in ds:
            ds.append(dl)
        return ds

    def _poll_downloads(self):
        """兼容模式：内嵌页下载未被接管（或走了系统浏览器）时，轮询下载目录补归集。"""
        if not self.running or not self.chk_poll.isChecked():
            return
        if not (0 <= self.cur < len(self.jobs)) or self._collected_for_current:
            return
        out = self.out_path()
        if not out:
            return
        try:
            out.mkdir(parents=True, exist_ok=True)
        except Exception:
            return
        cands: list[Path] = []
        for d in self._watch_dirs():
            try:
                for p in d.iterdir():
                    if not p.is_file() or p.suffix.lower() != ".pdf":
                        continue
                    if p.name in self._seen_downloads:
                        continue
                    if p.stat().st_mtime < self._batch_started - 2:
                        continue
                    if p.stat().st_size < 1024:
                        continue
                    cands.append(p)
            except Exception:
                pass
        if not cands:
            return
        p = max(cands, key=lambda x: x.stat().st_mtime)
        name = self._target_name(self.jobs[self.cur])
        try:
            dest = out / name
            if p.resolve() != dest.resolve():
                # 系统「下载」文件夹常与输出盘不同，rename 会 EXDEV / EPERM；
                # copy2 的 copystat(utime) 在部分受限目录也会 EPERM，
                # 所以用纯字节复制 copyfile，再尽力删源，删不掉就加入 seen 防重复。
                shutil.copyfile(str(p), str(dest))
                try:
                    p.unlink()
                except Exception:
                    pass
            self._seen_downloads.add(p.name)
            self._log(f"（兼容模式）检测到新下载：{p.name}")
            self._collect(name, "下载目录轮询归集")
        except Exception as e:
            self._log(f"（兼容模式）归集失败：{e}")

    # ---------------- 杂项 ----------------
    def _log(self, s: str):
        ts = datetime.now().strftime("%H:%M:%S")
        self.log.append(f"[{ts}] {s}")
        self.statusBar().showMessage(s[:120], 6000)

    def _refresh_crc(self):
        p = os.environ.get("FAR_SELF", "")
        if not p or not Path(p).exists():
            self.lbl_crc.setText(f"CRC32：源码模式 · {APP_NAME} {VERSION}")
            return
        try:
            c = 0
            with open(p, "rb") as f:
                while True:
                    b = f.read(1 << 20)
                    if not b:
                        break
                    c = zlib.crc32(b, c)
            self.lbl_crc.setText(f"整文件 CRC32：{c & 0xffffffff:08X}  ·  {APP_NAME} {VERSION}")
        except Exception as e:
            self.lbl_crc.setText(f"CRC32：{type(e).__name__}")


def main():
    if "--selftest" in sys.argv:
        os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")
        app = QApplication(sys.argv)
        w = MainWindow()
        assert w.tbl.columnCount() == 5
        assert w.btn_login.text() == "登录 / 打开官网"
        assert w.chk_auto_next.isChecked() and w.chk_auto_clip.isChecked()
        print("selftest: MainWindow 构建成功，模块导入正常")
        return 0
    app = QApplication(sys.argv)
    w = MainWindow()
    w.show()
    return app.exec()


if __name__ == "__main__":
    sys.exit(main())
