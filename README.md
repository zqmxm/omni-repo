# omni-repo · 万能软件仓库

一个**不绑定语言、不绑定平台、也不绑定任何 AI** 的软件基地。

Python 工具、命令行脚本、静态网页、Node 服务、**Android APK**、Windows exe、Go / Rust / Java 工程、小程序、数据处理脚本……以后想写什么，都往这里放，不用每次新建仓库。

> 先把话说实：它不是「输入一句话就自动长出任意软件」的魔法盒子。它是**一套任何 AI 都能读懂的项目规范 + 多平台最小可运行模板 + 自动构建流水线**。真正写软件时，你把需求告诉任何一个 AI（元宝 / Cursor / Claude / Copilot / 通义灵码……），它读完根目录的 `AGENTS.md` 就知道该往哪写、怎么写、怎么测、怎么出包。地基打好了，上层什么都能盖。

---

## 一句话用法（在别的 AI 里也能用）

```bash
git clone https://github.com/zqmxm/omni-repo.git
cd omni-repo
# 把整个目录拖进任意 AI 的工作区，然后说：
# 「先读 AGENTS.md，按约定在 python/ 下写个批量重命名工具，补测试，给我运行方式」
```

不需要本对话、不需要元宝账号、不需要任何私有接口。仓库里全是纯文本 + 标准开源工具链，MIT 许可，离线解压 zip 也能用。详细见 [AI_GUIDE.md](AI_GUIDE.md)。

---

## 目录约定

| 目录 | 放什么 | 现状 |
| --- | --- | --- |
| `python/` | Python 脚本、CLI、数据处理、桌面程序源码 | ✅ 示例 + unittest |
| `web/` | 静态网页、单页小工具（零依赖，双击即开） | ✅ 带首页 |
| `node/` | Node 服务、前端工程、构建脚本 | ✅ `node index.js` 可跑 |
| `android/` | **Android 原生工程，CI 自动出 APK** | ✅ 最小 Gradle 工程 + 自动抠图（一键抠图 App） |
| `desktop/` | Windows exe / 桌面应用源码与打包说明 | ✅ 说明 + 打包配方 + 透明橡皮擦 + 自动操作助手 + 批量替换助手 + 2 个手动归档软件 |
| `scripts/` | 通用脚手架 + 备份工具 | ✅ `new_project.py` / `omni.py` / `mcp_server.py` |
| `data/` | 样例数据、配置、素材 | 占位，按需放 |
| `go/` `rust/` `java/` `flutter/` `miniapp/` | 其他语言 / 端 | 按需新建，规则见 AGENTS.md |
| `BACKUP_GUIDE.md` | **自动备份系统规范** | ✅ 只保留最新版 / 对话内上传下载 |
| `build-manifest.json` | **自动构建备份清单** | ✅ 加一行即自动出包备份 |

**三条铁律**：
1. 一个软件一个子目录，目录里必须自带 `README.md` 写清「怎么跑」。互不干扰，CI 按改动路径分别检查。
2. ⭐ **命名铁律：软件叫什么名字，GitHub 上就叫什么名字。** 软件本名（中文软件用中文名）同时用作：项目目录名、README 标题、exe/apk 文件名、Release tag、workflow 文件名和备份 `--name`。禁止用英文代号/拼音缩写代替。例：「透明橡皮擦」对应 `desktop/透明橡皮擦/`、`透明橡皮擦.exe`、`透明橡皮擦.yml`、tag `透明橡皮擦-v1.0.0-...`。任何 AI 做的软件一律照此执行，详见 `AGENTS.md`。
3. ⭐⭐ **交付铁律：源码 + exe/apk 成品缺一不可。** 任何 AI 用这个仓库做软件，必须同时留下**源码**（提交进仓库目录）和**可运行成品**（Windows 为 `*.exe`，Android 为 `*.apk`，上传到 GitHub Release）。只交代码、只给运行命令、只给网页或只给源码包都算没做完。`omni.py publish` 会强制校验产物后缀和源码存在性，不满足直接报错。详见 `AGENTS.md`。

---

## 备份系统：每个软件都存到 GitHub，只保留最新版

这是本仓库的核心能力，详见 [BACKUP_GUIDE.md](BACKUP_GUIDE.md)。

```bash
python scripts/omni.py auth --token <你的PAT>              # 只配一次，存在本机
python scripts/omni.py publish --name <软件名> --artifact <exe/apk路径>   # 备份源码+产物
python scripts/omni.py list                                 # 列出所有历史版本
python scripts/omni.py download --tag <tag>                 # 下载任意版本的 exe
```

- **源码**进仓库目录（新 commit + VERSION 递增），**产物**进该软件固定的 `<软件名>-latest` Release（不受 100MB 限制）
- **强制交付**：`--artifact` 只接受 `.exe` 或 `.apk`，项目目录必须有真实源码；zip/脚本/网页/空目录会被拒绝，不能只交一半
- 每个软件**只保留一个 Release**：更新时覆盖旧附件并删除历史 Release，GitHub 上永远只有最新版
- 支持 MCP 的 AI 注册 `scripts/mcp_server.py` 后，可**在对话框里直接上传/下载 exe**，不用打开 GitHub（配置见 `mcp-config.example.json`）
- CI 构建的产物：在 `build-manifest.json` 加一行，`auto-backup.yml` 自动打包并备份

---

## 流水线

| workflow | 触发 | 产出 |
| --- | --- | --- |
| `ci.yml` | 每次 push | Python 单测 / Node 冒烟 / Web 检查 / 脚手架自检 |
| `android.yml` | `android/**` 改动或手动 | `app-debug.apk` artifact（最小模板） |
| `自动抠图.yml` | `android/自动抠图/**` 改动或手动 | 算法自检 + `自动抠图.apk` artifact |
| `透明橡皮擦.yml` | 透明橡皮擦改动或手动 | 透明橡皮擦.exe artifact |
| `自动操作助手.yml` | `desktop/自动操作助手/**` 改动或手动 | 自检 + `自动操作助手.exe` artifact |
| `批量替换助手.yml` | `desktop/批量替换助手/**` 改动或手动 | 自检 + `批量替换助手.exe` / `批量替换助手-CLI.exe` artifact |
| `auto-backup.yml` | 清单/源码改动或手动 | **自动构建 + 更新固定 Release（只留最新版）** |

产物在仓库 → **Actions** → 对应工作流 → Artifacts，或 **Releases** 页签直接下载。

---

## 快速验证

```bash
python python/hello.py                       # Hello, world!
cd python && python -m unittest discover -s tests -v
cd ../node && node index.js
# web/index.html 双击用浏览器打开
python scripts/new_project.py --name my-tool --template python
python scripts/new_project.py --name my-app  --template android
```

脚手架模板：`python` / `web` / `node` / `android` / `desktop` / `blank`。

---

## 怎么让我（或任何 AI）往里写

直接说需求，例如：
- 「写个批量压缩图片的 Python 工具，放 `python/`」
- 「做个待办清单网页，放 `web/`」
- 「给 Android App 加个相机拍照页面，APK 照样能打出来」
- 「加个 Go 写的 HTTP 接口，配好构建」

对方会建目录、写代码、补测试、**打出 exe/apk 成品**、配 CI、推上来，并用中文告诉你源码位置和 exe/apk 下载链接。**只写代码不给成品不算完成**。**换个 AI 也一样**，因为它读的是同一份 `AGENTS.md`，不是某个平台的隐藏规则。

---

## 效果与边界（如实说）

- ✅ 适合：小工具、脚本、原型、学习项目、静态页、CLI、桌面小程序、原生 Android App
- ⚠️ 大型工程（多服务、数据库、iOS 签名、AI 大模型权重、应用商店发布）代码能放，但构建/签名/发布要单独配，默认模板不覆盖
- ⚠️ APK 构建依赖 Android SDK + JDK 17 + Gradle 8.7，本机若没装就交给 CI；**Release 签名 APK 需要你自己的 keystore**，仓库里绝不放密钥
- ❌ 不预置：账号密钥、私有依赖、二进制大文件（用 Git LFS 或 Release）

## 合规

请只把生成的代码用在你有权使用的场景；移除他人版权水印、绕过平台保护等用途不在支持范围。使用者自行承担合规责任。

## 许可

MIT，见 [LICENSE](LICENSE)。
