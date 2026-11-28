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

## Go → exe 配方（无第三方依赖，可交叉编译）

```
desktop/自动操作助手/
├── README.md
├── VERSION
├── src/main.go
├── src/main_test.go
├── build.bat（Windows 本地打包）
└── build.sh（Linux/Mac 交叉编译）
```

Windows 本地双击 `build.bat`，或在 Linux/Mac 上执行 `bash build.sh`：

```
go build -ldflags="-H=windowsgui -s -w" -o "dist/软件本名.exe" ./src
```

CI 用 `actions/setup-go@v5` + `go test` + 上面的构建命令，参考 `.github/workflows/自动操作助手.yml`。

## 规则

- `*.exe` / `*.msi` / `build/` / `dist/` **不入库**，一律走 Artifact / Release
- 源码、依赖清单、构建脚本、README 必须入库
- 不要写死本机绝对路径；中文路径读写用 `Path` / Unicode-safe API

## 本目录已有的软件（2026-11-28 新增两个）

| 软件 | 目录 | 一句话说明 | 成品在哪 |
| --- | --- | --- | --- |
| 文件区域批量重命名 | `desktop/文件区域批量重命名/` | 从 PDF / Word / Excel 的固定位置读文字，给整批文件各自改名 | Releases → `文件区域批量重命名-latest` |
| 夸克扫描王批量处理 | `desktop/夸克扫描王批量处理/` | 批量把 PDF 放进夸克扫描王、批量收回到指定文件夹 | Releases → `夸克扫描王批量处理-latest` |

这两个都是**已经产出成品的归档**：源码在本目录，exe 只在 Release（仓库不收二进制）。成品含内置中文 OCR 模型 / 纯本地文件操作，CI 无法重建，所以没接 `build-manifest.json` 自动构建，也没加 workflow；后续每次更新都会**新增一个独立 Release**，历史保留。

两个目录里都有 `README.md`（怎么用、每个文件是什么）、`VERSION`、`CHECKSUMS.md`（成品校验值）。「夸克扫描王批量处理/历史版本/」放的是已作废的中间版源码，只备查，不要拿去编译。

### 批量替换助手（v3.1.1，接了自动构建）

`desktop/批量替换助手/` 是 Go 写的批量替换工具：**源码入库 + 进 `auto-backup.yml` 自动矩阵**（`build-manifest.json` 已去掉 `manual_release`），每次 push 自动构建 GUI/CLI 两个 exe 并**新增独立 Release**，历史版本全部保留。

- 源码：`src/` + `internal/`，Go 1.21+，零第三方依赖
- 本地出包：`build.bat`（GUI + CLI 两个 exe），或 `CROSS=1 bash build.sh`
- CI：改 `desktop/批量替换助手/**` 自动做**编译验证**并出 GUI + CLI 两个 Artifact（不自动改 Release）。完整 `go test ./src -v` 依赖 `testdata/` 下 3 个真实 `.doc/.xls` 二进制样本（不入库），需本地补齐后手动跑
- 成品：Releases → `批量替换助手-latest`（GUI 版 + CLI 版两个附件）
