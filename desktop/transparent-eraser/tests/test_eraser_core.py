"""透明橡皮擦核心逻辑单测：确认擦掉的是 alpha 而不是白色。"""
from __future__ import annotations

import sys
import tempfile
import unittest
from pathlib import Path

from PySide6.QtGui import QColor, QImage

SRC = Path(__file__).resolve().parents[1] / "src"
sys.path.insert(0, str(SRC))

from eraser_core import alpha_at, erase_circle, erase_line, save_png, to_argb32  # noqa: E402


def opaque_image(size: int = 40, color: str = "#ff0000") -> QImage:
    image = QImage(size, size, QImage.Format.Format_ARGB32)
    image.fill(QColor(color))
    return image


class EraserCoreTest(unittest.TestCase):
    def test_erase_center_is_transparent_not_white(self) -> None:
        image = opaque_image()
        erase_circle(image, 20, 20, 6, feather=0)
        self.assertEqual(alpha_at(image, 20, 20), 0)
        self.assertEqual(alpha_at(image, 0, 0), 255)
        # RGB 通道没有被涂白，只是 alpha 归零
        self.assertEqual(image.pixelColor(20, 20).red(), 255)
        self.assertEqual(image.pixelColor(20, 20).green(), 0)

    def test_rgb_image_becomes_erasable(self) -> None:
        raw = QImage(20, 20, QImage.Format.Format_RGB32)
        raw.fill(QColor("#112233"))
        image = to_argb32(raw)
        erase_circle(image, 10, 10, 4, feather=0)
        self.assertEqual(alpha_at(image, 10, 10), 0)
        self.assertEqual(alpha_at(image, 0, 0), 255)

    def test_soft_edge_keeps_partial_alpha(self) -> None:
        image = opaque_image()
        erase_circle(image, 20, 20, 10, feather=0.5)
        self.assertEqual(alpha_at(image, 20, 20), 0)
        edge = alpha_at(image, 27, 20)
        self.assertGreater(edge, 0)
        self.assertLess(edge, 255)
        self.assertEqual(alpha_at(image, 39, 20), 255)

    def test_fast_drag_leaves_no_gaps(self) -> None:
        image = opaque_image()
        erase_line(image, 5, 20, 35, 20, 2, feather=0)
        for x in range(6, 35):
            self.assertEqual(alpha_at(image, x, 20), 0, f"x={x} 未被擦除")

    def test_saved_png_keeps_transparency(self) -> None:
        image = opaque_image()
        erase_circle(image, 20, 20, 5, feather=0)
        with tempfile.TemporaryDirectory() as folder:
            target = Path(folder) / "cutout"  # 故意不带后缀
            self.assertTrue(save_png(image, target))
            saved = QImage(str(target) + ".png")
            self.assertFalse(saved.isNull())
            self.assertEqual(alpha_at(saved, 20, 20), 0)
            self.assertEqual(alpha_at(saved, 0, 0), 255)


if __name__ == "__main__":
    unittest.main()
