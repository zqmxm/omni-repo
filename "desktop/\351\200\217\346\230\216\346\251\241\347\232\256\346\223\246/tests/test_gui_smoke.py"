"""界面冒烟测试：验证窗口能建起来、能打开图片。需 offscreen 平台。"""
from __future__ import annotations

import os
import sys
import tempfile
import unittest
from pathlib import Path

SRC = Path(__file__).resolve().parents[1] / "src"
sys.path.insert(0, str(SRC))


class GuiSmokeTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")
        from PySide6.QtWidgets import QApplication

        cls.app = QApplication.instance() or QApplication([])

    def test_window_can_open_image(self) -> None:
        from PySide6.QtGui import QColor, QImage

        from main import MainWindow

        window = MainWindow()
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "sample.png"
            image = QImage(30, 30, QImage.Format.Format_ARGB32)
            image.fill(QColor("#336699"))
            self.assertTrue(image.save(str(path), "PNG"))
            window.open_image(str(path))
            self.assertFalse(window.canvas.image.isNull())
            self.assertEqual(window.canvas.image.width(), 30)
        window.close()

    def test_brush_supports_one_pixel(self) -> None:
        from main import MainWindow

        window = MainWindow()
        self.assertEqual(window._size_for_slider(window.slider.minimum()), 1)
        for size in (1, 2, 3, 5, 10, 48, 300):
            self.assertEqual(window._size_for_slider(window._slider_for_size(size)), size)
        window._set_brush_size(1)
        window.brush_smaller()
        self.assertEqual(window.canvas.brush_size, 1)  # 下限不能再小
        window.brush_bigger()
        self.assertEqual(window.canvas.brush_size, 2)
        window.close()


if __name__ == "__main__":
    unittest.main()
