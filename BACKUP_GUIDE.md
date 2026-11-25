# BACKUP_GUIDE.md · 自动备份与交付系统

> 一句话：任何 AI 做完软件后，跑一条命令，源码进 GitHub、exe/apk 进 Release，**永远不覆盖旧版本**，永久可回溯。

本文件是 `AGENTS.md` 的补充规范。任何 AI 在本仓库工作时，交付阶段必须按这里的方式备份。

---

## 1. 三层存储模型

| 层 | 位置 | 内容 | 为什么不覆盖 |
| --- | --- | --- | --- |
| 源码层 | GitHub 仓库目录 | 全部源码、README、测试、配置 | 每次备份生成新 commit，git 历史天然不覆盖 |
| 版本层 | git tag `src/<name>/v<版本>` | 标记源码快照 | tag 名带版本号，递增不重复 |
| 产物层 | GitHub Release assets | exe / apk / zip / 安装包 | 每个版本一个独立 Release，tag 带时间戳 |

**为什么 exe 不放仓库目录**：GitHub 单文件上限 100MB、仓库软上限 1GB，而 Release 单资产上限 2GB 且不计入仓库容量。构建产物（exe/apk/dist/build）绝不入库，只进 Release。

---

## 2. 版本规则

- 版本格式：`<major>.<minor>.<patch>`，如 `1.0.0`
- 每个软件目录下有 `VERSION` 文件记录当前版本
- 每次 `publish` 默认 patch +1（`1.0.0` → `1.0.1`），可用 `--version` 指定
- Release tag：`<name>-v<版本>-<YYYYMMDD-HHMM>`，例如 `transparent-eraser-v1.0.1-20261126-0130`
- 源码 tag：`src/<name>/v<版本>`
- Asset 名：`<name>-v<版本>-<平台>.<扩展名>`，例如 `transparent-eraser-v1.0.1-windows-x64.exe`

---

## 3. 一次配置，以后免登录

```bash
python scripts/omni.py auth --token <你的GitHub_PAT>
```

Token 只保存在本机 `~/.omni-repo/config.json`（仓库外，不入库），脚本自动读取。之后所有备份、下载都在 AI 对话框里完成，不再需要手动登录 GitHub 网页。

也可以设置环境变量 `GITHUB_TOKEN` 替代配置文件。

---

## 4. 交付命令（AI 只需记这一条）

```bash
python scripts/omni.py publish --name <软件名> --artifact <产物路径>
```

`publish` 会自动完成：
1. 把工作区所有源码变更提交并同步到 GitHub（走 API，兼容代理环境）
2. 递增版本、打源码 tag
3. 创建 Release 并把 exe/apk 上传为 asset
4. 输出 Release 下载链接

其他命令：

```bash
python scripts/omni.py list                          # 列出所有备份版本
python scripts/omni.py list --name transparent-eraser  # 只看某个软件
python scripts/omni.py download --tag <tag>          # 下载某版本的产物
python scripts/omni.py version --name <软件名>       # 查看当前版本
```

---

## 5. 兼容任何 AI 软件

`omni.py` 只用 Python 标准库，不依赖任何 AI 平台插件或私有接口。三种接入方式：

1. **命令行 AI**（Cursor / Claude Code / Copilot / 通义灵码等）：在提示词里要求"完成后运行 `python scripts/omni.py publish ...`"
2. **聊天式 AI**（元宝 / ChatGPT / Kimi 等）：让 AI 生成文件后，你运行上述命令；或把 `mcp_server.py` 配成 MCP 工具
3. **MCP 原生**：把 `scripts/mcp_server.py` 注册到支持 MCP 的 AI，即可在对话里直接调用 `omni_publish` / `omni_list` / `omni_download`

无论哪个 AI 产出 exe/apk，只要按本规范放在对应平台目录并调用 `publish`，都会在 GitHub 上新增一份，互不覆盖。

---

## 6. 支持的平台目录

`python/` `web/` `node/` `desktop/`（Windows exe）`android/`（APK）`go/` `rust/` `java/` `flutter/` `miniapp/`（小程序）

新平台直接建小写目录，规则见 `AGENTS.md`。产物统一走 Release，扩展名按平台区分：`.exe` / `.apk` / `.zip` / `.dmg` 等。

---

## 7. 边界（如实说明）

- GitHub 免费账户 Release 单资产上限 2GB；单个仓库所有 Release 总量无硬性上限，但请合理使用
- "永久保留"的前提是账号和仓库不被删除；GitHub 本身是第三方托管，重要资料请另做本地备份
- 自动同步源码走 GitHub REST API，需要有效的 PAT（有 `repo` 权限）
- 构建 exe/apk 需要对应工具链：Windows exe 用 PyInstaller（CI 的 `windows-latest`），APK 用 Android SDK（CI 的 `ubuntu-latest`）
- 不提交任何密钥、keystore、token 到仓库；Token 只存本机配置文件
