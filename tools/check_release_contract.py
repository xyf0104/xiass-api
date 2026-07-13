#!/usr/bin/env python3
"""Validate XIASS release branding, privacy, migrations, and data persistence."""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
PUBLIC_DOCS = ("README.md", "deploy/README.md", "deploy/DOCKER.md")


def read(path: str) -> str:
    return (ROOT / path).read_text(encoding="utf-8")


def git_output(*args: str) -> str:
    return subprocess.check_output(
        ["git", *args], cwd=ROOT, text=True, stderr=subprocess.DEVNULL
    ).strip()


def require_all(
    relative: str, content: str, required: list[str], errors: list[str]
) -> None:
    for needle in required:
        if needle not in content:
            errors.append(f"{relative} 缺少发布契约内容: {needle}")


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

    forbidden_text = {
        "Wei-Shaw": "旧仓库宣传",
        "trendshift": "Trending 宣传",
        "Sponsors": "赞助商内容",
        "赞助商": "赞助商内容",
        "sub2api.org": "旧项目外链",
    }
    for relative in PUBLIC_DOCS:
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


def check_release_branding_and_compatibility(errors: list[str]) -> None:
    full = read(".goreleaser.yaml")
    simple = read(".goreleaser.simple.yaml")
    workflow = read(".github/workflows/release.yml")

    require_all(
        ".goreleaser.yaml",
        full,
        [
            "project_name: xiass-api",
            "binary: sub2api",
            'ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/xiass-api:{{ .Version }}-amd64',
            'ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/xiass-api:{{ .Version }}-arm64',
            'ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/nowind-api:{{ .Version }}-amd64',
            'ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/nowind-api:{{ .Version }}-arm64',
            'name_template: "ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/xiass-api:{{ .Version }}"',
            'name_template: "ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/xiass-api:latest"',
            'name_template: "ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/nowind-api:{{ .Version }}"',
            'name_template: "ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/nowind-api:latest"',
            'name_template: "ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/sub2api:{{ .Version }}"',
            'name_template: "ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/sub2api:latest"',
        ],
        errors,
    )
    require_all(
        ".goreleaser.simple.yaml",
        simple,
        [
            "project_name: xiass-api",
            "binary: sub2api",
            'ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/xiass-api:{{ .Version }}',
            'ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/xiass-api:latest',
            'ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/nowind-api:{{ .Version }}',
            'ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/nowind-api:latest',
            'ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/sub2api:{{ .Version }}',
            'ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/sub2api:latest',
            'name_template: "XIASS API {{.Version}}"',
            "> 支持 linux/amd64 GHCR 镜像和安装包",
        ],
        errors,
    )
    if "(Simple)" in simple or "Simple Release" in simple:
        errors.append("简化发布配置向用户暴露了内部构建模式名")

    for relative, content in [
        (".goreleaser.yaml", full),
        (".goreleaser.simple.yaml", simple),
    ]:
        footer = content.partition("footer: |")[2]
        if "/sub2api:" in footer:
            errors.append(f"{relative} 的公开 Release 文案宣传了旧镜像别名")

    require_all(
        ".github/workflows/release.yml",
        workflow,
        [
            "${{ secrets.DOCKERHUB_USERNAME }}/xiass-api",
            'GHCR_IMAGE="ghcr.io/${{ steps.lowercase.outputs.owner }}/xiass-api"',
            "/pkgs/container/xiass-api",
        ],
        errors,
    )
    if "sub2api" in workflow:
        errors.append("release workflow 仍在公开流程中使用旧品牌名")

    for relative in PUBLIC_DOCS:
        content = read(relative)
        if "ghcr.io/xyf0104/sub2api" in content:
            errors.append(f"{relative} 宣传了旧 GHCR 镜像")
        if re.search(
            r"(?m)^\s*(?:docker(?:-compose)?|docker\s+compose)\b[^\n]*\bsub2api\b",
            content,
        ):
            errors.append(f"{relative} 的命令仍使用旧应用名")

    require_all(
        "Dockerfile",
        read("Dockerfile"),
        [
            "# XIASS API Multi-Stage Dockerfile",
            "XIASS API - AI API Gateway Platform",
            "addgroup -g 1000 nowind",
            "adduser -u 1000 -G nowind",
            "/app/nowind-api",
            'CMD ["/app/nowind-api"]',
        ],
        errors,
    )
    require_all(
        "Dockerfile.goreleaser",
        read("Dockerfile.goreleaser"),
        [
            "# XIASS API Dockerfile for GoReleaser",
            "XIASS API - customized AI API gateway",
            "addgroup -g 1000 nowind",
            "adduser -u 1000 -G nowind",
            "sub2api /app/nowind-api",
            'CMD ["/app/nowind-api"]',
        ],
        errors,
    )
    for relative in ["Dockerfile", "Dockerfile.goreleaser"]:
        content = read(relative)
        if "/app/sub2api" in content or "sub2api:sub2api" in content:
            errors.append(f"{relative} 仍暴露旧镜像内部运行路径或用户")

    require_all(
        "deploy/docker-entrypoint.sh",
        read("deploy/docker-entrypoint.sh"),
        ["chown -R 1000:1000 /app/data", "su-exec 1000:1000", "/app/nowind-api"],
        errors,
    )


def check_compose_branding(errors: list[str]) -> None:
    compose_paths = [
        "deploy/docker-compose.local.yml",
        "deploy/docker-compose.yml",
        "deploy/docker-compose.nowind.yml",
        "deploy/docker-compose.standalone.yml",
        "deploy/docker-compose.build.yml",
        "deploy/docker-compose.dev.yml",
    ]
    for relative in compose_paths:
        content = read(relative)
        if re.search(r"(?m)^  sub2api:\s*$", content):
            errors.append(f"{relative} 仍使用旧应用 service 名")
        if "ghcr.io/xyf0104/sub2api" in content:
            errors.append(f"{relative} 仍使用旧 GHCR 镜像")
        if not re.search(r"(?m)^  nowind-api:\s*$", content):
            errors.append(f"{relative} 缺少 nowind-api service")

    token_default = "${NOWIND_WATCHTOWER_TOKEN:-sub2api-update-token}"
    for relative in ["deploy/docker-compose.local.yml", "deploy/docker-compose.yml"]:
        content = read(relative)
        require_all(
            relative,
            content,
            [
                "image: ghcr.io/xyf0104/xiass-api:latest",
                "container_name: nowind-api",
                "container_name: nowind-api-watchtower",
                "container_name: nowind-api-postgres",
                "container_name: nowind-api-redis",
                "nowind-api-network",
                "command: --http-api-update nowind-api",
                f"NOWIND_WATCHTOWER_TOKEN={token_default}",
                f"WATCHTOWER_HTTP_API_TOKEN={token_default}",
            ],
            errors,
        )
        if content.count(f"NOWIND_WATCHTOWER_TOKEN={token_default}") < 2:
            errors.append(f"{relative} 未同时向应用与 Watchtower 传入更新令牌")

    require_all(
        "deploy/docker-compose.standalone.yml",
        read("deploy/docker-compose.standalone.yml"),
        ["image: ghcr.io/xyf0104/xiass-api:latest", "container_name: nowind-api"],
        errors,
    )
    require_all(
        "deploy/docker-compose.dev.yml",
        read("deploy/docker-compose.dev.yml"),
        [
            "container_name: nowind-api-dev",
            "container_name: nowind-api-postgres-dev",
            "container_name: nowind-api-redis-dev",
            "nowind-api-network",
        ],
        errors,
    )


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

    watchtower_target = "command: --http-api-update nowind-api"
    for relative, content in [
        ("deploy/docker-compose.local.yml", local_compose),
        ("deploy/docker-compose.yml", named_compose),
    ]:
        if watchtower_target not in content:
            errors.append(f"{relative} 的在线更新目标不再限定为应用容器")

    historical_identifiers = [
        "DATABASE_USER=${POSTGRES_USER:-sub2api}",
        "DATABASE_DBNAME=${POSTGRES_DB:-sub2api}",
        "POSTGRES_USER=${POSTGRES_USER:-sub2api}",
        "POSTGRES_DB=${POSTGRES_DB:-sub2api}",
    ]
    for relative, content in [
        ("deploy/docker-compose.local.yml", local_compose),
        ("deploy/docker-compose.yml", named_compose),
    ]:
        require_all(relative, content, historical_identifiers, errors)

    config = read("backend/internal/config/config.go")
    if 'viper.SetDefault("dashboard_cache.key_prefix", "sub2api:")' not in config:
        errors.append("Redis dashboard_cache 历史前缀 sub2api: 被修改")

    install_script = read("deploy/xiass-install.sh")
    if 'if [ -f "$env_file" ]' not in install_script or "保留已有 .env" not in install_script:
        errors.append("一键安装脚本不再明确保留已有 .env")
    require_all(
        "deploy/xiass-install.sh",
        install_script,
        [
            "NOWIND_WATCHTOWER_TOKEN=${watchtower_token}",
            'for container_name in sub2api sub2api-watchtower sub2api-postgres sub2api-redis; do',
            'docker stop -t 60 "$container_name"',
            'docker rm "$container_name"',
        ],
        errors,
    )

    for relative in [
        "install.sh",
        "deploy/xiass-install.sh",
        "deploy/xiass-update.sh",
        "deploy/xiass-backup.sh",
        "deploy/xiass-restore.sh",
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


def check_update_bridge(errors: list[str]) -> None:
    service = read("backend/internal/service/docker_update_service.go")
    service_test = read("backend/internal/service/docker_update_service_test.go")
    require_all(
        "backend/internal/service/docker_update_service.go",
        service,
        [
            'watchtowerUpdateURL',
            '"http://watchtower:8080/v1/update"',
            'watchtowerTokenEnv',
            '"NOWIND_WATCHTOWER_TOKEN"',
            'legacyWatchtowerToken',
            '"sub2api-update-token"',
            "strings.TrimSpace(os.Getenv(watchtowerTokenEnv))",
        ],
        errors,
    )
    require_all(
        "backend/internal/service/docker_update_service_test.go",
        service_test,
        ["uses service DNS and configured token", "falls back to v1.0.65 token"],
        errors,
    )

    update_script = read("deploy/xiass-update.sh")
    main_body = update_script.partition("main() {")[2]
    ordered_markers = [
        'xiass-backup.sh',
        'git -C "$INSTALL_DIR" fetch --prune origin main',
        "compose down",
        'git -C "$INSTALL_DIR" reset --hard origin/main',
    ]
    positions = [main_body.find(marker) for marker in ordered_markers]
    if any(position < 0 for position in positions) or positions != sorted(positions):
        errors.append("xiass-update.sh 必须先备份，再拉取、停止旧栈并切换 Git 状态")
    require_all(
        "deploy/xiass-update.sh",
        update_script,
        [
            'PREVIOUS_REF=$(git -C "$INSTALL_DIR" rev-parse HEAD)',
            "rollback_update()",
            'git -C "$INSTALL_DIR" reset --hard "$PREVIOUS_REF"',
            "UPDATE_STARTED=true",
            "compose pull nowind-api watchtower",
        ],
        errors,
    )
    if "git clean" in update_script:
        errors.append("xiass-update.sh 禁止清理未跟踪的 .env 或数据目录")
    if re.search(
        r"(?m)^\s*(?:rm|cp|mv)\b[^\n]*(?:\.env|postgres_data|redis_data|/data\b)",
        update_script,
    ):
        errors.append("xiass-update.sh 禁止覆盖或移动持久化数据")


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
    check_release_branding_and_compatibility(errors)
    check_compose_branding(errors)
    check_persistence(errors)
    check_update_bridge(errors)
    if not args.skip_migrations:
        check_migration_immutability(errors)

    if errors:
        print("XIASS 发布契约检查失败：", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1

    print("XIASS 发布契约检查通过。")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
