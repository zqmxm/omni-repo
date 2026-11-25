# AGENTS.md — 给任何 AI 的项目规则

> 本文件是这个仓库的**唯一权威规范**。无论你来自 Cursor、Claude Code、GitHub Copilot、通义灵码、CodeGeeX、元宝、ChatGPT 还是别的工具，动手前先读完本文件，然后严格按它执行。不要依赖对话里临时给的偏好，以本文件为准。

## 1. 这个仓库是什么

通用软件基地：一个软件一个子目录，覆盖 Python / Web / Node / Android(APK) / Windows exe / Go / Rust / Java / Flutter / 小程序 / 数据脚本。不绑定语言、不绑定平台、**不绑定任何特定 AI 或服务**。所有内容必须是纯文本 + 标准开源工具链，可离线克隆/解压即用。

## 2. 目录与命名

- 已有目录：`python/` `web/` `node/` `android/` `desktop/` `scripts/` `data/`
- 新语言/新端直接新建小写目录：`go/` `rust/` `java/` `flutter/` `miniapp/`
- **一个软件 = 一个子目录**
- 空目录放 `.gitkeep`，不要提交构建产物

### ⭐ 命名铁律（最高优先级，任何 AI 都必须遵守）

> **软件叫什么名字，GitHub 上就用什么名字。** 一律用软件的真实名字（中文软件就用中文），
> 禁止用英文代号、拼音缩写或繁简变体代替。这条规则跨 AI、跨软件、跨语言一律生效。

- **项目目录名 = 软件本名**：例如软件叫「透明橡皮擦」，目录就是 `desktop/透明橡皮擦/`；
  别的 AI 做的软件叫「图片批量压缩器」，目录就是 `python/图片批量压缩器/`。
- **README 标题 = 软件本名**：项目 `README.md` 第一行 `# <软件本名>`。
- **exe / apk / 安装包文件名 = 软件本名**：例如 `透明橡皮擦.exe`、`图片批量压缩器.apk`。
  PyInstaller 用 `--name <软件本名>`，gradle `archivesBaseName` 也用软件本名。

### ⭐ 交付给用户的三类资产，显示名必须带软件本名

每个软件的 Release 里都要有这三类资产，**用户在 GitHub 页面上看到的显示名必须用中文软件本名**：

| 资产 | 显示名（label，中文） | 实际文件名（ASCII，GitHub 限制） |
| --- | --- | --- |
| 可执行文件 | `透明橡皮擦` | `toumingxiangpicao-v<版本>-windows-x64.exe` |
| 源码压缩包 | `透明橡皮擦源码` | `toumingxiangpicao-source-v<版本>.zip` |
| 源码包（可选） | `透明橡皮擦源码` | `toumingxiangpicao-source-v<版本>.tar.gz` |

- **实现方式**：上传时用 `?name=<拼音文件名>`（GitHub 会剥离中文 name），上传成功后立刻
  `PATCH /releases/assets/<id>` 设置 `label` 为**中文软件本名**——GitHub 界面优先显示 label，
  这样用户看到的就是「透明橡皮擦」「透明橡皮擦源码」。
- 源码压缩包内容 = 该软件项目目录（排除 `build/` `dist/` `__pycache__` / `*.exe`）。
- **GitHub 自动生成的 "Source code (zip)" / "Source code (tar.gz)" 是平台固定文字，无法改名**，
  所以必须额外上传上面这种带中文显示名的源码包来满足命名要求。
- Release **标题**、**tag**、**仓库目录**、**本地产物文件**同样全部用中文软件本名。

- **Release tag / 名称 / 备份标识 = 软件本名**：`透明橡皮擦-v1.0.0-<时间戳>`，
  `omni.py publish --name 透明橡皮擦`，`build-manifest.json` 的 `name`/`dir`/`artifact` 全部用本名。
- **workflow 文件名也用软件本名**：`desktop/透明橡皮擦/` 对应 `.github/workflows/透明橡皮擦.yml`。
- **生成的文件一律用中文名**：本地生成的源码包叫 `透明橡皮擦源码.zip`，用户下载到本地的文件叫
  `透明橡皮擦 v1.0.0.exe` —— 让用户保存下来的文件一眼就知道是什么，绝不留英文代号文件名。
- 只允许的例外：平台根目录（`python/` `desktop/` 等）和通用脚本目录保持小写英文（被代码/CI 引用）；
  软件名本身不含空格，也不含 `/ \ : * ? " < > |` 等系统保留字符。
- 每个项目目录**必须**自带 `README.md`，写清：功能、怎么运行、依赖、怎么测试、怎么出包

### ⭐ 全中文可读规则（让所有者看得懂、找得到文件）

> 仓库所有者不是程序员。任何 AI 生成的内容都要让所有者**用中文就能看懂、找得到文件**。

- **能用中文的地方一律用中文**：文档、说明、注释、软件名、产物名、显示名都用中文；
  只有被代码 / CI / 工具链强制要求的标识（平台目录名、`.py` `.yml` 等扩展名、
  git ref、GitHub API 字段名）保留英文，并在说明里解释清楚。
- **每个文件夹必须有中文说明**：每个平台根目录（`python/` `web/` `node/` `android/`
  `desktop/` `data/` `scripts/` `.github/workflows/`）都要有 `README.md`，用大白话写清
  「这个文件夹是干什么的、里面每个文件是什么」。新增目录时必须同时补说明。
- **维护 `仓库地图.md`（根目录）**：这是给所有者看的中文总说明书。每次新增 / 删除 / 改名
  软件或文件，**必须同步更新 `仓库地图.md`**，保证里面写的路径、文件名和实际完全一致。
  地图必须包含：① 做的软件在哪找（本地路径 + GitHub 链接）② 每个文件夹干什么
  ③ 每个根目录文件是什么 ④ 下载 / 备份 / 找老版本怎么操作（用大白话，不说术语）。
- **回复所有者时用中文**，并明确告诉他：文件保存在哪里、是什么名字、怎么打开、怎么找到。

## 3. 加新项目的固定流程

1. 优先用脚手架：`python scripts/new_project.py --name <name> --template <python|web|node|android|desktop|blank>`
   （`<name>` 用软件本名，中文照传）
2. 在生成的目录里实现功能，不要污染其他目录
3. 补可自动运行的测试（Python → `unittest`，Node → `npm test`，Web → 静态检查）
4. 若需要出包，在 `.github/workflows/` 加一个**带路径过滤**的 workflow，避免每次 push 都全量跑
5. **给新目录写中文 `README.md`**——用大白话说明这个软件干什么、每个文件是什么（全中文可读规则）
6. **更新根目录 `仓库地图.md`**——加入新软件/文件的位置与用途，保证所有者找得到
7. 更新根目录 `README.md` 的目录表（新增了目录时）
8. 备份（见第 5 节），并在回复里用中文给出：改了哪些文件、文件保存在哪、叫什么名字、怎么跑、怎么拿到产物

## 4. 编码规范

- 全部文件 **UTF-8**、**LF** 换行，遵循 `.editorconfig`
- Python：3.10+，标准库优先，依赖写进该项目自己的 `requirements.txt`；类型注解、docstring 用中文或英文均可，保持统一
- Node：Node 20，`package.json` 里必须有 `start` 和 `test` 脚本，零依赖优先
- Web：静态页零构建优先，能双击打开；需要框架时自带锁文件
- **Android**：JDK 17 / Gradle 8.7 / AGP 8.5.2 / compileSdk 34 / minSdk 24，纯 Java 或 Kotlin 均可；`app/build.gradle` 里不要写死签名配置
- Windows exe：PyInstaller 6.x，构建放到 `windows-latest` runner
- 注释和文档默认**中文**（与仓库所有者语言一致）

## 5. 交付必做：备份到 GitHub（不覆盖旧版本）

**每个软件做完并产出可执行文件后，必须备份。** 这是本仓库的核心能力，详见 `BACKUP_GUIDE.md`。

- 一条命令完成源码 + 产物备份：
  `python scripts/omni.py publish --name <软件本名> --artifact <产物路径>`
  （`<软件本名>` 必须是软件的真实名字，中文软件就传中文，例如 `--name 透明橡皮擦`；
  脚本会自动用这个名字命名仓库目录、Release tag 和产物资产，绝不用英文代号）
- 每个版本生成**独立的 Release**，tag 带版本号和时间戳，**永不覆盖旧版本**
- 源码走仓库目录（每次新 commit + VERSION 文件递增），产物 exe/apk 走 Release（不入库，避开 100MB 限制）
- 若产物由 CI 构建：在 `build-manifest.json` 加一条配置，`auto-backup.yml` 会自动构建并备份，无需手工上传
- 用户无需登录 GitHub 网页：Token 一次配置（`python scripts/omni.py auth --token <PAT>`）后存在本机仓库外
- 支持 MCP 的 AI：注册 `scripts/mcp_server.py` 后可在对话框直接调用 `omni_publish` / `omni_list` / `omni_download`

## 6. 绝对不要做的事

- ❌ 提交 `.env`、keystore、token、密码、私钥、账号 cookie、`.omni-repo/config.json`
- ❌ 提交 `*.exe` `*.apk` `*.zip` `node_modules/` `build/` `dist/` 等构建产物（用 Release / Artifact 分发）
- ❌ 引入需要付费账号、私有 API、特定 AI 平台插件才能运行的依赖
- ❌ 修改或删除别的项目目录里的内容
- ❌ 写「能破解/绕过/移除他人版权保护」的功能
- ❌ 覆盖已有版本的 Release 或产物——永远新增版本

## 7. CI 约定

- `ci.yml`：每次 push 跑 Python 单测 / Node 冒烟 / Web 存在性，必须保持绿
- `android.yml`：仅 `android/**` 或自身改动时触发，产出 `app-debug.apk` artifact
- `auto-backup.yml`：读 `build-manifest.json`，自动构建 exe/apk 并创建独立 Release（不覆盖）
- 新增 workflow 一律加 `workflow_dispatch:` 方便手动重跑
- 构建失败时如实报告，不要假装成功；本机跑不了的工具链（Android SDK / Windows）交给 CI

## 8. 交付给用户的回复模板

```
已写到 <目录>/，新增 <N> 个文件。
运行：<命令>
测试：<命令 / CI 链接>
产物：<Release 链接 / Artifact / 本地路径>
备份：python scripts/omni.py publish ... （或已由 CI 自动备份）
边界：<做不到或需要额外环境的地方>
```

## 9. 权限与合规

仓库归所有者所有，你只是代为写代码。所有生成内容按 MIT 许可。不要声称仓库绑定了某个 AI 服务；用户换任何 AI 打开本仓库，读本文件即可继续工作。
