"""透明橡皮擦的图像处理逻辑（不依赖界面，方便单测）。

核心约定：擦除 = 把像素的 alpha 通道置为 0，而不是涂成白色。
"""
from __future__ import annotations

import math
import struct
from pathlib import Path

from PySide6.QtGui import QImage

#: ARGB32 内存布局为小端 BGRA，一个像素 4 字节
_PIXEL = struct.Struct("<I")


def to_argb32(image: QImage) -> QImage:
    """转换成带 alpha 通道的 32 位图像；JPG 等无透明通道的图也能被擦除。"""
    if image.isNull():
        raise ValueError("图片为空或格式不受支持")
    if image.format() == QImage.Format.Format_ARGB32:
        return image
    return image.convertToFormat(QImage.Format.Format_ARGB32)


def erase_circle(
    image: QImage,
    cx: float,
    cy: float,
    radius: float,
    feather: float = 0.15,
) -> None:
    """原地擦除以 (cx, cy) 为圆心、radius 为半径的圆形区域。

    :param feather: 边缘柔化比例，0 表示硬边（半径内 alpha 直接归零）。
                    大于 0 时，从芯部到外沿按比例衰减 alpha，避免锯齿。
    """
    if image.format() != QImage.Format.Format_ARGB32:
        raise ValueError("请先调用 to_argb32 转换图像")
    if radius <= 0:
        return

    soft = max(0.0, min(0.9, feather)) * radius
    core = radius - soft
    radius2 = radius * radius

    x0 = max(0, math.floor(cx - radius))
    x1 = min(image.width() - 1, math.ceil(cx + radius))
    y0 = max(0, math.floor(cy - radius))
    y1 = min(image.height() - 1, math.ceil(cy + radius))

    for y in range(y0, y1 + 1):
        scanline = memoryview(image.scanLine(y))
        dy = y - cy
        dy2 = dy * dy
        for x in range(x0, x1 + 1):
            dx = x - cx
            d2 = dx * dx + dy2
            if d2 > radius2:
                continue
            value = _PIXEL.unpack_from(scanline, x * 4)[0]
            alpha = value >> 24
            if alpha == 0:
                continue
            if soft <= 0:
                new_alpha = 0
            else:
                d = math.sqrt(d2)
                if d <= core:
                    new_alpha = 0
                else:
                    new_alpha = int(alpha * (radius - d) / soft)
            if new_alpha != alpha:
                _PIXEL.pack_into(scanline, x * 4, (new_alpha << 24) | (value & 0x00FFFFFF))


def erase_line(
    image: QImage,
    x0: float,
    y0: float,
    x1: float,
    y1: float,
    radius: float,
    feather: float = 0.15,
) -> None:
    """沿两点之间连续擦除，鼠标快速拖动也不会留下断点。"""
    distance = math.hypot(x1 - x0, y1 - y0)
    steps = max(1, math.ceil(distance / max(1.0, radius * 0.35)))
    for i in range(steps + 1):
        t = i / steps
        erase_circle(image, x0 + (x1 - x0) * t, y0 + (y1 - y0) * t, radius, feather)


def alpha_at(image: QImage, x: int, y: int) -> int:
    """取像素 alpha（0~255），供测试与状态显示使用。"""
    return (image.pixel(x, y) >> 24) & 0xFF


def save_png(image: QImage, path: str | Path) -> bool:
    """保存为 PNG；PNG 才保留透明通道，JPG 会把透明区域变回白色。"""
    target = str(path)
    if not target.lower().endswith(".png"):
        target += ".png"
    return image.save(target, "PNG")
