"""在仓库根目录生成一个新项目子目录。

用法：
  python scripts/new_project.py --name my-tool --template python
  python scripts/new_project.py --name my-app  --template android
  python scripts/new_project.py --name my-page --template web
  python scripts/new_project.py --name my-svc  --template node
  python scripts/new_project.py --name my-exe  --template desktop
  python scripts/new_project.py --name whatever            # 空目录 + README 占位
  python scripts/new_project.py --name x --force           # 覆盖已存在目录
"""
from __future__ import annotations

import argparse
import re
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
TEMPLATES = {
    "python": "python",
    "web": "web",
    "node": "node",
    "android": "android",
    "desktop": "desktop",
}
# 软件名用本名（中文即可），只排除空白与系统/路径保留字符
NAME_RE = re.compile(r"^[^\s/\\~^:?*\[\]@{}][^\s/\\~^:?*\[\]@{}]{0,63}$")
SKIP_PARTS = {"__pycache__", "node_modules", ".git", "build", "dist", ".gradle"}
SKIP_SUFFIXES = {".pyc", ".apk", ".exe", ".jks", ".keystore"}


def copy_tree(src: Path, dst: Path) -> None:
    """纯文件内容复制，不触碰权限/时间戳元数据，受限文件系统也能用。"""
    dst.mkdir(parents=True, exist_ok=True)
    for p in src.rglob("*"):
        rel = p.relative_to(src)
        if SKIP_PARTS.intersection(rel.parts) or p.suffix in SKIP_SUFFIXES:
            continue
        target = dst / rel
        if p.is_dir():
            target.mkdir(parents=True, exist_ok=True)
        else:
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(p, target)


def render_readme(name: str, template: str, has_original: bool) -> str:
    lines = [
        f"# {name}",
        "",
        f"由 `{template}` 模板生成，按需修改。",
        "",
        "## 这个软件是干什么的",
        "",
        "（用大白话写清楚：它能帮用户做什么）",
        "",
        "## 每个文件是什么",
        "",
        "（用中文列出来：哪个文件干什么，让所有者一眼看懂）",
        "",
        "## 生成的文件在哪里",
        "",
        "（写清楚成品在哪个文件夹、叫什么名字、怎么打开）",
        "",
        "## 交付清单（必须留下，缺一不可）",
        "",
        "- [ ] 源码：本目录里已有真实实现代码（不只是 README）",
        "- [ ] 成品：已产出 `*.exe`（Windows）或 `*.apk`（Android），并随备份上传到 Release",
        "- [ ] 备份：已运行 `python scripts/omni.py publish --name <软件本名> --artifact <exe/apk路径>`",
        "- [ ] 回复：已用中文告知源码位置和 exe/apk 下载链接",
        "",
        "> 规则来源：仓库根目录 `AGENTS.md` 的「交付铁律」。只交代码或只交成品都算没做完。",
        "",
        "## 运行",
        "",
        "（写清楚：怎么跑、依赖、怎么测试、怎么出包）",
        "",
    ]
    if has_original:
        lines += [
            "## 参考",
            "",
            f"- 模板说明：仓库 `{TEMPLATES[template]}/README.md`",
            "- 全局规范：仓库根目录 `AGENTS.md`",
            "",
        ]
    return "\n".join(lines)


def main() -> int:
    ap = argparse.ArgumentParser(description="生成一个新项目骨架")
    ap.add_argument("--name", required=True, help="软件本名（中文软件就传中文，目录用它命名）")
    ap.add_argument("--template", choices=[*TEMPLATES, "blank"], default="blank")
    ap.add_argument("--force", action="store_true", help="目录已存在时覆盖")
    args = ap.parse_args()

    if not NAME_RE.fullmatch(args.name):
        ap.error("目录名只能用字母/数字/点/下划线/连字符，且以字母或数字开头")
    if args.name in TEMPLATES.values() or args.name in {"scripts", "data", ".github"}:
        ap.error("不能覆盖仓库自带的模板/规范目录")

    dst = ROOT / args.name
    if dst.exists():
        if not args.force:
            ap.error(f"目录已存在：{dst}（要覆盖请加 --force）")
        shutil.rmtree(dst)

    if args.template == "blank":
        dst.mkdir()
        (dst / "README.md").write_text(render_readme(args.name, "blank", False), encoding="utf-8")
    else:
        src = ROOT / TEMPLATES[args.template]
        copy_tree(src, dst)
        (dst / "README.md").write_text(
            render_readme(args.name, args.template, True), encoding="utf-8"
        )

    print(f"已生成 {dst.relative_to(ROOT)}/（模板：{args.template}）")
    print("提醒（交付铁律）：完成后必须同时留下源码 + exe/apk 成品，并用 omni.py publish 备份；只交代码不算完成。")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
