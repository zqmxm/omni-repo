# desktop/ · Windows 桌面程序 / exe

放 Python（PyInstaller）、Electron、Tauri、Go、Rust 等**桌面端**源码。一个软件一个子目录，例如 `desktop/img-tool/`。

## Python → exe 配方

```
desktop/img-tool/
├── README.md
├── requirements.txt
├── src/main.py
└── build.spec（可选）
```

本地（Windows，Python 3.10~3.12）：

```bat
pip install -r requirements.txt "pyinstaller>=6,<7"
pyinstaller --noconfirm --clean --onefile --windowed --name MyTool src/main.py
:: 产物 dist/MyTool.exe
```

CI 自动出包：新建 `.github/workflows/<name>.yml`，参考下面骨架（务必带路径过滤 + 手动触发）：

```yaml
name: Build MyTool EXE
on:
  push:
    paths: ["desktop/img-tool/**", ".github/workflows/<name>.yml"]
  workflow_dispatch:
permissions:
  contents: read
jobs:
  build:
    runs-on: windows-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: "3.11"
      - run: pip install -r desktop/img-tool/requirements.txt "pyinstaller>=6,<7"
      - run: pyinstaller --onefile --windowed --name MyTool desktop/img-tool/src/main.py
      - uses: actions/upload-artifact@v4
        with:
          name: MyTool-Windows-x64
          path: dist/MyTool.exe
```

打 tag 时想发到 Releases，再加一个 `softprops/action-gh-release@v2` 步骤即可。

## 规则

- `*.exe` / `*.msi` / `build/` / `dist/` **不入库**，一律走 Artifact / Release
- 源码、依赖清单、构建脚本、README 必须入库
- 不要写死本机绝对路径；中文路径读写用 `Path` / Unicode-safe API
