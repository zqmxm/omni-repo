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
| `android/` | **Android 原生工程，CI 自动出 APK** | ✅ 最小 Gradle 工程 |
| `desktop/` | Windows exe / 桌面应用源码与打包说明 | ✅ 说明 + 打包配方 + 透明橡皮擦工具 |
| `scripts/` | 通用脚手架、辅助脚本 | ✅ `new_project.py` |
| `data/` | 样例数据、配置、素材 | 占位，按需放 |
| `go/` `rust/` `java/` `flutter/` `miniapp/` | 其他语言 / 端 | 按需新建，规则见 AGENTS.md |

**唯一铁律**：一个软件一个子目录，目录里必须自带 `README.md` 写清「怎么跑」。互不干扰，CI 按改动路径分别检查。

---

## 三条流水线

| workflow | 触发 | 产出 |
| --- | --- | --- |
| `ci.yml` | 每次 push | Python 单测 / Node 冒烟 / Web 检查 / 六种脚手架模板自检 |
| `android.yml` | `android/**` 改动或手动 | **`app-debug.apk` 可下载产物**（含合法性校验） |
| `desktop/` 里的配方 | 按需复制 | Windows exe，参考 PyInstaller |

APK 你在网页上就能拿：仓库 → **Actions** → `Android APK` → 底部 Artifacts → `omni-android-debug-apk`，下载后传到手机安装（需允许「未知来源」）。

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

对方会建目录、写代码、补测试、配 CI、推上来，并告诉你链接。**换个 AI 也一样**，因为它读的是同一份 `AGENTS.md`，不是某个平台的隐藏规则。

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
