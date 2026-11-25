# CLAUDE.md

本项目规则以根目录 **`AGENTS.md`** 为唯一权威，请先完整阅读再动手。

要点速记：
- ⭐ 命名铁律：软件叫什么名字，GitHub 上就用什么名字（中文软件用中文名）——目录名、README 标题、exe/apk 文件名、Release tag、workflow 文件名、备份 `--name` 全部用软件本名，禁止英文代号/拼音缩写。例：透明橡皮擦 → `desktop/透明橡皮擦/`、`透明橡皮擦.exe`、`透明橡皮擦.yml`
- 一个软件一个子目录，目录内必须自带 `README.md`
- 新项目优先 `python scripts/new_project.py --name <name> --template <模板>`
- 不提交密钥、构建产物；CI 保持绿；改完给出运行方式与产物路径
- **交付必做备份**：`python scripts/omni.py publish --name <名> --artifact <产物>`，每个版本独立 Release 不覆盖旧版本，详见 `BACKUP_GUIDE.md`
- 文档用中文，文件 UTF-8 / LF

详细目录约定、编码规范、Android/Gradle 版本、CI 规则、禁止项见 `AGENTS.md`；跨 AI 使用说明见 `AI_GUIDE.md`；备份系统见 `BACKUP_GUIDE.md`。
