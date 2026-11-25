# 透明橡皮擦

Windows 桌面小工具：像抠图一样，用橡皮擦把图片里不要的像素擦掉。
**擦掉的地方是透明的（alpha = 0），不是白色。** 界面用灰白棋盘格表示透明区域。

## 功能

- 打开 PNG / JPG / JPEG / BMP / WebP，也支持把图片直接拖进窗口
- 按住鼠标左键擦除，笔刷大小可调（**1~300 px**，滑杆小尺寸段为指数映射，1 px 也能精确选中），边缘可柔化
- 工具栏 `−` / `+` 或键盘 `[` / `]` 逐像素微调笔刷
- 快速拖动不会断点；Ctrl+Z 撤销、Ctrl+Shift+Z 重做（最多 50 步）
- 滚轮缩放、适应窗口、全部擦除
- 另存为 PNG，透明通道完整保留
- 快捷键：Ctrl+O 打开、Ctrl+S 保存、Ctrl+Q 退出

## 运行

```bash
pip install -r requirements.txt
python src/main.py
```

## 测试

```bash
# Windows 有桌面时
python -m unittest discover -s tests -v
# 无界面环境 / CI
set QT_QPA_PLATFORM=offscreen && python -m unittest discover -s tests -v
```

测试覆盖：擦除处 alpha 归零、RGB 图转换后可擦、柔边半透明、连续拖动无断点、PNG 保存保留透明、窗口能打开图片。

## 出包（Windows exe）

```bash
pip install -r requirements.txt
python build.py
:: 产物：dist/透明橡皮擦.exe（单文件，双击即用，无需装 Python）
```

GitHub Actions：改动本目录或 `.github/workflows/透明橡皮擦.yml` 时自动跑测试并产出
`透明橡皮擦-Windows-x64` artifact，也可在 Actions 页面手动触发。

## 边界

- **只有 PNG 保留透明**；保存成 JPG/BMP 时透明区域会被填成白色，所以保存对话框限定为 PNG。
- 这是手动橡皮擦，不是 AI 自动抠图；复杂边缘需要自己调整笔刷大小和缩放。
- 打包产物（exe / build / dist）按仓库规范不入库，只走 Artifact / Release 分发。
