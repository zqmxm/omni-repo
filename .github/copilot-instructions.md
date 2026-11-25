# GitHub Copilot 指令

本仓库的唯一权威规则是根目录 **`AGENTS.md`**，请先完整阅读再生成代码；跨 AI 使用说明见 `AI_GUIDE.md`。

速记：
- 一个软件一个子目录，目录内必须自带 `README.md`（功能 / 运行 / 测试 / 出包）
- 新项目用 `python scripts/new_project.py --name <name> --template <模板>`
- Python 3.10+ / Node 20 / Android: JDK 17 + Gradle 8.7 + AGP 8.5.2 + compileSdk 34
- UTF-8 + LF，文档用中文
- 不提交密钥、keystore、*.exe、*.apk、node_modules、build 产物
- 不引入绑定特定 AI 平台的私有依赖
- 新增 workflow 要带路径过滤 + `workflow_dispatch`
- **做完软件必须备份**：`python scripts/omni.py publish --name <名> --artifact <产物>`，每个版本独立 Release 不覆盖旧版本，详见 `BACKUP_GUIDE.md`

交付时说明改了哪些文件、怎么运行、怎么测试、Release 备份链接在哪、有什么边界。
