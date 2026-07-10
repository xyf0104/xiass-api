#!/usr/bin/env python3
"""Validate NoWind release branding, privacy, migrations, and data persistence."""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def read(path: str) -> str:
    return (ROOT / path).read_text(encoding="utf-8")


def git_output(*args: str) -> str:
    return subprocess.check_output(
        ["git", *args], cwd=ROOT, text=True, stderr=subprocess.DEVNULL
    ).strip()


def check_version(errors: list[str]) -> None:
    version = read("backend/cmd/server/VERSION").strip()
    readme = read("README.md")
    if not re.fullmatch(r"\d+\.\d+\.\d+", version):
        errors.append(f"VERSION 格式无效: {version!r}")
        return
    if f"> 当前版本：v{version}" not in readme:
        errors.append(f"README 当前版本未同步为 v{version}")
    if f"当前版本-v{version}-" not in readme:
        errors.append(f"README 版本徽章未同步为 v{version}")

    try:
        tags = git_output("tag", "--list", "v[0-9]*", "--sort=-v:refname").splitlines()
        stable_tags = [tag for tag in tags if re.fullmatch(r"v\d+\.\d+\.\d+", tag)]
        if stable_tags and stable_tags[0].removeprefix("v") != version:
            readme_changed = subprocess.run(
                ["git", "diff", "--quiet", stable_tags[0], "--", "README.md"],
                cwd=ROOT,
                check=False,
            ).returncode != 0
            if not readme_changed:
                errors.append(f"版本升级到 v{version} 时必须同步修改 README.md")
    except (subprocess.CalledProcessError, FileNotFoundError):
        pass


def check_public_branding_and_privacy(errors: list[str]) -> None:
    forbidden_paths = [
        "README_CN.md",
        "README_JA.md",
        "assets/partners",
        "account5_update.json",
        "channel1.json",
        "channel1_update.json",
        "generate_docs.py",
        "setup_pricing.sql",
    ]
    for relative in forbidden_paths:
        if (ROOT / relative).exists():
            errors.append(f"公开仓库仍包含旧宣传或临时导出: {relative}")

    docs = ["README.md", "deploy/README.md", "deploy/DOCKER.md"]
    forbidden_text = {
        "Wei-Shaw": "旧仓库宣传",
        "trendshift": "Trending 宣传",
        "Sponsors": "赞助商内容",
        "赞助商": "赞助商内容",
        "sub2api.org": "旧项目外链",
    }
    for relative in docs:
        content = read(relative)
        for needle, label in forbidden_text.items():
            if needle.lower() in content.lower():
                errors.append(f"{relative} 仍包含{label}: {needle}")

    tracked = git_output("ls-files", "-z")
    for relative in filter(None, tracked.split("\0")):
        path = ROOT / relative
        if not path.is_file() or path.stat().st_size > 5 * 1024 * 1024:
            continue
        try:
            content = path.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
        if re.search(r"(?i)(?:api\.)?xiass\.com", content):
            errors.append(f"检测到维护者线上域名硬编码: {relative}")
        if re.search(r"admin-[0-9a-f]{48,}", content):
            errors.append(f"检测到疑似管理员密钥: {relative}")


def check_persistence(errors: list[str]) -> None:
    local_compose = read("deploy/docker-compose.local.yml")
    named_compose = read("deploy/docker-compose.yml")
    required_local = [
        "./data:/app/data:Z",
        "./postgres_data:/var/lib/postgresql/data:Z",
        "./redis_data:/data:Z",
    ]
    required_named = [
        "sub2api_data:/app/data",
        "postgres_data:/var/lib/postgresql/data",
        "redis_data:/data",
    ]
    for mount in required_local:
        if mount not in local_compose:
            errors.append(f"本地目录持久化挂载缺失: {mount}")
    for mount in required_named:
        if mount not in named_compose:
            errors.append(f"命名卷持久化挂载缺失: {mount}")

    watchtower_target = (
        "command: --http-api-update --http-api-token sub2api-update-token sub2api"
    )
    for relative, content in [
        ("deploy/docker-compose.local.yml", local_compose),
        ("deploy/docker-compose.yml", named_compose),
    ]:
        if watchtower_target not in content:
            errors.append(f"{relative} 的在线更新目标不再限定为应用容器")

    install_script = read("deploy/nowind-install.sh")
    if 'if [ -f "$env_file" ]' not in install_script or "保留已有 .env" not in install_script:
        errors.append("一键安装脚本不再明确保留已有 .env")

    for relative in [
        "install.sh",
        "deploy/nowind-install.sh",
        "deploy/nowind-update.sh",
        "deploy/nowind-backup.sh",
        "deploy/nowind-restore.sh",
    ]:
        content = read(relative)
        for line_number, line in enumerate(content.splitlines(), start=1):
            stripped = line.strip()
            if stripped.startswith(("#", "echo ", "printf ")):
                continue
            is_compose_command = re.match(
                r"^(?:compose|docker\s+compose|docker-compose|\"?\$\{COMPOSE\[@\]\})\s+",
                stripped,
            )
            if is_compose_command and re.search(
                r"\bdown\s+(?:-[A-Za-z]*v[A-Za-z]*|--volumes)\b", stripped
            ):
                errors.append(f"{relative}:{line_number} 禁止在维护脚本中删除卷")


def check_migration_immutability(errors: list[str]) -> None:
    try:
        tags = git_output("tag", "--list", "v[0-9]*", "--sort=-v:refname").splitlines()
    except (subprocess.CalledProcessError, FileNotFoundError):
        return
    stable_tags = [tag for tag in tags if re.fullmatch(r"v\d+\.\d+\.\d+", tag)]
    if not stable_tags:
        return
    base = stable_tags[0]
    changes = git_output("diff", "--name-status", base, "--", "backend/migrations")
    for line in changes.splitlines():
        if not line:
            continue
        status, *paths = line.split("\t")
        if status == "A":
            continue
        errors.append(
            f"已发布迁移只能新增，不能修改/删除/重命名: {status} {' '.join(paths)} (基准 {base})"
        )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--skip-migrations", action="store_true")
    args = parser.parse_args()

    errors: list[str] = []
    check_version(errors)
    check_public_branding_and_privacy(errors)
    check_persistence(errors)
    if not args.skip_migrations:
        check_migration_immutability(errors)

    if errors:
        print("NoWind 发布契约检查失败：", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1

    print("NoWind 发布契约检查通过。")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
