# AGENTS.md — 给任何 AI 的项目规则

> 本文件是这个仓库的**唯一权威规范**。无论你来自 Cursor、Claude Code、GitHub Copilot、通义灵码、CodeGeeX、元宝、ChatGPT 还是别的工具，动手前先读完本文件，然后严格按它执行。不要依赖对话里临时给的偏好，以本文件为准。

## 1. 这个仓库是什么

通用软件基地：一个软件一个子目录，覆盖 Python / Web / Node / Android(APK) / Windows exe / Go / Rust / Java / Flutter / 小程序 / 数据脚本。不绑定语言、不绑定平台、**不绑定任何特定 AI 或服务**。所有内容必须是纯文本 + 标准开源工具链，可离线克隆/解压即用。

## 2. 目录与命名

- 已有目录：`python/` `web/` `node/` `android/` `desktop/` `scripts/` `data/`
- 新语言/新端直接新建小写目录：`go/` `rust/` `java/` `flutter/` `miniapp/`
- **一个软件 = 一个子目录**，目录名用小写字母/数字/连字符，不用空格和中文
- 每个项目目录**必须**自带 `README.md`，写清：功能、怎么运行、依赖、怎么测试、怎么出包
- 空目录放 `.gitkeep`，不要提交构建产物

## 3. 加新项目的固定流程

1. 优先用脚手架：`python scripts/new_project.py --name <name> --template <python|web|node|android|desktop|blank>`
2. 在生成的目录里实现功能，不要污染其他目录
3. 补可自动运行的测试（Python → `unittest`，Node → `npm test`，Web → 静态检查）
4. 若需要出包，在 `.github/workflows/` 加一个**带路径过滤**的 workflow，避免每次 push 都全量跑
5. 更新根目录 `README.md` 的目录表（新增了目录时）
6. 在回复里给出：改了哪些文件、怎么跑、怎么拿到产物

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
  `python scripts/omni.py publish --name <软件名> --artifact <产物路径>`
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
