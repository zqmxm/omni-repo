"""omni-repo 共享库：GitHub REST API 封装 + 版本管理。

只用 Python 标准库，不依赖任何第三方包或 AI 平台插件。
供 omni.py（CLI）和 mcp_server.py（MCP）共同使用。
"""
from __future__ import annotations

import base64
import datetime as _dt
import json
import os
import re
import subprocess
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
API = "https://api.github.com"
CONFIG_DIR = Path.home() / ".omni-repo"
CONFIG_FILE = CONFIG_DIR / "config.json"
VERSION_RE = re.compile(r"^(\d+)\.(\d+)\.(\d+)$")
# 软件名用软件本名（中文即可）。只排除空白与 git ref 非法字符，其余一律允许。
NAME_RE = re.compile(r"^[^\s/\\~^:?*\[\]@{}][^\s/\\~^:?*\[\]@{}]{0,63}$")
PLATFORM_DIRS = [
    "python", "web", "node", "desktop", "android",
    "go", "rust", "java", "flutter", "miniapp",
]
# 交付铁律：成品只接受 exe/apk；项目目录必须有真实源码文件
DELIVERY_ARTIFACT_SUFFIXES = {".exe", ".apk"}
SOURCE_SUFFIXES = {
    ".py", ".js", ".ts", ".jsx", ".tsx", ".java", ".kt", ".kts",
    ".go", ".rs", ".c", ".cpp", ".cc", ".h", ".hpp", ".cs",
    ".html", ".htm", ".css", ".vue", ".dart", ".sh", ".bat", ".ps1",
}
SKIP_DIRS = {"build", "dist", "__pycache__", ".git", "node_modules"}


class OmniError(RuntimeError):
    """面向用户的可读错误。"""


# ---------- 配置 ----------

def get_token(cli_token: str | None = None) -> str:
    """Token 读取顺序：命令行参数 > 环境变量 > 本机配置文件。"""
    token = cli_token or os.environ.get("GITHUB_TOKEN") or ""
    if not token and CONFIG_FILE.exists():
        try:
            token = json.loads(CONFIG_FILE.read_text(encoding="utf-8")).get("token", "")
        except (json.JSONDecodeError, OSError):
            token = ""
    if not token:
        raise OmniError(
            "未找到 GitHub Token。请先运行：\n"
            "  python scripts/omni.py auth --token <你的PAT>\n"
            "或设置环境变量 GITHUB_TOKEN。"
        )
    return token.strip()


def save_token(token: str) -> Path:
    CONFIG_DIR.mkdir(parents=True, exist_ok=True)
    CONFIG_FILE.write_text(
        json.dumps({"token": token.strip()}, ensure_ascii=False, indent=2),
        encoding="utf-8",
    )
    try:
        CONFIG_FILE.chmod(0o600)
    except OSError:
        pass
    return CONFIG_FILE


def get_repo() -> tuple[str, str]:
    """从 git remote origin 解析 owner/repo。"""
    try:
        url = subprocess.check_output(
            ["git", "-C", str(ROOT), "remote", "get-url", "origin"],
            text=True, stderr=subprocess.DEVNULL,
        ).strip()
    except subprocess.CalledProcessError as exc:
        raise OmniError("仓库没有配置 origin 远程地址") from exc
    m = re.search(r"github\.com[:/]([^/]+)/([^/.]+)(?:\.git)?$", url)
    if not m:
        raise OmniError(f"无法从 origin 解析 GitHub 仓库：{url}")
    return m.group(1), m.group(2)


# ---------- HTTP ----------

def api(method: str, path: str, token: str, data: dict | None = None,
        raw: bytes | None = None, host: str = API,
        content_type: str = "application/json", timeout: int = 90):
    """调用 GitHub API。返回 (json数据, 响应头)。raw 为二进制上传。"""
    url = host.rstrip("/") + path
    headers = {
        "Authorization": "Bearer " + token,
        "Accept": "application/vnd.github+json",
        "User-Agent": "omni-repo-backup",
        "X-GitHub-Api-Version": "2022-11-28",
    }
    body = None
    if raw is not None:
        body = raw
        headers["Content-Type"] = content_type
        headers["Content-Length"] = str(len(raw))
    elif data is not None:
        body = json.dumps(data, ensure_ascii=False).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            payload = resp.read()
            try:
                parsed = json.loads(payload) if payload else {}
            except json.JSONDecodeError:
                parsed = payload
            return parsed, dict(resp.headers)
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", "replace")[:500]
        raise OmniError(f"GitHub API {method} {path} 失败：HTTP {exc.code}\n{detail}") from exc
    except urllib.error.URLError as exc:
        raise OmniError(f"无法连接 GitHub（{host}）：{exc.reason}") from exc


def get_ref(sha_or_ref: str, token: str) -> str:
    owner, repo = get_repo()
    data, _ = api("GET", f"/repos/{owner}/{repo}/{sha_or_ref}", token)
    return data["object"]["sha"]


# ---------- 版本 ----------

def find_project(name: str) -> Path:
    if not NAME_RE.fullmatch(name):
        raise OmniError(f"软件名不合法：{name}（用软件本名即可，支持中文；不能含空格或 / \\ : * ? 等符号）")
    candidates = [ROOT / d / name for d in PLATFORM_DIRS]
    candidates += [ROOT / name]
    for p in candidates:
        if p.is_dir():
            return p
    raise OmniError(
        f"找不到项目目录：{name}。请放在平台目录下，例如 desktop/{name}/ 或 android/{name}/，"
        "或用 --project-dir 指定。"
    )


def read_version(project: Path) -> str:
    vf = project / "VERSION"
    if vf.exists():
        v = vf.read_text(encoding="utf-8").strip()
        if VERSION_RE.fullmatch(v):
            return v
    return "0.0.0"


def write_version(project: Path, version: str) -> None:
    if not VERSION_RE.fullmatch(version):
        raise OmniError(f"版本号格式应为 X.Y.Z：{version}")
    (project / "VERSION").write_text(version + "\n", encoding="utf-8", newline="\n")


def bump_version(current: str, bump: str = "patch") -> str:
    major, minor, patch = (int(x) for x in current.split("."))
    if bump == "major":
        major, minor, patch = major + 1, 0, 0
    elif bump == "minor":
        minor, patch = minor + 1, 0
    else:
        patch += 1
    return f"{major}.{minor}.{patch}"


def timestamp_tag() -> str:
    return _dt.datetime.now().strftime("%Y%m%d-%H%M")


def platform_label(path: Path) -> str:
    suffix = path.suffix.lower()
    return {
        ".exe": "windows-x64", ".msi": "windows-x64",
        ".apk": "android", ".aab": "android",
        ".dmg": "macos", ".zip": "bundle", ".tar.gz": "bundle",
    }.get(suffix, suffix.lstrip(".") or "bin")


def ensure_delivery(name: str, artifact: Path, project: Path) -> None:
    """强制交付铁律：源码 + exe/apk 成品缺一不可，否则拒绝备份。

    这是 `AGENTS.md`「交付铁律」的可执行版本，任何 AI 都不能绕过：
    - 产物必须是真实存在的 .exe 或 .apk
    - 项目目录必须存在、有 README.md，且含至少一个真实源码文件
    """
    suffix = artifact.suffix.lower()
    if suffix not in DELIVERY_ARTIFACT_SUFFIXES:
        raise OmniError(
            f"交付铁律：成品必须是 .exe 或 .apk，当前是「{suffix or '无后缀'}」：{artifact.name}。\n"
            "Windows 软件请用 PyInstaller 打成 exe；Android 软件请用 Gradle 打出 apk。\n"
            "不接受源码包、脚本、网页、构建命令当成品。详见 AGENTS.md「交付铁律」。"
        )
    if not artifact.is_file():
        raise OmniError(f"交付铁律：成品文件不存在：{artifact}")
    if not project.is_dir():
        raise OmniError(
            f"交付铁律：找不到源码目录：{project}。请在平台目录下用软件本名建项目目录，"
            "或用 --project-dir 指定。"
        )
    if not (project / "README.md").is_file():
        raise OmniError(f"交付铁律：项目目录缺少 README.md：{project}")
    source_files = [
        p for p in project.rglob("*")
        if p.is_file()
        and not SKIP_DIRS.intersection(p.relative_to(project).parts)
        and p.suffix.lower() in SOURCE_SUFFIXES
    ]
    if not source_files:
        raise OmniError(
            f"交付铁律：项目目录里没有源码文件：{project}。\n"
            "只有成品没有源码不算交付，请先把实现代码放进项目目录再备份。"
        )


# ---------- 源码同步（Git Data API，兼容代理环境） ----------

def sync_source(token: str, message: str) -> str:
    """把工作区所有源码同步到远程 main，返回新 commit sha。

    通过 REST API 创建 blob/tree/commit 并更新 ref，绕过 git push 的 CONNECT 隧道问题。
    """
    owner, repo = get_repo()

    # 1) 暂存全部变更，得到本地 tree（含所有 blob 哈希）
    subprocess.run(["git", "-C", str(ROOT), "add", "-A"], check=True)
    tree_sha = subprocess.check_output(
        ["git", "-C", str(ROOT), "write-tree"], text=True).strip()
    entries = []
    # -c core.quotepath=false：确保中文路径按 UTF-8 原样输出，
    # 否则 git 会把中文转成 "\351\200\217" 八进制转义，导致远程 tree 路径错乱。
    ls_out = subprocess.check_output(
        ["git", "-C", str(ROOT), "-c", "core.quotepath=false",
         "ls-tree", "-r", "-z", tree_sha]
    ).decode("utf-8")
    for line in ls_out.split("\0"):
        if not line:
            continue
        meta, path = line.split("\t", 1)
        mode, typ, sha = meta.split()
        entries.append({"path": path, "mode": mode, "type": typ, "sha": sha})

    # 2) 远程当前状态
    main_sha = get_ref("git/ref/heads/main", token)
    base_commit, _ = api("GET", f"/repos/{owner}/{repo}/git/commits/{main_sha}", token)
    base_tree = base_commit["tree"]["sha"]

    # 3) 缺失的 blob 从本地上传
    remote_tree, _ = api("GET", f"/repos/{owner}/{repo}/git/trees/{base_tree}?recursive=1", token)
    remote_blobs = {i["sha"] for i in remote_tree.get("tree", []) if i["type"] == "blob"}
    for e in entries:
        if e["type"] != "blob" or e["sha"] in remote_blobs:
            continue
        raw = subprocess.check_output(["git", "-C", str(ROOT), "cat-file", "blob", e["sha"]])
        api("POST", f"/repos/{owner}/{repo}/git/blobs", token, data={
            "content": base64.b64encode(raw).decode(),
            "encoding": "base64",
        })
    if any(e["sha"] not in remote_blobs for e in entries if e["type"] == "blob"):
        # 重新拉取一次，确认新 blob 已就绪
        remote_tree, _ = api("GET", f"/repos/{owner}/{repo}/git/trees/{base_tree}?recursive=1", token)
        remote_blobs = {i["sha"] for i in remote_tree.get("tree", []) if i["type"] == "blob"}

    # 4) 建 tree（用本地 tree 的精确条目，保证内容一致）
    tree_data, _ = api("POST", f"/repos/{owner}/{repo}/git/trees", token, data={
        "tree": entries,
    })

    # 5) 建 commit（沿用本地 git 身份）
    def git_cfg(key: str, default: str) -> str:
        try:
            return subprocess.check_output(
                ["git", "-C", str(ROOT), "config", key], text=True,
                stderr=subprocess.DEVNULL).strip()
        except subprocess.CalledProcessError:
            return default

    author_name = git_cfg("user.name", "omni-repo")
    author_email = git_cfg("user.email", "omni-repo@users.noreply.github.com")
    now = _dt.datetime.now(_dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    commit_data, _ = api("POST", f"/repos/{owner}/{repo}/git/commits", token, data={
        "message": message,
        "tree": tree_data["sha"],
        "parents": [main_sha],
        "author": {"name": author_name, "email": author_email, "date": now},
        "committer": {"name": author_name, "email": author_email, "date": now},
    })

    # 6) 更新 ref
    api("PATCH", f"/repos/{owner}/{repo}/git/refs/heads/main", token, data={
        "sha": commit_data["sha"], "force": False,
    })

    # 7) 本地对齐：用同样的 tree 建本地 commit，并同步 origin/main 引用
    try:
        local_commit = subprocess.check_output(
            ["git", "-C", str(ROOT), "commit-tree", tree_sha, "-p",
             subprocess.check_output(["git", "-C", str(ROOT), "rev-parse", "HEAD"], text=True).strip()],
            input=message.encode(), stderr=subprocess.DEVNULL).decode().strip()
        subprocess.run(["git", "-C", str(ROOT), "update-ref", "HEAD", local_commit], check=True)
        subprocess.run(["git", "-C", str(ROOT), "update-ref",
                        "refs/remotes/origin/main", local_commit], check=True)
    except (subprocess.CalledProcessError, OSError):
        pass  # 本地对齐失败不影响远程备份
    return commit_data["sha"]


# ---------- Release ----------

def find_release_by_tag(token: str, tag: str):
    """按 tag 查找 Release；不存在返回 None。"""
    owner, repo = get_repo()
    safe = urllib.parse.quote(tag, safe="")
    try:
        data, _ = api("GET", f"/repos/{owner}/{repo}/releases/tags/{safe}", token)
        return data
    except OmniError as exc:
        if "HTTP 404" in str(exc):
            return None
        raise


def delete_release_assets(token: str, release: dict) -> None:
    """删除某个 Release 下的所有旧附件。"""
    owner, repo = get_repo()
    for asset in release.get("assets", []):
        api("DELETE", f"/repos/{owner}/{repo}/releases/assets/{asset['id']}", token)


def upsert_release(token: str, tag: str, name: str, body: str) -> dict:
    """【v1 遗留，已停用】覆盖式更新固定 Release。v2 publish 不再调用。"""
    owner, repo = get_repo()
    existing = find_release_by_tag(token, tag)
    if existing:
        api("PATCH", f"/repos/{owner}/{repo}/releases/{existing['id']}", token, data={
            "name": name, "body": body, "draft": False, "prerelease": False,
        })
        delete_release_assets(token, existing)
        return existing
    return create_release(token, tag, name, body)


def delete_old_releases(token: str, name: str, keep_tag: str) -> int:
    """【v1 遗留，已停用】v2 永不覆盖，publish 不再调用；保留仅为兼容旧脚本。"""
    owner, repo = get_repo()
    count = 0
    for r in list_releases(token):
        tag = r["tag_name"]
        if tag == keep_tag:
            continue
        if tag == name + "-latest" or tag.startswith(name + "-v"):
            api("DELETE", f"/repos/{owner}/{repo}/releases/{r['id']}", token)
            count += 1
    return count


def create_release(token: str, tag: str, name: str, body: str) -> dict:
    owner, repo = get_repo()
    data, _ = api("POST", f"/repos/{owner}/{repo}/releases", token, data={
        "tag_name": tag, "name": name, "body": body,
        "draft": False, "prerelease": False,
    })
    return data


def _ascii_name(name: str) -> str:
    """软件本名 -> ASCII 文件名。GitHub 上传接口会剥离中文 name，
    故实际文件名用拼音/ASCII，显示名用中文 label。内置常见名映射，
    其他名字用 'app' 兜底（AI 可在 build-manifest 里加 "ascii_name" 覆盖）。"""
    builtin = {
        "透明橡皮擦": "toumingxiangpicao",
    }
    if name in builtin:
        return builtin[name]
    if name.isascii():
        return name
    # 尝试从 build-manifest.json 读 ascii_name
    try:
        mf = json.loads((ROOT / "build-manifest.json").read_text(encoding="utf-8"))
        for app in mf.get("apps", []):
            if app.get("name") == name and app.get("ascii_name"):
                return app["ascii_name"]
    except (OSError, json.JSONDecodeError):
        pass
    return "app"


def upload_asset(token: str, upload_url: str, file_path: Path, display_name: str,
                 file_name: str | None = None) -> dict:
    """上传资产：文件名用 ASCII（GitHub 限制），显示名 label 用中文软件本名。"""
    if not file_path.is_file():
        raise OmniError(f"产物文件不存在：{file_path}")
    name = file_name or file_path.name
    if not name.isascii():
        name = _ascii_name(display_name) + file_path.suffix
    url = upload_url.split("{")[0] + "?name=" + urllib.parse.quote(name)
    data, _ = api(
        "POST", "", token, raw=file_path.read_bytes(),
        host=url, content_type="application/octet-stream",
    )
    # 设置中文显示名（label 字段保留中文，GitHub 界面优先显示）
    asset_id = data["id"]
    owner, repo = get_repo()
    api("PATCH", f"/repos/{owner}/{repo}/releases/assets/{asset_id}", token, data={
        "label": display_name,
    })
    data["label"] = display_name
    return data


def make_source_zip(project: Path, out_dir: Path, app_name: str) -> Path:
    """把软件项目目录打包成源码 zip（排除构建产物），返回 zip 路径。
    本地文件名用中文（如「透明橡皮擦源码.zip」），上传到 GitHub 时才转 ASCII。"""
    import zipfile
    out_dir.mkdir(parents=True, exist_ok=True)
    target = out_dir / f"{app_name}源码.zip"
    skip_dirs = {"build", "dist", "__pycache__", ".git", "node_modules"}
    with zipfile.ZipFile(target, "w", zipfile.ZIP_DEFLATED) as zf:
        for root, dirs, files in os.walk(project):
            dirs[:] = [d for d in dirs if d not in skip_dirs]
            for fn in files:
                if fn.endswith((".pyc", ".exe", ".spec")):
                    continue
                full = Path(root) / fn
                zf.write(full, full.relative_to(project))
    return target


def list_releases(token: str, name: str | None = None) -> list[dict]:
    owner, repo = get_repo()
    out, page = [], 1
    while True:
        data, _ = api("GET", f"/repos/{owner}/{repo}/releases?per_page=100&page={page}", token)
        if not data:
            break
        out.extend(data)
        if len(data) < 100:
            break
        page += 1
    if name:
        out = [r for r in out
               if r["tag_name"].startswith(name + "-")]
    return out


def download_asset(token: str, tag: str, dest: Path, asset_name: str | None = None) -> Path:
    owner, repo = get_repo()
    safe_tag = urllib.parse.quote(tag, safe="")
    releases, _ = api("GET", f"/repos/{owner}/{repo}/releases/tags/{safe_tag}", token)
    assets = releases.get("assets", [])
    if not assets:
        raise OmniError(f"Release {tag} 没有可下载资产")
    if asset_name:
        assets = [a for a in assets if a["name"] == asset_name] or assets
    else:
        # 默认优先选可执行文件（用户最常要的），其次才选源码包
        exe_kinds = (".exe", ".apk", ".msi", ".dmg", ".appimage")
        exes = [a for a in assets if a["name"].lower().endswith(exe_kinds)]
        if exes:
            assets = exes
    chosen = assets[0]
    dest.mkdir(parents=True, exist_ok=True)
    # 下载到本地用中文名：优先用中文显示名(label) + Release 版本，
    # 让用户保存下来的文件一眼就知道是什么。GitHub 上存的 ASCII 名不暴露给用户。
    m = re.search(r"-v(\d+\.\d+\.\d+)", tag)
    if not m:
        m = re.search(r"v(\d+\.\d+\.\d+)", releases.get("name", ""))
    ver = m.group(1) if m else ""
    label = chosen.get("label") or ""
    suffix = Path(chosen["name"]).suffix or ""
    if label:
        version_part = f"v{ver}" if ver else ""
        local_name = f"{label} {version_part}{suffix}".strip()
    else:
        local_name = chosen["name"]
    target = dest / local_name
    url = f"{API}/repos/{owner}/{repo}/releases/assets/{chosen['id']}"
    total = int(chosen.get("size", 0))
    chunk_size = 2 * 1024 * 1024  # 每块 2MB，逐块请求避免单次超时

    # 若已有部分文件，从已下载长度续传（断点续传）
    start = target.stat().st_size if target.exists() else 0
    if total and start >= total:
        return target

    mode = "ab" if start else "wb"
    with open(target, mode) as f:
        while True:
            end = start + chunk_size - 1
            if total:
                end = min(end, total - 1)
            req = urllib.request.Request(url, headers={
                "Authorization": "Bearer " + token,
                "Accept": "application/octet-stream",
                "User-Agent": "omni-repo-backup",
                "Range": f"bytes={start}-{end}",
            })
            try:
                with urllib.request.urlopen(req, timeout=60) as resp:
                    data = resp.read()
            except urllib.error.HTTPError as exc:
                if exc.code == 416:  # 请求范围超出，视为完成
                    break
                raise OmniError(f"下载失败：HTTP {exc.code}") from exc
            except urllib.error.URLError as exc:
                raise OmniError(f"下载中断（已保存 {f.tell()} 字节，可重跑续传）：{exc.reason}") from exc
            if not data:
                break
            f.write(data)
            f.flush()
            start += len(data)
            if total and start >= total:
                break
            if not total and len(data) < chunk_size:
                break
    return target


def publish(token: str, name: str, artifact: Path, bump: str = "patch",
            version: str | None = None, project_dir: Path | None = None) -> dict:
    """完整备份流程：源码 + 版本 + exe/apk 产物，一次完成。"""
    project = project_dir or find_project(name)
    ensure_delivery(name, artifact, project)  # 交付铁律：缺源码或成品不是 exe/apk 就拒绝
    current = read_version(project)
    new_version = version or bump_version(current, bump)
    write_version(project, new_version)

    # v2 永不覆盖：每次 publish 都生成一个全局唯一 tag，历史 Release 全保留
    stamp = _dt.datetime.now(_dt.timezone.utc).strftime("%Y%m%d-%H%M%S")
    uniq = os.urandom(3).hex()
    tag = f"{name}-v{new_version}-{stamp}-{uniq}"
    ascii_name = _ascii_name(name)

    commit_sha = sync_source(token, f"backup({name}): v{new_version} 源码与构建脚本")

    release = create_release(
        token, tag, f"{name} v{new_version}",
        f"## {name} v{new_version}\n\n"
        f"- 源码提交：`{commit_sha[:7]}`\n"
        f"- 生成时间：{_dt.datetime.now().isoformat(timespec='seconds')}\n"
        f"- **v2 永不覆盖**：本次是独立新 Release（{tag}），历史 Release / tag / 附件全部保留\n"
        f"- 可用 `python scripts/omni.py download --tag {tag}` 单独取回\n",
    )

    # 1) 可执行文件：文件名用 ASCII，显示名用软件本名
    exe_file = f"{ascii_name}-{platform_label(artifact)}{artifact.suffix}"
    asset = upload_asset(token, release["upload_url"], artifact, name, exe_file)

    deleted = 0  # v2：永不删除任何历史 Release

    # 2) 源码压缩包：显示名「<软件本名>源码」
    source_label = f"{name}源码"
    source_zip = make_source_zip(project, project / "dist", name)
    try:
        src_asset = upload_asset(
            token, release["upload_url"], source_zip, source_label,
            f"{ascii_name}-source.zip",
        )
    except OmniError:
        src_asset = None  # 源码包上传失败不影响主产物

    return {
        "name": name, "version": new_version, "tag": tag,
        "commit": commit_sha, "release_url": release["html_url"],
        "asset": asset["name"], "asset_label": asset.get("label", name),
        "asset_url": asset["browser_download_url"], "size": asset["size"],
        "source_asset": src_asset["name"] if src_asset else None,
        "deleted_old_releases": deleted,  # v2 恒为 0
        "history_preserved": True,
    }
