> **备份规则 v2 · 永不覆盖（唯一权威说明）**：每次做完软件 / 每次 push 触发构建，
> 都会**新增一个全局唯一的 Release**（tag = `<软件名>-v<版本>-<UTC时间戳>-<随机>`）。
> 源码进 git 新 commit，成品 exe/apk 挂到这个新 Release。
> **历史 Release、tag、附件一律不删除、不覆盖、不使用 `overwrite_files`**，
> 任意一个旧版本都能用 `omni.py download --tag` 单独取回。
> 唯一权威规范见 `BACKUP_GUIDE.md`；本文件其余章节若与本节冲突，以本节为准。

# BACKUP_GUIDE.md · 自动备份与交付系统

> 一句话：任何 AI 做完软件后，跑一条命令，源码进 GitHub、exe/apk 进**全新的独立 Release**，**历史版本全部保留、永不覆盖**。

本文件是 `AGENTS.md` 的补充规范。任何 AI 在本仓库工作时，交付阶段必须按这里的方式备份。

> ⭐⭐ **交付铁律**：任何软件都必须同时留下**源码 + exe/apk 成品**，缺一不可。
> `publish` 会强制校验：产物必须是 `.exe` 或 `.apk`，项目目录必须有源码文件；不满足直接报错。

---

## 1. 存储模型（每次新增、永不覆盖）

| 层 | 位置 | 内容 | 更新方式 |
| --- | --- | --- | --- |
| 源码层 | GitHub 仓库目录 | 全部源码、README、测试、配置 | 每次备份生成新 commit，git 历史照常保留，可查旧代码 |
| 版本层 | `VERSION` 文件 | 记录当前版本号 | 每次 `publish` 默认 patch +1 |
| 产物层 | GitHub Release assets | **exe / apk** + 源码 zip | 每次备份新建独立 Release（唯一 tag）；旧附件与历史 Release 全部保留 |

**为什么 exe 不放仓库目录**：GitHub 单文件上限 100MB、仓库软上限 1GB，而 Release 单资产上限 2GB 且不计入仓库容量。构建产物（exe/apk/dist/build）绝不入库，只进 Release。

---

## 2. 版本与命名规则

- 版本格式：`<major>.<minor>.<patch>`，如 `1.0.0`；每个软件目录的 `VERSION` 记录当前版本
- 每次 `publish` 默认 patch +1（`1.0.0` → `1.0.1`），可用 `--version` 指定
- `<name>` 一律是**软件本名**：中文软件用中文名（如 `透明橡皮擦`），禁止英文代号/拼音缩写代替
- **Release tag 全局唯一**，例如 `透明橡皮擦-v0.0.13-20261128-221530-a1b2c3`；绝不复用固定 `-latest` tag
- Release 标题包含当前版本，例如 `透明橡皮擦 v1.0.1`
- 资产显示名（label）全部带软件本名：

| 资产 | 显示名 label | 实际文件名（ASCII，GitHub 限制） |
| --- | --- | --- |
| 可执行文件 | `透明橡皮擦` | `toumingxiangpicao-windows-x64.exe` |
| 源码压缩包 | `透明橡皮擦源码` | `toumingxiangpicao-source.zip` |

  - ⚠️ GitHub Release 附件**文件名**经 API 上传会剥离中文，所以实际文件名用 ASCII；
    GitHub 界面优先显示 **label**，label 设为中文软件本名，用户看到的就是「透明橡皮擦」「透明橡皮擦源码」。
  - GitHub 自动生成的 "Source code (zip/tar.gz)" 是平台固定文字无法改名，故脚本额外上传带中文显示名的源码包。
- 项目目录、README 标题、exe/apk 文件名、workflow 文件名同样用软件本名，详见 `AGENTS.md` 的「命名铁律」。

---

## 3. 一次配置，以后免登录

```bash
python scripts/omni.py auth --token <你的GitHub_PAT>
```

Token 只保存在本机 `~/.omni-repo/config.json`（仓库外，不入库），脚本自动读取。也可以设置环境变量 `GITHUB_TOKEN` 替代配置文件。

---

## 4. 交付命令（AI 只需记这一条）

```bash
python scripts/omni.py publish --name <软件名> --artifact <产物路径>
```

`publish` 会自动完成：

1. **先校验**：项目目录有源码 + `--artifact` 是 `.exe` 或 `.apk`（否则立即报错）
2. 把工作区所有源码变更提交并同步到 GitHub（走 API，兼容代理环境）
3. 递增 `VERSION`
4. **新建独立 Release**（唯一 tag）：上传新的 exe/apk 和源码 zip；不碰任何已有 Release
5. **删除该软件的历史 Release**，GitHub 上只剩最新版；源码 git 历史不受影响
6. 输出 Release 下载链接（回复用户时必须同时给出源码位置和成品链接）

其他命令：

```bash
python scripts/omni.py list                          # 列出所有软件的最新版 Release
python scripts/omni.py list --name 透明橡皮擦          # 只看某个软件
python scripts/omni.py list --name 透明橡皮擦           # 列出全部历史版本
python scripts/omni.py download --tag <tag>  # 下载任意一个历史版本的 exe
python scripts/omni.py version --name 透明橡皮擦       # 查看当前版本
```

---

## 5. 兼容任何 AI 软件

`omni.py` 只用 Python 标准库，不依赖任何 AI 平台插件或私有接口。三种接入方式：

1. **命令行 AI**：完成后运行 `python scripts/omni.py publish ...`
2. **聊天式 AI**：让 AI 生成文件后由你运行上述命令；或把 `mcp_server.py` 配成 MCP 工具
3. **MCP 原生**：注册 `scripts/mcp_server.py` 后可在对话框直接调用 `omni_publish` / `omni_list` / `omni_download`

无论哪个 AI 产出 exe/apk，只要按规范放好并调用 `publish`，都会更新该软件的最新版 Release。

---

## 6. 支持的平台目录

`python/` `web/` `node/` `desktop/`（Windows exe）`android/`（APK）`go/` `rust/` `java/` `flutter/` `miniapp/`（小程序）

新平台直接建小写目录，规则见 `AGENTS.md`。**成品统一走 Release，且只接受 `.exe` 或 `.apk`**；源码 zip 是附加物，不能代替成品。

---

## 7. 边界（如实说明）

- **永不覆盖意味着 GitHub 上会逐步积累 Release**，这是设计目标不是副作用；每个都能单独下载，配合 git 历史可精确回退
- GitHub 免费账户 Release 单资产上限 2GB
- 自动同步源码走 GitHub REST API，需要有效的 PAT（有 `repo` 权限）
- 构建 exe/apk 需要对应工具链：Windows exe 用 PyInstaller / Go，APK 用 Android SDK（CI 已配置）
