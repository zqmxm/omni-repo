#!/usr/bin/env python3
"""omni-repo MCP Server — 让支持 MCP 的 AI 在对话框里直接备份/下载 exe。

基于 MCP stdio 协议，仅用 Python 标准库实现，不依赖任何第三方包。

在 AI 工具（Cursor / Claude Desktop / WorkBuddy 等）的 MCP 配置中添加：
{
  "mcpServers": {
    "omni-repo": {
      "command": "python",
      "args": ["<仓库绝对路径>/scripts/mcp_server.py"],
      "env": {"GITHUB_TOKEN": "<你的PAT>"}
    }
  }
}

提供的工具：
  omni_publish  - 备份源码并把 exe/apk 上传到新 Release（不覆盖）
  omni_list     - 列出所有备份版本
  omni_download - 下载某个版本的产物到本地
  omni_version  - 查看软件当前版本
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import omni_lib as lib

TOOLS = [
    {
        "name": "omni_publish",
        "description": "把 AI 做好的软件备份到 GitHub：源码提交 + 产物上传到独立 Release，每个版本不覆盖旧版本。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "name": {"type": "string", "description": "软件本名（软件叫什么就传什么，中文照传；对应平台目录下的同名子目录）"},
                "artifact": {"type": "string", "description": "产物文件绝对路径（exe/apk/zip，文件名也用软件本名）"},
                "version": {"type": "string", "description": "可选，指定版本号 X.Y.Z，默认自动递增"},
                "project_dir": {"type": "string", "description": "可选，项目目录绝对路径"},
            },
            "required": ["name", "artifact"],
        },
    },
    {
        "name": "omni_list",
        "description": "列出 GitHub 上所有已备份的软件版本及其产物。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "name": {"type": "string", "description": "可选，只看某个软件的备份"},
            },
        },
    },
    {
        "name": "omni_download",
        "description": "从 GitHub Release 下载某个版本的 exe/apk 产物到本地。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "tag": {"type": "string", "description": "Release tag，如 透明橡皮擦-v1.0.1-20261126-0130"},
                "output": {"type": "string", "description": "可选，保存目录，默认当前目录/downloads"},
                "asset": {"type": "string", "description": "可选，指定资产文件名"},
            },
            "required": ["tag"],
        },
    },
    {
        "name": "omni_version",
        "description": "查看某个软件当前本地版本号。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "name": {"type": "string", "description": "软件名"},
            },
            "required": ["name"],
        },
    },
]


def text_result(text: str, is_error: bool = False) -> dict:
    return {
        "content": [{"type": "text", "text": text}],
        "isError": is_error,
    }


def call_tool(name: str, arguments: dict) -> dict:
    try:
        token = lib.get_token()
        if name == "omni_publish":
            project = Path(arguments["project_dir"]) if arguments.get("project_dir") else None
            result = lib.publish(
                token, arguments["name"], Path(arguments["artifact"]),
                version=arguments.get("version"), project_dir=project,
            )
            return text_result(
                f"备份完成：{result['name']} v{result['version']}\n"
                f"Release：{result['release_url']}\n"
                f"产物：{result['asset']}（{result['size']} 字节）\n"
                f"旧版本未被覆盖。"
            )
        if name == "omni_list":
            releases = lib.list_releases(token, arguments.get("name"))
            if not releases:
                return text_result("没有找到备份版本。")
            lines = [f"共 {len(releases)} 个备份版本："]
            for r in releases:
                assets = ", ".join(a["name"] for a in r.get("assets", [])) or "（无产物）"
                lines.append(f"- {r['tag_name']} | {assets}\n  {r['html_url']}")
            return text_result("\n".join(lines))
        if name == "omni_download":
            dest = Path(arguments["output"]) if arguments.get("output") else Path.cwd() / "downloads"
            path = lib.download_asset(token, arguments["tag"], dest, arguments.get("asset"))
            return text_result(f"已下载：{path}")
        if name == "omni_version":
            project = lib.find_project(arguments["name"])
            return text_result(f"{arguments['name']}: v{lib.read_version(project)}")
        return text_result(f"未知工具：{name}", is_error=True)
    except (lib.OmniError, KeyError, OSError) as exc:
        return text_result(f"错误：{exc}", is_error=True)


def handle(msg: dict) -> dict | None:
    method = msg.get("method")
    req_id = msg.get("id")
    if method == "initialize":
        return {
            "jsonrpc": "2.0", "id": req_id,
            "result": {
                "protocolVersion": "2024-11-05",
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "omni-repo", "version": "1.0.0"},
            },
        }
    if method == "notifications/initialized":
        return None
    if method == "tools/list":
        return {"jsonrpc": "2.0", "id": req_id, "result": {"tools": TOOLS}}
    if method == "tools/call":
        params = msg.get("params", {})
        return {
            "jsonrpc": "2.0", "id": req_id,
            "result": call_tool(params.get("name", ""), params.get("arguments", {})),
        }
    if method == "ping":
        return {"jsonrpc": "2.0", "id": req_id, "result": {}}
    if req_id is not None:
        return {"jsonrpc": "2.0", "id": req_id,
                "error": {"code": -32601, "message": f"未知方法：{method}"}}
    return None


def main() -> int:
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except json.JSONDecodeError:
            continue
        response = handle(msg)
        if response is not None:
            sys.stdout.write(json.dumps(response, ensure_ascii=False) + "\n")
            sys.stdout.flush()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
