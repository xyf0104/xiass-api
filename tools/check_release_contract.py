#!/usr/bin/env python3
"""Validate XIASS release branding, privacy, migrations, and data persistence."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
PUBLIC_DOCS = ("README.md", "deploy/README.md", "deploy/DOCKER.md")
ALLOWED_PUBLIC_API_REFERENCES = {
    "tools/check_release_contract.py": ("https://api.xiass.com",),
    "README.md": ("`https://api.xiass.com`",),
    "tools/xiass-codex-helper/README.md": ("`https://api.xiass.com`",),
    "tools/xiass-codex-helper/main.go": (
        'const defaultXIASSAPIURL = "https://api.xiass.com"',
    ),
    "tools/xiass-codex-helper/web/index.html": (
        'placeholder="https://api.xiass.com"',
    ),
    "ios/XIASSAdmin/README.md": (
        "`https://api.xiass.com/ios`",
    ),
    "ios/XIASSAdmin/Sources/Foundation/APIClient.swift": (
        "https://api.xiass.com",
    ),
    "ios/XIASSAdmin/Sources/Foundation/AppSession.swift": (
        "https://api.xiass.com",
    ),
    "ios/XIASSAdmin/Sources/Views/LoginView.swift": (
        "https://api.xiass.com",
    ),
}


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
    if not re.fullmatch(r"(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)", version):
        errors.append(f"VERSION 格式无效: {version!r}")
        return
    if any(int(component) > 99 for component in version.split(".")):
        errors.append(f"VERSION 每一段必须不大于 99: {version!r}")
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
        domain_scan = content
        for allowed in ALLOWED_PUBLIC_API_REFERENCES.get(relative, ()):
            domain_scan = domain_scan.replace(allowed, "")
        if re.search(r"(?i)(?:api\.)?xiass\.com", domain_scan):
            errors.append(f"检测到维护者线上域名硬编码: {relative}")
        if re.search(r"admin-[0-9a-f]{48,}", content):
            errors.append(f"检测到疑似管理员密钥: {relative}")


def check_team_child_mailbox_release_privacy(errors: list[str]) -> None:
    """Keep imported mailbox provider settings out of release artifacts."""
    legacy_importer = ROOT / "deploy/import-team-child-mail-config.sh"
    if legacy_importer.exists():
        errors.append("发布物不得包含命令行邮箱配置迁移脚本")

    template = read("deploy/.env.example")
    expected_defaults = {
        "TEAM_CHILD_MAIL_API_BASE": "",
        "TEAM_CHILD_MAIL_AUTH_MODE": "none",
        "TEAM_CHILD_MAIL_API_KEY": "",
        "TEAM_CHILD_MAIL_CUSTOM_AUTH": "",
        "TEAM_CHILD_MAIL_DOMAIN": "",
        "TEAM_CHILD_MAIL_CREATE_PATH": "/api/new_address",
        "TEAM_CHILD_MAIL_MESSAGES_PATH": "/api/mails",
        "TEAM_CHILD_MAIL_CONFIG_FILE": "/app/data/team-child-mail.env",
    }
    for key, expected in expected_defaults.items():
        match = re.search(rf"(?m)^{re.escape(key)}=(.*)$", template)
        if not match or match.group(1) != expected:
            errors.append(f"deploy/.env.example 的 {key} 必须保持发布安全默认值")

    public_mail_docs = ("deploy/.env.example", "deploy/README.md", "deploy/TEAM_CHILD_BROWSER_SETUP.md")
    for relative in public_mail_docs:
        content = read(relative)
        if "import-team-child-mail-config.sh" in content:
            errors.append(f"{relative} 不得引导使用命令行邮箱配置迁移脚本")
        if "cloudflare_api_" in content:
            errors.append(f"{relative} 不得包含旧邮箱配置字段")

    require_all(
        "deploy/TEAM_CHILD_BROWSER_SETUP.md",
        read("deploy/TEAM_CHILD_BROWSER_SETUP.md"),
        ["导入邮箱配置", "/app/data/team-child-mail.env"],
        errors,
    )


def check_frontend_visible_branding(errors: list[str]) -> None:
    index_path = "frontend/index.html"
    manifest_path = "frontend/public/site.webmanifest"
    store_path = "frontend/src/stores/app.ts"
    title_path = "frontend/src/router/title.ts"
    contents = {
        relative: read(relative)
        for relative in (index_path, manifest_path, store_path, title_path)
    }

    require_all(
        index_path,
        contents[index_path],
        [
            '<html lang="zh-CN" class="dark" data-theme="dark">',
            '<meta name="color-scheme" content="dark light" />',
            'nonce="__CSP_NONCE_VALUE__"',
            "window.localStorage.getItem('theme')",
            "root.classList.toggle('dark', theme === 'dark')",
            "root.dataset.themeBooting = 'true'",
            'html[data-theme-booting] #app',
            "<title>XIASS API</title>",
            'href="/favicon-dark.png',
        ],
        errors,
    )
    require_all(
        store_path,
        contents[store_path],
        [
            "const siteName = ref<string>('XIASS API')",
            "siteName.value = config.site_name || 'XIASS API'",
        ],
        errors,
    )
    require_all(
        title_path,
        contents[title_path],
        [": 'XIASS API'"],
        errors,
    )

    try:
        manifest = json.loads(contents[manifest_path])
    except json.JSONDecodeError as exc:
        errors.append(f"{manifest_path} 不是有效 JSON: {exc}")
    else:
        if not isinstance(manifest, dict):
            errors.append(f"{manifest_path} 顶层必须是 JSON 对象")
        else:
            for key in ("name", "short_name"):
                if manifest.get(key) != "XIASS API":
                    errors.append(f"{manifest_path} 的 {key} 默认品牌必须为 XIASS API")
            icons = manifest.get("icons", [])
            icon_sources = (
                {
                    icon.get("src", "").partition("?")[0]
                    for icon in icons
                    if isinstance(icon, dict) and isinstance(icon.get("src"), str)
                }
                if isinstance(icons, list)
                else set()
            )
            if "/brand/xiass-mark-light.png" not in icon_sources:
                errors.append(f"{manifest_path} 缺少 XIASS 品牌图标引用")

    visible_sub2api = re.compile(
        r"(?:[\"'][^\"'\n]*\bSub2API\b[^\"'\n]*[\"']|"
        r"<title>[^<]*\bSub2API\b[^<]*</title>)"
    )
    for relative, content in contents.items():
        if visible_sub2api.search(content):
            errors.append(f"{relative} 仍包含可见 Sub2API 标题")

    legacy_favicon = re.compile(
        r"/(?:favicon\.png|logo\.png|vite\.svg)(?:\?[^\"'\s<>]*)?",
        re.IGNORECASE,
    )
    for relative in (index_path, manifest_path):
        match = legacy_favicon.search(contents[relative])
        if match:
            errors.append(f"{relative} 仍引用旧 favicon: {match.group(0)}")


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
            "      - install.sh",
            "      - deploy/team-child-automation/email-code.mjs",
            'ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/xiass-api:{{ .Version }}-amd64',
            'ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/xiass-api:{{ .Version }}-arm64',
            'name_template: "ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/xiass-api:{{ .Version }}"',
            'name_template: "ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/xiass-api:latest"',
        ],
        errors,
    )
    require_all(
        ".goreleaser.simple.yaml",
        simple,
        [
            "project_name: xiass-api",
            "binary: sub2api",
            "      - install.sh",
            "      - deploy/team-child-automation/email-code.mjs",
            'ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/xiass-api:{{ .Version }}',
            'ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/xiass-api:latest',
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
        for legacy_image in ("/sub2api:", "/nowind-api:"):
            if legacy_image in content:
                errors.append(f"{relative} 仍会发布旧 GHCR 镜像: {legacy_image}")

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
            "addgroup -g 1000 xiass",
            "adduser -u 1000 -G xiass",
            "/app/xiass-api",
            'CMD ["/app/xiass-api"]',
        ],
        errors,
    )
    require_all(
        "Dockerfile.goreleaser",
        read("Dockerfile.goreleaser"),
        [
            "# XIASS API Dockerfile for GoReleaser",
            "XIASS API - customized AI API gateway",
            "addgroup -g 1000 xiass",
            "adduser -u 1000 -G xiass",
            "sub2api /app/xiass-api",
            'CMD ["/app/xiass-api"]',
        ],
        errors,
    )
    require_all(
        "deploy/docker-entrypoint.sh",
        read("deploy/docker-entrypoint.sh"),
        [
            "chown -R 1000:1000 /app/data",
            "stat -c '%g' /var/run/docker.sock",
            'addgroup xiass "$docker_socket_group"',
            "su-exec xiass",
            "/app/xiass-api",
        ],
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
        if not re.search(r"(?m)^  xiass-api:\s*$", content):
            errors.append(f"{relative} 缺少 xiass-api service")

    token_default = "${XIASS_WATCHTOWER_TOKEN:-${NOWIND_WATCHTOWER_TOKEN:-sub2api-update-token}}"
    for relative in ["deploy/docker-compose.local.yml", "deploy/docker-compose.yml"]:
        content = read(relative)
        require_all(
            relative,
            content,
            [
                "image: ghcr.io/xyf0104/xiass-api:latest",
                "container_name: xiass-api",
                "container_name: xiass-api-watchtower",
                "container_name: xiass-api-postgres",
                "container_name: xiass-api-redis",
                "xiass-api-network",
                "command: --http-api-update xiass-api",
                f"XIASS_WATCHTOWER_TOKEN={token_default}",
                f"NOWIND_WATCHTOWER_TOKEN={token_default}",
                f"WATCHTOWER_HTTP_API_TOKEN={token_default}",
                "DOCKER_API_VERSION=${DOCKER_API_VERSION:-1.40}",
            ],
            errors,
        )
        if content.count(f"XIASS_WATCHTOWER_TOKEN={token_default}") < 2:
            errors.append(f"{relative} 未同时向应用与 Watchtower 传入 XIASS 更新令牌")

    standalone_compose = read("deploy/docker-compose.standalone.yml")
    require_all(
        "deploy/docker-compose.standalone.yml",
        standalone_compose,
        [
            "image: ghcr.io/xyf0104/xiass-api:latest",
            "container_name: xiass-api",
            "container_name: xiass-api-watchtower",
            "command: --http-api-update xiass-api",
            f"XIASS_WATCHTOWER_TOKEN={token_default}",
            f"NOWIND_WATCHTOWER_TOKEN={token_default}",
            f"WATCHTOWER_HTTP_API_TOKEN={token_default}",
            "DOCKER_API_VERSION=${DOCKER_API_VERSION:-1.40}",
        ],
        errors,
    )
    if standalone_compose.count(f"XIASS_WATCHTOWER_TOKEN={token_default}") < 2:
        errors.append("deploy/docker-compose.standalone.yml 未同时向应用与 Watchtower 传入 XIASS 更新令牌")

    alipay_mobile_setting = (
        "ALIPAY_MOBILE_PRECREATE_DEEP_LINK=${ALIPAY_MOBILE_PRECREATE_DEEP_LINK:-}"
    )
    for relative in [
        "deploy/docker-compose.yml",
        "deploy/docker-compose.local.yml",
        "deploy/docker-compose.standalone.yml",
        "deploy/docker-compose.dev.yml",
    ]:
        if alipay_mobile_setting not in read(relative):
            errors.append(f"{relative} 缺少支付宝移动端当面付配置透传")

    if "ADMIN_EMAIL=admin@nowind.local" in read("deploy/.env.example"):
        errors.append("deploy/.env.example 仍使用旧 NoWind 管理员邮箱默认值")
    require_all(
        "deploy/docker-compose.dev.yml",
        read("deploy/docker-compose.dev.yml"),
        [
            "container_name: xiass-api-dev",
            "container_name: xiass-api-postgres-dev",
            "container_name: xiass-api-redis-dev",
            "xiass-api-network",
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

    watchtower_target = "command: --http-api-update xiass-api"
    for relative, content in [
        ("deploy/docker-compose.local.yml", local_compose),
        ("deploy/docker-compose.yml", named_compose),
    ]:
        if watchtower_target not in content:
            errors.append(f"{relative} 的在线更新目标不再限定为应用容器")

    historical_identifiers = [
        "POSTGRES_USER=${POSTGRES_USER:-sub2api}",
        "POSTGRES_DB=${POSTGRES_DB:-sub2api}",
    ]
    database_identity_fallbacks = {
        "DATABASE_USER": (
            "DATABASE_USER=${POSTGRES_USER:-sub2api}",
            "DATABASE_USER=${DATABASE_USER:-${POSTGRES_USER:-sub2api}}",
        ),
        "DATABASE_DBNAME": (
            "DATABASE_DBNAME=${POSTGRES_DB:-sub2api}",
            "DATABASE_DBNAME=${DATABASE_DBNAME:-${POSTGRES_DB:-sub2api}}",
        ),
    }
    for relative, content in [
        ("deploy/docker-compose.local.yml", local_compose),
        ("deploy/docker-compose.yml", named_compose),
    ]:
        require_all(relative, content, historical_identifiers, errors)
        for setting, variants in database_identity_fallbacks.items():
            if not any(variant in content for variant in variants):
                errors.append(
                    f"{relative} 缺少发布契约内容: {setting} 必须保留 POSTGRES_* 回退"
                )

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
            "XIASS_WATCHTOWER_TOKEN=${watchtower_token}",
            "NOWIND_WATCHTOWER_TOKEN=${watchtower_token}",
            "backup_existing_runtime()",
            "com.docker.compose.project.working_dir",
            "PERSISTENCE_MODE",
            "docker-compose.yml",
            "xiass-api nowind-api sub2api",
            '"/var/lib/postgresql/data"',
            'docker stop -t 60 "$container_name"',
            'docker rm "$container_name"',
        ],
        errors,
    )

    for relative, required in [
        (
            "deploy/xiass-backup.sh",
            [
                "com.docker.compose.project.config_files",
                "archive_named_volume()",
                "layout=named",
                "docker-compose.yml",
                "docker-compose.local.yml",
            ],
        ),
        (
            "deploy/xiass-restore.sh",
            [
                "com.docker.compose.project.config_files",
                "snapshot_named_volume()",
                "restore_named_volume()",
                "拒绝跨布局恢复",
                "docker-compose.yml",
                "docker-compose.local.yml",
            ],
        ),
    ]:
        require_all(relative, read(relative), required, errors)

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
            '"XIASS_WATCHTOWER_TOKEN"',
            '"NOWIND_WATCHTOWER_TOKEN"',
            'legacyWatchtowerToken',
            '"sub2api-update-token"',
            "strings.TrimSpace(os.Getenv(watchtowerTokenEnv))",
            'create.HostConfig.NetworkMode = "host"',
        ],
        errors,
    )
    require_all(
        "backend/internal/service/docker_update_service_test.go",
        service_test,
        ["uses service DNS and configured token", "previous token variable as a compatibility fallback", "falls back to v1.0.65 token"],
        errors,
    )

    runtime_start_script = read("deploy/xiass-runtime-start.sh")
    require_all(
        "deploy/xiass-runtime-start.sh",
        runtime_start_script,
        [
            "start_core()",
            "wait_for_core_health()",
            'curl -fsS --max-time 3 "http://127.0.0.1:${port}/health"',
            "start_browser_stack()",
            'sleep "$CORE_READY_DELAY_SECONDS"',
            "team-child-browser",
            "team-child-automation",
            "主服务保持运行",
        ],
        errors,
    )
    runtime_main = runtime_start_script.partition("main() {")[2]
    runtime_order = [
        "resolve_compose",
        "start_core",
        "start_browser_stack",
    ]
    runtime_positions = [runtime_main.find(marker) for marker in runtime_order]
    if any(position < 0 for position in runtime_positions) or runtime_positions != sorted(
        runtime_positions
    ):
        errors.append("xiass-runtime-start.sh 必须先启动并验证主服务，再进入浏览器组件阶段")

    for compose_path in [
        "deploy/docker-compose.yml",
        "deploy/docker-compose.standalone.yml",
        "deploy/docker-compose.local.yml",
        "deploy/docker-compose.dev.yml",
    ]:
        require_all(
            compose_path,
            read(compose_path),
            [
                "--disable-background-timer-throttling",
                "--disable-backgrounding-occluded-windows",
                "--disable-renderer-backgrounding",
            ],
            errors,
        )

    updater_dockerfile = read("deploy/xiass-updater/Dockerfile")
    require_all(
        "deploy/xiass-updater/Dockerfile",
        updater_dockerfile,
        [
            "COPY xiass-update.sh xiass-runtime-start.sh xiass-backup.sh xiass-runtime-export.sh xiass-runtime-restore.sh ./",
            "COPY xiass-updater/xiass-updater-entrypoint.sh /usr/local/bin/xiass-updater",
        ],
        errors,
    )
    require_all(
        "deploy/xiass-cluster-join.sh",
        read("deploy/xiass-cluster-join.sh"),
        [
            "source-authoritative",
            "OLD_ENV_FILE",
            "compose up -d --no-deps --no-build --force-recreate xiass-api",
            "finalize_source",
            "自动回滚",
            'set_env_value POSTGRES_PASSWORD "$database_pass"',
        ],
        errors,
    )

    for relative, required in [
        (
            "deploy/xiass-runtime-export.sh",
            [
                "--postgres-from-container",
                'docker cp "$POSTGRES_FROM_CONTAINER" "$WORK_DIR/payload/postgres.sql.gz"',
                'gzip -t "$WORK_DIR/payload/postgres.sql.gz"',
                "redis_cli()",
                "--rdb /tmp/xiass.rdb",
                'container:$APP_CONTAINER',
                '"$WORK_DIR/payload/redis.acl"',
                "--runtime-context-from-container",
                'docker cp "$APP_CONTAINER:/app/data/."',
                'rm -rf "$WORK_DIR/payload/app-data/runtime-exports"',
                "--publish-to-container",
                'PUBLISH_TEMP_PATH="${PUBLISH_PATH}.partial"',
                'docker cp "$OUTPUT" "$PUBLISH_CONTAINER:$PUBLISH_TEMP_PATH"',
                'mv -f "$1" "$2"',
                "xiass-runtime-restore.sh",
            ],
        ),
        (
            "deploy/xiass-runtime-restore.sh",
            [
                'exec sudo -E bash "$0" "${ORIGINAL_ARGS[@]}"',
                "verify_package()",
                "--domain new.example.com",
                "stop_existing_install()",
                "compose_at",
                "compose pull postgres redis xiass-api watchtower",
            ],
        ),
    ]:
        require_all(relative, read(relative), required, errors)

    require_all(
        "backend/internal/service/runtime_export_service.go",
        read("backend/internal/service/runtime_export_service.go"),
        ["NewRuntimeExportService(dumper DBDumper, cfg *config.Config)", "s.dumper.Dump(ctx)",
         "stream.Close()", '"--postgres-from-container"'],
        errors,
    )
    require_all(
        "backend/internal/repository/backup_pg_dumper.go",
        read("backend/internal/repository/backup_pg_dumper.go"),
        ["cfg: &cfg.Database", 'exec.CommandContext(ctx, "pg_dump", args...)'],
        errors,
    )

    update_script = read("deploy/xiass-update.sh")
    backup_script = read("deploy/xiass-backup.sh")
    main_body = update_script.partition("main() {")[2]
    ordered_markers = [
        "ensure_xiass_update_remote",
        "resolve_stable_release",
        "snapshot_previous_compose",
        'xiass-backup.sh',
        "capture_previous_image",
        "UPDATE_STARTED=true",
        'git -C "$INSTALL_DIR" reset --hard "$UPDATE_REF"',
        "restore_local_compose",
        "prefetch_target_app_image",
        "verify_target_image_id",
        "if can_hot_swap_canonical_app; then",
    ]
    positions = [main_body.find(marker) for marker in ordered_markers]
    if any(position < 0 for position in positions) or positions != sorted(positions):
        errors.append(
            "xiass-update.sh 必须在线保存配置与旧镜像、同步并准备目标镜像，再开始容器切换"
        )
    prepare_body = update_script.partition("prefetch_target_app_image() {")[2].partition("\n}\n")[0]
    prepare_markers = [
        "compose config --quiet",
        "compose build xiass-api",
        "compose pull xiass-api",
        "verify_prepared_app_image",
        "TARGET_APP_IMAGE_PREFETCHED=true",
    ]
    prepare_positions = [prepare_body.find(marker) for marker in prepare_markers]
    if any(position < 0 for position in prepare_positions) or prepare_positions != sorted(prepare_positions):
        errors.append("xiass-update.sh 下载/构建及镜像版本校验必须先于允许容器切换")
    if main_body.find("if ! verify_running_target; then") < 0 or not (
        main_body.find("if ! verify_running_target; then") < main_body.find("UPDATE_SUCCEEDED=true")
    ):
        errors.append("xiass-update.sh 必须核实运行版本后才可报告更新成功")
    hot_swap_body = main_body.partition("if can_hot_swap_canonical_app; then")[2].partition("else")[0]
    hot_swap_markers = [
        "hot_swap_canonical_app",
        "start_runtime_stack true true",
    ]
    hot_swap_positions = [hot_swap_body.find(marker) for marker in hot_swap_markers]
    if any(position < 0 for position in hot_swap_positions) or hot_swap_positions != sorted(hot_swap_positions):
        errors.append("xiass-update.sh 规范安装必须只热切换应用并保持数据库、缓存容器在线")
    legacy_body = main_body.partition('log "历史部署布局使用完整兼容切换')[2]
    legacy_markers = [
        "stop_previous_runtime",
        "stop_known_runtime_containers",
        'start_runtime_stack "$TARGET_APP_IMAGE_PREFETCHED"',
    ]
    legacy_positions = [legacy_body.find(marker) for marker in legacy_markers]
    if any(position < 0 for position in legacy_positions) or legacy_positions != sorted(legacy_positions):
        errors.append("xiass-update.sh 历史安装必须保留停止旧栈后的完整兼容迁移路径")
    require_all(
        "deploy/xiass-update.sh",
        update_script,
        [
            'PREVIOUS_REF=$(git -C "$INSTALL_DIR" rev-parse HEAD)',
            'PREVIOUS_IMAGE_ID=""',
            'PREVIOUS_IMAGE_REF=""',
            "capture_previous_image()",
            'fetch --no-tags "$UPDATE_REMOTE" "refs/tags/$UPDATE_TAG"',
            "FETCH_HEAD^{commit}",
            "verify_prepared_app_image()",
            "--entrypoint /app/xiass-api",
            "/api/v1/settings/public",
            "generate_runtime_token()",
            "append_env_default()",
            "ensure_team_child_runtime_config()",
            "TEAM_CHILD_AUTOMATION_TOKEN",
            "{{.Image}} {{.Config.Image}}",
            "rollback_update()",
            'git -C "$INSTALL_DIR" reset --hard "$PREVIOUS_REF"',
            'docker image tag "$PREVIOUS_IMAGE_ID" "$PREVIOUS_IMAGE_REF"',
            "UPDATE_STARTED=true",
            "start_runtime_stack true",
            "snapshot_previous_compose()",
            "restore_previous_config()",
            "APP_REPLACEMENT_STARTED=false",
            "compose up -d --no-deps --no-build --pull never --force-recreate xiass-api",
            "PREVIOUS_COMPOSE_FILES",
            "com.docker.compose.project.config_files",
            "docker inspect --type container",
            "PERSISTENCE_MODE",
            'CANONICAL_UPSTREAM_REMOTE="xiass-upstream"',
            'UPDATE_REF=""',
            "ensure_xiass_update_remote()",
            "remove_created_update_remote()",
            'remote add "$CANONICAL_UPSTREAM_REMOTE" "$CANONICAL_UPSTREAM_URL"',
        ],
        errors,
    )
    capture_body = update_script.partition("capture_previous_image() {")[2].partition(
        "\n}\n\nwait_for_health()"
    )[0]
    if (
        "for container_name in xiass-api nowind-api sub2api" not in capture_body
        or capture_body.count("return 0") < 2
    ):
        errors.append("xiass-update.sh 记录旧镜像失败时必须非致命降级")
    rollback_body = update_script.partition("rollback_update() {")[2].partition(
        "\n}\n\ncleanup()"
    )[0]
    rollback_markers = [
        'git -C "$INSTALL_DIR" reset --hard "$PREVIOUS_REF"',
        'docker image tag "$PREVIOUS_IMAGE_ID" "$PREVIOUS_IMAGE_REF"',
        "start_runtime_stack true",
        "compose up -d --no-build --pull never >/dev/null",
    ]
    rollback_positions = [rollback_body.find(marker) for marker in rollback_markers]
    if any(position < 0 for position in rollback_positions) or rollback_positions != sorted(
        rollback_positions
    ):
        errors.append(
            "xiass-update.sh 回滚必须恢复 Git 和旧镜像 tag，通过两阶段启动脚本恢复，"
            "并保留普通 compose 启动降级"
        )
    if "git clean" in update_script:
        errors.append("xiass-update.sh 禁止清理未跟踪的 .env 或数据目录")
    if '[ -n "$patch_file" ] && printf' in update_script:
        errors.append("xiass-update.sh 成功收尾不得因空本地补丁路径返回非零状态")
    require_all(
        "deploy/xiass-backup.sh",
        backup_script,
        [
            "XIASS_BACKUP_LIB_ONLY",
            "stat -c '%Y'",
        ],
        errors,
    )
    if "-printf" in backup_script:
        errors.append("xiass-backup.sh 必须兼容不支持 find -printf 的 BusyBox")
    if re.search(
        r"(?m)^\s*(?:rm|cp|mv)\b[^\n]*(?:\.env|postgres_data|redis_data|/data\b)",
        update_script,
    ):
        errors.append("xiass-update.sh 禁止覆盖或移动持久化数据")


def check_soft_router_compatibility(errors: list[str]) -> None:
    service = read("backend/internal/service/soft_router_proxy.go")
    service_test = read("backend/internal/service/soft_router_proxy_test.go")
    installer = read("deploy/frps-soft-router-install.sh")
    restart_hint = "docker compose up -d --force-recreate xiass-api"

    require_all(
        "backend/internal/service/soft_router_proxy.go",
        service,
        [f'result.Metadata["restart_hint"] = "{restart_hint}"'],
        errors,
    )
    require_all(
        "backend/internal/service/soft_router_proxy_test.go",
        service_test,
        [restart_hint],
        errors,
    )
    require_all(
        "deploy/frps-soft-router-install.sh",
        installer,
        ["name=^/xiass-api$", "name=^/nowind-api$", "name=^/sub2api$"],
        errors,
    )


def check_migration_immutability(errors: list[str]) -> None:
    try:
        tags = git_output("tag", "--list", "v[0-9]*", "--sort=-v:refname").splitlines()
        head_tags = set(git_output("tag", "--points-at", "HEAD").splitlines())
    except (subprocess.CalledProcessError, FileNotFoundError):
        return
    stable_tags = [tag for tag in tags if re.fullmatch(r"v\d+\.\d+\.\d+", tag)]
    # During a tag-triggered release the newest tag is the candidate itself.
    # Compare against the preceding stable release so modified historical
    # migrations cannot pass through an empty candidate-to-candidate diff.
    stable_tags = [tag for tag in stable_tags if tag not in head_tags]
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
    check_team_child_mailbox_release_privacy(errors)
    check_frontend_visible_branding(errors)
    check_release_branding_and_compatibility(errors)
    check_compose_branding(errors)
    check_persistence(errors)
    check_update_bridge(errors)
    check_soft_router_compatibility(errors)
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
