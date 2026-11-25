# AI_GUIDE.md · 在别的 AI 软件里也能自己用

这个仓库**故意不绑定任何 AI**。它里面没有私有接口、没有平台插件、没有账号体系，只有：纯文本规范 + 标准开源工具链（Git / GitHub Actions / Python / Node / Gradle / Android SDK）。所以换个 AI，照用不误。

## 为什么能跨 AI

| 文件 | 谁会自动读 |
| --- | --- |
| `AGENTS.md` | 业界通用规范，多数 Agent 型工具默认识别；也可手动 @ 或粘贴 |
| `CLAUDE.md` | Claude Code 启动时自动加载 |
| `.cursorrules` | Cursor 打开工作区时自动加载 |
| `.github/copilot-instructions.md` | GitHub Copilot / Copilot Workspace |
| `AI_GUIDE.md` | 给人看的说明；在任意聊天式 AI 里粘贴即可当「项目规则」 |

四个入口文件内容互相指向 `AGENTS.md`，改规则只改一处，不会出现各 AI 理解不一致。

## 各平台怎么用

**Cursor**
把整个 `omni-repo` 目录作为工作区打开，`.cursorrules` 自动生效。想更稳就在对话框里 `@AGENTS.md` 再说需求。

**Claude Code**
`cd omni-repo && claude`，`CLAUDE.md` 自动加载；说「按 AGENTS.md 在 node/ 下写一个 XXX」。

**GitHub Copilot / Copilot Workspace**
`.github/copilot-instructions.md` 自动生效；也可以在提问里附上 `AGENTS.md`。

**通义灵码 / CodeGeeX / 豆包 / 元宝 / ChatGPT / Kimi 等聊天式 AI**
三种方式任选：
1. 把下面这段提示词 + `AGENTS.md` 全文粘进新对话开头
2. 直接上传本仓库的 zip，让 AI 先读 `AGENTS.md`
3. 把 `AGENTS.md` 放进「项目知识库 / 自定义指令 / 系统提示」里

## 万能提示词（复制即可）

```
你正在 omni-repo 仓库里工作。先完整阅读根目录的 AGENTS.md，
严格按其中的目录约定、编码规范、CI 规则和禁止项执行。

需求：<在这里写你要的软件>

要求：
- 一个软件一个子目录，目录内写 README.md 说明怎么运行
- 补上能自动跑的测试
- 需要安装包时配好带路径过滤的 GitHub Actions workflow
- 不提交任何密钥和构建产物
- 最后告诉我：改了哪些文件、怎么运行、怎么测试、产物在哪、有什么边界
```

把 `<需求>` 换成「写个批量重命名图片的 Python 工具」「给 Android App 加拍照页」「做个待办网页」都行。

## 完全离线也能用

`omni-repo.zip` 解压后就是完整仓库（不含 `.git`）。把它拖进任何支持「上传文件夹 / 本地工作区」的 AI 工具，指向 `AGENTS.md`，即可开始。不需要原对话、不需要 GitHub 账号、不需要联网（构建 APK / exe 时才需要对应 SDK 和 CI）。

## 自动备份：做完软件直接存到 GitHub

本仓库已内置备份系统，任何 AI 做完软件后都能把源码和 exe/apk 存到 GitHub，**每个版本独立保留、永不覆盖旧版本**。详见 `BACKUP_GUIDE.md`。

**一次配置**（任何 AI 帮你做，或你自己跑一次）：
```bash
python scripts/omni.py auth --token <你的GitHub_PAT>
```
Token 只存本机 `~/.omni-repo/config.json`（仓库外，不入库），以后不用再登录 GitHub 网页。

**每次交付只需一条命令**：
```bash
python scripts/omni.py publish --name <软件名> --artifact <产物路径>
python scripts/omni.py list                    # 看所有历史版本
python scripts/omni.py download --tag <tag>    # 下载某个版本的 exe
```

**在 AI 对话框里直接上传/下载**（无需打开 GitHub）：
把 `scripts/mcp_server.py` 注册为 MCP 工具（配置模板见 `mcp-config.example.json`），AI 就能直接调用 `omni_publish` / `omni_list` / `omni_download`。支持 MCP 的 Cursor、Claude Desktop、WorkBuddy 等都可用。

**CI 自动备份**：产物由 GitHub Actions 构建时，在 `build-manifest.json` 加一条配置，`auto-backup.yml` 会自动打包并创建独立 Release，连命令都不用敲。

## 换 AI 时不会丢的东西

- 目录约定与命名规则
- 各语言版本与工具链版本
- CI 触发与产物路径
- 禁止提交清单与合规边界
- 「一个项目一个 README」的习惯
- **备份规范与版本历史（全部在 GitHub，跨 AI 可见）**

**会丢的只有对话记忆**——所以规范才全部写进仓库，而不是留在聊天里。

## 边界（别被「万能」误导）

- 仓库不自动生成任意软件，它让任何 AI 接手时**零成本起步**
- APK 需要 Android SDK / JDK 17 / Gradle；Release 签名需要你自己的 keystore
- iOS 应用、应用商店发布、大模型权重、多服务集群，代码能放，构建发布要单独配
- 别把密钥交给任何 AI 或写进仓库
