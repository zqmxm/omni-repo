# GitHub Copilot 指令

本仓库的唯一权威规则是根目录 **`AGENTS.md`**，请先完整阅读再生成代码；跨 AI 使用说明见 `AI_GUIDE.md`。

速记：
- ⭐ 命名铁律：软件叫什么名字，GitHub 上就用什么名字（中文软件用中文名）——目录名、README 标题、exe/apk 文件名、Release tag、workflow 文件名、备份 `--name` 全部用软件本名，禁止英文代号/拼音缩写。例：透明橡皮擦 → `desktop/透明橡皮擦/`、`透明橡皮擦.exe`、`透明橡皮擦.yml`
- ⭐⭐ 交付铁律（最高优先级）：**源码 + exe/apk 成品缺一不可**。源码提交进仓库，exe/apk 上传到 Release；禁止只交代码/命令/网页/源码包。`omni.py publish` 会强制校验产物必须是 `.exe` 或 `.apk` 且项目有源码，不满足直接报错
- ⭐ 全中文可读：能用中文的都用中文；每个文件夹都要有中文 README 说明「干什么、每个文件是什么」；新增/改名后必须同步更新根目录「仓库地图.md」，让所有者找得到文件。回复用中文，说清文件保存在哪、叫什么、怎么打开
- 一个软件一个子目录，目录内必须自带 `README.md`（功能 / 运行 / 测试 / 出包）
- 新项目用 `python scripts/new_project.py --name <name> --template <模板>`
- Python 3.10+ / Node 20 / Android: JDK 17 + Gradle 8.7 + AGP 8.5.2 + compileSdk 34
- UTF-8 + LF，文档用中文
- 不提交密钥、keystore、*.exe、*.apk、node_modules、build 产物
- 不引入绑定特定 AI 平台的私有依赖
- 新增 workflow 要带路径过滤 + `workflow_dispatch`
- **做完软件必须备份**：`python scripts/omni.py publish --name <名> --artifact <产物>`，每个软件只保留一个 Release（软件名-latest），更新时覆盖，不保留旧版本，详见 `BACKUP_GUIDE.md`

交付时说明改了哪些文件、怎么运行、怎么测试、Release 备份链接在哪、有什么边界。
