#!/usr/bin/env python3
"""omni-repo 备份总控 CLI。

任何 AI 做完软件后，只需一条命令即可把源码和 exe/apk 成品备份到 GitHub，
且每个版本独立保留、永不覆盖。

交付铁律（强制）：源码 + exe/apk 缺一不可。--artifact 只接受 .exe 或 .apk，
项目目录必须有真实源码；否则 publish 直接报错，不许只交一半。

用法示例：
  python scripts/omni.py auth --token ghp_xxx
  python scripts/omni.py publish --name 透明橡皮擦 --artifact desktop/透明橡皮擦/dist/透明橡皮擦.exe
  python scripts/omni.py list
  python scripts/omni.py download --tag 透明橡皮擦-v1.0.1-20261126-0130
  python scripts/omni.py version --name 透明橡皮擦

命名铁律：--name 传软件本名（软件叫什么就传什么，中文照传），仓库目录、
Release tag、产物文件名全部用这个名字，不用英文代号。
"""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import omni_lib as lib


def cmd_auth(args):
    path = lib.save_token(args.token)
    print(f"Token 已保存到本机：{path}")
    print("该文件在仓库外，不会入库。之后 publish/list/download 无需再登录。")


def cmd_publish(args):
    token = lib.get_token(args.token)
    artifact = Path(args.artifact).expanduser().resolve()
    project = Path(args.project_dir).expanduser().resolve() if args.project_dir else None
    result = lib.publish(
        token, args.name, artifact, bump=args.bump,
        version=args.version, project_dir=project,
    )
    print(json.dumps(result, ensure_ascii=False, indent=2))
    print("\n备份完成：源码已入库，产物已上传到 Release，旧版本未被覆盖。")


def cmd_list(args):
    token = lib.get_token(args.token)
    releases = lib.list_releases(token, args.name)
    if not releases:
        print("没有找到备份版本。")
        return
    print(f"共 {len(releases)} 个备份版本：\n")
    for r in releases:
        assets = ", ".join(a["name"] for a in r.get("assets", [])) or "（无产物）"
        print(f"- {r['tag_name']}")
        print(f"  {r['html_url']}")
        print(f"  产物：{assets}")


def cmd_download(args):
    token = lib.get_token(args.token)
    dest = Path(args.output).expanduser().resolve() if args.output else Path.cwd() / "downloads"
    path = lib.download_asset(token, args.tag, dest, args.asset)
    print(f"已下载：{path}")


def cmd_version(args):
    project = lib.find_project(args.name)
    print(f"{args.name}: v{lib.read_version(project)}")


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="omni.py", description="omni-repo：任何 AI 都能用的 GitHub 自动备份工具")
    sub = p.add_subparsers(dest="command", required=True)

    a = sub.add_parser("auth", help="保存 GitHub Token 到本机（一次配置，以后免登录）")
    a.add_argument("--token", required=True, help="有 repo 权限的 Personal Access Token")
    a.set_defaults(func=cmd_auth)

    b = sub.add_parser("publish", help="备份源码 + exe/apk 成品到新 Release（强制校验，不覆盖旧版本）")
    b.add_argument("--name", required=True, help="软件本名（中文软件就传中文），对应平台目录下的同名子目录")
    b.add_argument("--artifact", required=True, help="成品文件路径：必须是 .exe 或 .apk（源码包/脚本/网页不被接受）")
    b.add_argument("--project-dir", help="项目目录（默认自动在平台目录下查找）")
    b.add_argument("--version", help="指定版本号 X.Y.Z（默认自动递增 patch）")
    b.add_argument("--bump", choices=["major", "minor", "patch"], default="patch",
                   help="版本递增方式，默认 patch")
    b.add_argument("--token", help="临时 Token（也可存本机或设环境变量）")
    b.set_defaults(func=cmd_publish)

    c = sub.add_parser("list", help="列出所有备份版本")
    c.add_argument("--name", help="只列出某个软件的备份")
    c.add_argument("--token")
    c.set_defaults(func=cmd_list)

    d = sub.add_parser("download", help="从 Release 下载产物")
    d.add_argument("--tag", required=True, help="Release tag")
    d.add_argument("--asset", help="指定资产文件名（默认取第一个）")
    d.add_argument("--output", help="输出目录，默认 ./downloads")
    d.add_argument("--token")
    d.set_defaults(func=cmd_download)

    v = sub.add_parser("version", help="查看软件当前版本号")
    v.add_argument("--name", required=True)
    v.set_defaults(func=cmd_version)

    return p


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    try:
        args.func(args)
    except lib.OmniError as exc:
        print(f"错误：{exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
