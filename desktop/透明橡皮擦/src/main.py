"""透明橡皮擦 —— 像抠图一样擦掉图片中不要的像素，擦掉处为透明而非白色。

运行：python desktop/透明橡皮擦/src/main.py
打包：python desktop/透明橡皮擦/build.py
"""
from __future__ import annotations

import math
import sys
from pathlib import Path

from PySide6.QtCore import QPointF, QRectF, Qt, Signal
from PySide6.QtGui import (
    QAction,
    QBrush,
    QColor,
    QImage,
    QKeySequence,
    QPainter,
    QPen,
    QPixmap,
)
from PySide6.QtWidgets import (
    QApplication,
    QFileDialog,
    QLabel,
    QMainWindow,
    QMessageBox,
    QSlider,
    QStatusBar,
    QToolBar,
    QWidget,
)

from eraser_core import erase_circle, erase_line, save_png, to_argb32

APP_TITLE = "透明橡皮擦"
IMAGE_FILTER = "图片文件 (*.png *.jpg *.jpeg *.bmp *.webp);;所有文件 (*.*)"
MIN_BRUSH = 1
MAX_BRUSH = 300
SLIDER_STEPS = 1000


class Canvas(QWidget):
    """显示图片、棋盘格透明背景，并处理橡皮擦笔迹。"""

    stroke_started = Signal()

    def __init__(self, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self.image = QImage()
        self.zoom = 1.0
        self.brush_size = 48
        self.feather = 0.15
        self._drawing = False
        self._last: QPointF | None = None
        self._cursor: QPointF | None = None
        self._checker = self._make_checker_brush()
        self.setMouseTracking(True)
        self.setCursor(Qt.CursorShape.BlankCursor)
        self.setMinimumSize(320, 240)
        self.setStyleSheet("background: #f4f5f7;")

    @staticmethod
    def _make_checker_brush() -> QBrush:
        tile = QPixmap(24, 24)
        painter = QPainter(tile)
        painter.fillRect(tile.rect(), QColor("#ffffff"))
        painter.fillRect(0, 0, 12, 12, QColor("#dcdfe5"))
        painter.fillRect(12, 12, 12, 12, QColor("#dcdfe5"))
        painter.end()
        return QBrush(tile)

    def image_rect(self) -> QRectF:
        w = self.image.width() * self.zoom
        h = self.image.height() * self.zoom
        return QRectF((self.width() - w) / 2, (self.height() - h) / 2, w, h)

    def _to_image_pos(self, pos) -> QPointF:
        rect = self.image_rect()
        return QPointF((pos.x() - rect.x()) / self.zoom, (pos.y() - rect.y()) / self.zoom)

    def set_image(self, image: QImage) -> None:
        self.image = image
        self.zoom = 1.0
        self.update()

    def paintEvent(self, _event) -> None:
        painter = QPainter(self)
        if self.image.isNull():
            painter.setPen(QColor("#6b7280"))
            painter.drawText(
                self.rect(),
                Qt.AlignmentFlag.AlignCenter,
                "打开一张图片开始擦除\n（也可以把图片直接拖进来）\n\n擦掉的地方是透明的，不是白色",
            )
            painter.end()
            return

        rect = self.image_rect()
        painter.fillRect(rect, self._checker)
        painter.drawImage(rect, self.image)
        painter.setPen(QPen(QColor("#aeb4bf"), 1))
        painter.drawRect(rect)

        if self._cursor is not None:
            radius = self.brush_size * self.zoom / 2
            pen = QPen(QColor("#e5484d"), 1.5)
            pen.setStyle(Qt.PenStyle.DashLine)
            painter.setPen(pen)
            painter.setBrush(Qt.BrushStyle.NoBrush)
            painter.drawEllipse(self._cursor, radius, radius)
        painter.end()

    def mousePressEvent(self, event) -> None:
        if event.button() != Qt.MouseButton.LeftButton or self.image.isNull():
            return
        pos = self._to_image_pos(event.position())
        self.stroke_started.emit()
        erase_circle(self.image, pos.x(), pos.y(), self.brush_size, self.feather)
        self._drawing = True
        self._last = pos
        self.update()

    def mouseMoveEvent(self, event) -> None:
        pos = event.position()
        self._cursor = QPointF(pos.x(), pos.y())
        if self._drawing and self._last is not None and not self.image.isNull():
            current = self._to_image_pos(pos)
            erase_line(
                self.image,
                self._last.x(),
                self._last.y(),
                current.x(),
                current.y(),
                self.brush_size,
                self.feather,
            )
            self._last = current
        self.update()

    def mouseLeaveEvent(self, _event) -> None:
        self._cursor = None
        self.update()

    def mouseReleaseEvent(self, event) -> None:
        if event.button() == Qt.MouseButton.LeftButton:
            self._drawing = False
            self._last = None

    def wheelEvent(self, event) -> None:
        if self.image.isNull():
            return
        factor = 1.15 if event.angleDelta().y() > 0 else 1 / 1.15
        self.zoom = max(0.05, min(12.0, self.zoom * factor))
        self.update()
        self.window().update_status()


class MainWindow(QMainWindow):
    def __init__(self) -> None:
        super().__init__()
        self.setWindowTitle(APP_TITLE)
        self.resize(1040, 720)
        self._undo: list[QImage] = []
        self._redo: list[QImage] = []
        self._current_path: Path | None = None

        self.canvas = Canvas(self)
        self.setCentralWidget(self.canvas)
        self.canvas.stroke_started.connect(self._begin_stroke)

        self._build_actions()
        self._build_toolbar()
        self._build_statusbar()
        self.setAcceptDrops(True)
        self._update_actions()

    # ---------- 界面 ----------
    def _build_actions(self) -> None:
        def action(text: str, shortcut: str, callback) -> QAction:
            item = QAction(text, self)
            item.setShortcut(QKeySequence(shortcut))
            item.triggered.connect(callback)
            return item

        self.act_open = action("打开图片", "Ctrl+O", self.open_image_dialog)
        self.act_save = action("保存为 PNG", "Ctrl+S", self.save_image)
        self.act_undo = action("撤销", "Ctrl+Z", self.undo)
        self.act_redo = action("重做", "Ctrl+Shift+Z", self.redo)
        self.act_fit = action("适应窗口", "0", self.fit_to_window)
        self.act_zoom_in = action("放大", "Ctrl+=", self.zoom_in)
        self.act_zoom_out = action("缩小", "Ctrl+-", self.zoom_out)
        self.act_exit = action("退出", "Ctrl+Q", self.close)
        self.act_erase_all = action("全部擦除", "", self.erase_all)

        menu = self.menuBar().addMenu("文件")
        menu.addAction(self.act_open)
        menu.addAction(self.act_save)
        menu.addSeparator()
        menu.addAction(self.act_exit)
        menu = self.menuBar().addMenu("编辑")
        menu.addAction(self.act_undo)
        menu.addAction(self.act_redo)
        menu.addAction(self.act_erase_all)
        menu = self.menuBar().addMenu("视图")
        menu.addAction(self.act_fit)
        menu.addAction(self.act_zoom_in)
        menu.addAction(self.act_zoom_out)

    def _build_toolbar(self) -> None:
        toolbar = QToolBar("工具栏", self)
        toolbar.setMovable(False)
        self.addToolBar(toolbar)
        for act in (self.act_open, self.act_save, self.act_undo, self.act_redo):
            toolbar.addAction(act)
        toolbar.addSeparator()
        toolbar.addWidget(QLabel("笔刷大小 "))
        # 滑杆位置 0~1000 按指数映射到 1~300 px，小尺寸段也能精确选中
        self.slider = QSlider(Qt.Orientation.Horizontal, self)
        self.slider.setRange(0, SLIDER_STEPS)
        self.slider.setValue(self._slider_for_size(self.canvas.brush_size))
        self.slider.setFixedWidth(170)
        self.slider.valueChanged.connect(self._on_brush_changed)
        toolbar.addWidget(self.slider)
        self.brush_label = QLabel(f"{self.canvas.brush_size} px")
        toolbar.addWidget(self.brush_label)

        self.act_brush_smaller = QAction("−", self)
        self.act_brush_smaller.setToolTip("笔刷减小 1 px（快捷键 [）")
        self.act_brush_smaller.setShortcut(QKeySequence("["))
        self.act_brush_smaller.triggered.connect(self.brush_smaller)
        self.act_brush_bigger = QAction("+", self)
        self.act_brush_bigger.setToolTip("笔刷增大 1 px（快捷键 ]）")
        self.act_brush_bigger.setShortcut(QKeySequence("]"))
        self.act_brush_bigger.triggered.connect(self.brush_bigger)
        toolbar.addAction(self.act_brush_smaller)
        toolbar.addAction(self.act_brush_bigger)
        toolbar.addSeparator()
        for act in (self.act_zoom_out, self.act_fit, self.act_zoom_in):
            toolbar.addAction(act)

    def _build_statusbar(self) -> None:
        self.status = QStatusBar(self)
        self.setStatusBar(self.status)
        self.hint = QLabel("擦掉的区域会保存为透明（仅 PNG 保留透明）")
        self.info = QLabel()
        self.status.addWidget(self.hint, 1)
        self.status.addPermanentWidget(self.info)
        self.update_status()

    def update_status(self) -> None:
        if self.canvas.image.isNull():
            self.info.setText("尚未打开图片")
        else:
            img = self.canvas.image
            self.info.setText(
                f"{img.width()} × {img.height()}  |  缩放 {self.canvas.zoom * 100:.0f}%"
                f"  |  笔刷 {self.canvas.brush_size} px"
            )
        self._update_actions()

    def _update_actions(self) -> None:
        has_image = not self.canvas.image.isNull()
        self.act_save.setEnabled(has_image)
        self.act_fit.setEnabled(has_image)
        self.act_zoom_in.setEnabled(has_image)
        self.act_zoom_out.setEnabled(has_image)
        self.act_erase_all.setEnabled(has_image)
        self.act_undo.setEnabled(bool(self._undo))
        self.act_redo.setEnabled(bool(self._redo))

    @staticmethod
    def _size_for_slider(value: int) -> int:
        """滑杆位置 → 笔刷像素；指数映射让 1~10 px 区间占据更多滑杆行程。"""
        ratio = max(0, min(SLIDER_STEPS, value)) / SLIDER_STEPS
        return max(MIN_BRUSH, min(MAX_BRUSH, round(MIN_BRUSH * (MAX_BRUSH / MIN_BRUSH) ** ratio)))

    @staticmethod
    def _slider_for_size(size: int) -> int:
        """笔刷像素 → 滑杆位置。"""
        size = max(MIN_BRUSH, min(MAX_BRUSH, size))
        return round(SLIDER_STEPS * math.log(size / MIN_BRUSH) / math.log(MAX_BRUSH / MIN_BRUSH))

    def _set_brush_size(self, size: int) -> None:
        size = max(MIN_BRUSH, min(MAX_BRUSH, size))
        self.canvas.brush_size = size
        self.slider.blockSignals(True)
        self.slider.setValue(self._slider_for_size(size))
        self.slider.blockSignals(False)
        self.brush_label.setText(f"{size} px")
        self.canvas.update()
        self.update_status()

    def _on_brush_changed(self, value: int) -> None:
        self._set_brush_size(self._size_for_slider(value))

    def brush_smaller(self) -> None:
        self._set_brush_size(self.canvas.brush_size - 1)

    def brush_bigger(self) -> None:
        self._set_brush_size(self.canvas.brush_size + 1)

    # ---------- 图像操作 ----------
    def open_image(self, path: str) -> None:
        try:
            image = to_argb32(QImage(path))
        except (ValueError, OSError) as exc:
            QMessageBox.warning(self, APP_TITLE, f"打不开这张图片：\n{exc}")
            return
        self.canvas.set_image(image)
        self._current_path = Path(path)
        self._undo.clear()
        self._redo.clear()
        self.fit_to_window()
        self.hint.setText(f"已打开：{Path(path).name} —— 按住鼠标左键擦除")
        self.update_status()

    def open_image_dialog(self) -> None:
        path, _ = QFileDialog.getOpenFileName(self, "打开图片", "", IMAGE_FILTER)
        if path:
            self.open_image(path)

    def save_image(self) -> None:
        if self.canvas.image.isNull():
            return
        default = "透明橡皮擦.png"
        if self._current_path is not None:
            default = f"{self._current_path.stem}_已擦除.png"
        path, _ = QFileDialog.getSaveFileName(
            self, "保存为 PNG（保留透明）", default, "PNG 图片 (*.png)"
        )
        if not path:
            return
        if not path.lower().endswith(".png"):
            path += ".png"
        if save_png(self.canvas.image, path):
            self.hint.setText(f"已保存：{path}（擦掉的地方是透明的）")
        else:
            QMessageBox.warning(self, APP_TITLE, f"保存失败：{path}")

    def _begin_stroke(self) -> None:
        self._undo.append(self.canvas.image.copy())
        if len(self._undo) > 50:
            self._undo.pop(0)
        self._redo.clear()
        self._update_actions()

    def undo(self) -> None:
        if not self._undo:
            return
        self._redo.append(self.canvas.image.copy())
        self.canvas.image = self._undo.pop()
        self.canvas.update()
        self.update_status()

    def redo(self) -> None:
        if not self._redo:
            return
        self._undo.append(self.canvas.image.copy())
        self.canvas.image = self._redo.pop()
        self.canvas.update()
        self.update_status()

    def erase_all(self) -> None:
        if self.canvas.image.isNull():
            return
        answer = QMessageBox.question(
            self, APP_TITLE, "要把整张图片全部擦成透明吗？", QMessageBox.StandardButton.Yes | QMessageBox.StandardButton.No
        )
        if answer != QMessageBox.StandardButton.Yes:
            return
        self._begin_stroke()
        self.canvas.image.fill(0)
        self.canvas.update()

    def fit_to_window(self) -> None:
        if self.canvas.image.isNull():
            return
        margin = 48
        w = max(1, self.canvas.width() - margin)
        h = max(1, self.canvas.height() - margin)
        self.canvas.zoom = max(
            0.05,
            min(12.0, min(w / self.canvas.image.width(), h / self.canvas.image.height())),
        )
        self.canvas.update()
        self.update_status()

    def zoom_in(self) -> None:
        self.canvas.zoom = min(12.0, self.canvas.zoom * 1.25)
        self.canvas.update()
        self.update_status()

    def zoom_out(self) -> None:
        self.canvas.zoom = max(0.05, self.canvas.zoom / 1.25)
        self.canvas.update()
        self.update_status()

    # ---------- 拖拽打开 ----------
    def dragEnterEvent(self, event) -> None:
        if event.mimeData().hasUrls():
            event.acceptProposedAction()

    def dropEvent(self, event) -> None:
        for url in event.mimeData().urls():
            path = url.toLocalFile()
            if path:
                self.open_image(path)
                break


def main() -> int:
    app = QApplication(sys.argv)
    window = MainWindow()
    window.show()
    return app.exec()


if __name__ == "__main__":
    raise SystemExit(main())
