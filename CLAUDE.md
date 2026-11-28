> **备份规则 v2 · 永不覆盖（唯一权威说明）**：每次做完软件 / 每次 push 触发构建，
> 都会**新增一个全局唯一的 Release**（tag = `<软件名>-v<版本>-<UTC时间戳>-<随机>`）。
> 源码进 git 新 commit，成品 exe/apk 挂到这个新 Release。
> **历史 Release、tag、附件一律不删除、不覆盖、不使用 `overwrite_files`**，
> 任意一个旧版本都能用 `omni.py download --tag` 单独取回。
> 唯一权威规范见 `BACKUP_GUIDE.md`；本文件其余章节若与本节冲突，以本节为准。

# CLAUDE.md

本项目规则以根目录 **`AGENTS.md`** 为唯一权威，请先完整阅读再动手。

要点速记：
- ⭐ 命名铁律：软件叫什么名字，GitHub 上就用什么名字（中文软件用中文名）——目录名、README 标题、exe/apk 文件名、Release tag、workflow 文件名、备份 `--name` 全部用软件本名，禁止英文代号/拼音缩写。例：透明橡皮擦 → `desktop/透明橡皮擦/`、`透明橡皮擦.exe`、`透明橡皮擦.yml`
- ⭐⭐ 交付铁律（最高优先级）：**源码 + exe/apk 成品缺一不可**。源码提交进仓库，exe/apk 上传到 Release；禁止只交代码/命令/网页/源码包。`omni.py publish` 会强制校验产物必须是 `.exe` 或 `.apk` 且项目有源码，不满足直接报错
- ⭐ 全中文可读：能用中文的都用中文；每个文件夹都要有中文 README 说明「干什么、每个文件是什么」；新增/改名后必须同步更新根目录「仓库地图.md」，让所有者找得到文件。回复用中文，说清文件保存在哪、叫什么、怎么打开
- 一个软件一个子目录，目录内必须自带 `README.md`
- 新项目优先 `python scripts/new_project.py --name <name> --template <模板>`
- 不提交密钥、构建产物；CI 保持绿；改完给出运行方式与产物路径
- **交付必做备份**：`python scripts/omni.py publish --name <名> --artifact <产物>`，每次新增独立 Release、历史全部保留、永不覆盖，详见 `BACKUP_GUIDE.md`
- 文档用中文，文件 UTF-8 / LF

详细目录约定、编码规范、Android/Gradle 版本、CI 规则、禁止项见 `AGENTS.md`；跨 AI 使用说明见 `AI_GUIDE.md`；备份系统见 `BACKUP_GUIDE.md`。
