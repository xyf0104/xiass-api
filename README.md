<div align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="frontend/public/brand/xiass-mark-dark.png" />
    <img src="frontend/public/brand/xiass-mark-light.png" alt="XIASS API Logo" width="128" />
  </picture>
  <h1>XIASS API</h1>
  <p>面向个人与团队的 AI API 网关、账号池和计费管理平台</p>
  <p>
    <img src="https://img.shields.io/badge/当前版本-v1.0.79-0ea5e9" alt="当前版本 v1.0.79" />
    <img src="https://img.shields.io/badge/Docker-amd64-2496ed" alt="Docker amd64" />
    <img src="https://img.shields.io/badge/Go-1.26-00add8" alt="Go 1.26" />
    <img src="https://img.shields.io/badge/Vue-3-42b883" alt="Vue 3" />
    <img src="https://img.shields.io/badge/License-LGPL--3.0-16a34a" alt="LGPL-3.0" />
  </p>
</div>

> 当前版本：v1.0.79

v1.0.79 新增 XIASS Codex 配置助手：macOS 与 Windows 用户可从密钥使用弹窗直接下载免安装助手，自动检测 Codex、备份并校验 `config.toml`、写入所选 XIASS API 密钥、重启 Codex，并可随时恢复原配置。

XIASS API 是本项目唯一的公开源码仓库与正式发布源。仓库包含完整前后端源码、Docker 镜像构建、数据库迁移、一键安装、在线更新、备份恢复和软路由代理节点功能。

## 主要功能

- 统一管理 Anthropic、OpenAI/Codex、Gemini、Antigravity、Grok/xAI 等账号与 API Key。
- 账号池调度、并发控制、限流恢复、健康检测、代理绑定和模型映射。
- 用户、分组、渠道、订阅、兑换码、邀请返利和支付订单管理。
- 模型价格、用户倍率、成本倍率、冻结余额和完整用量统计。
- OpenAI、Anthropic 等兼容接口，以及流式请求、图片和视频相关能力。
- 批量图片任务、运行状态、失败请求、IP 地理信息和运维日志。
- OAuth/OIDC、邮箱验证、TOTP、访问密钥和管理员权限管理。
- macOS/Windows XIASS Codex 配置助手，支持自动备份、校验、重启与恢复。
- OpenWrt/PassWall SOCKS 节点上报、FRP 反向连接和公网认证代理。
- GitHub Release 检查、Docker 在线更新、完整备份与跨服务器恢复。

## 数据隔离与持久化

每一台通过本仓库安装的 XIASS 都是独立实例：

- PostgreSQL、Redis、应用数据和安全密钥全部创建在安装者自己的服务器。
- 默认不会连接维护者的数据库、Redis、S3、管理后台或线上 XIASS 实例。
- 新安装会随机生成 PostgreSQL、Redis、JWT 和 TOTP 密钥。
- `.env` 权限设置为 `600`，不会提交到 Git。
- PostgreSQL 与 Redis 不映射到公网，只允许 Docker 内部网络访问。
- 对外请求仅包括 GitHub 版本/镜像获取，以及管理员自己配置的模型上游、OAuth、邮件、支付或代理服务。

推荐安装方式使用本地持久化目录：

> 新安装统一使用 `xiass-api` 和 `/opt/xiass-api`。更新脚本仍会识别旧运行目录、旧容器和旧环境变量，仅用于无损升级，不会连接任何外部 XIASS 实例。

| 数据 | 服务器路径 | 更新时处理方式 |
|---|---|---|
| 环境与密钥 | `/opt/xiass-api/deploy/.env` | 原样保留 |
| 应用配置、日志和附件 | `/opt/xiass-api/deploy/data` | 原样保留 |
| PostgreSQL | `/opt/xiass-api/deploy/postgres_data` | 原样保留并自动迁移 |
| Redis | `/opt/xiass-api/deploy/redis_data` | 原样保留 |

在线更新只替换应用容器，不删除上述目录。任何时候都不要执行 `docker compose down -v`，其中的 `-v` 会删除命名卷。

## 环境要求

推荐使用全新的 Linux VPS：

| 项目 | 最低要求 | 推荐 |
|---|---:|---:|
| 系统 | 64 位 Linux | Debian 12 / Ubuntu 22.04+ / Rocky Linux 9+ |
| CPU | 1 核 | 2 核以上 |
| 内存 | 1.5 GB | 2 GB 以上；源码构建建议 3 GB 以上 |
| 磁盘 | 4 GB 可用 | 10 GB 以上，并按日志与数据库增长预留 |
| 架构 | amd64 或 arm64 | 正式 GHCR 镜像当前为 amd64；arm64 自动源码构建 |

一键安装脚本会自动检查或安装：

- Docker Engine 与 Docker Compose 插件
- `curl`、`git`、`openssl`、`tar`、`gzip`、`iproute2`、`procps`
- 服务端口与代理端口是否被占用
- UFW 或 firewalld 的本机防火墙规则
- 容器启动状态、数据库迁移与 `/health` 健康检查

云服务器的安全组不受系统防火墙控制，需要在云厂商控制台单独放行。

## 端口规划

| 默认端口 | 用途 | 是否必须公网放行 |
|---|---|---|
| `8080/tcp` | XIASS Web 与 API | 直接访问时需要；使用反向代理时可只允许本机 |
| `80/tcp`、`443/tcp` | Nginx/Caddy HTTPS | 配置域名时需要 |
| `1101-1120/tcp` | 软路由节点的公网认证 SOCKS | 仅使用代理节点时需要 |
| `7010/tcp` | FRP 控制连接 | 安装软路由 FRP 后需要 |
| `12083-12150/tcp` | Raw FRP 映射 | 安装软路由 FRP 后按后台设置放行 |

安装向导允许修改 Web、Raw FRP 和公网 SOCKS 端口范围。新安装发现端口被占用时会直接提示并停止，不会带着错误配置继续启动。

## 一条命令完整安装

在新服务器执行：

```bash
curl -fsSL https://raw.githubusercontent.com/xyf0104/xiass-api/main/install.sh | sudo bash
```

已经是 `root` 用户时也可以执行：

```bash
curl -fsSL https://raw.githubusercontent.com/xyf0104/xiass-api/main/install.sh | bash
```

安装向导会依次询问：

1. 使用正式镜像还是源码构建，amd64 默认选择正式镜像。
2. Web 服务端口，默认 `8080`。
3. Raw FRP 端口范围，默认 `12083-12150`。
4. 公网 SOCKS 端口范围，默认 `1101-1120`。
5. 管理员邮箱和密码，密码留空会安全随机生成。

脚本随后自动完成：

1. 检查系统、内存、磁盘、CPU 架构和基础依赖。
2. 安装并启动 Docker 与 Compose。
3. 检查所有配置端口是否可用。
4. 克隆源码到 `/opt/xiass-api`。
5. 为当前服务器生成独立 `.env` 和随机密钥。
6. 创建 PostgreSQL、Redis 与应用持久化目录。
7. 拉取 `ghcr.io/xyf0104/xiass-api:latest` 并启动完整容器栈。
8. 等待数据库迁移完成并验证健康接口。
9. 输出访问地址、管理员账号和常用维护命令。

安装完成后打开：

```text
http://服务器IP:8080
```

如果浏览器无法访问，依次检查：

```bash
cd /opt/xiass-api/deploy
docker compose -f docker-compose.local.yml ps
docker compose -f docker-compose.local.yml logs --tail 200 xiass-api
ss -lntp | grep ':8080'
```

然后确认云服务器安全组已放行实际使用的 Web 端口。

## 首次配置

首次登录后建议按以下顺序处理：

1. 在个人设置中修改管理员密码并启用 TOTP。
2. 在系统设置中修改站点名称、Logo、注册方式和通知配置。
3. 创建分组与渠道，确认用户倍率、成本倍率和模型价格。
4. 添加上游账号并进行模型测试。
5. 创建用户 API Key，通过兼容端点进行实际请求验证。
6. 配置域名、HTTPS、邮件、OAuth、支付或代理节点等可选能力。

## Codex 配置助手

登录 XIASS API 后进入“API 密钥”，打开一个绑定到 OpenAI 分组的有效密钥，选择 Codex 标签页，即可下载：

- `xiass-codex-helper-macos-universal.zip`：同时支持 Apple Silicon 与 Intel Mac。
- `xiass-codex-helper-windows-x64.exe`：支持 64 位 Windows。

助手无需安装。首次运行若被系统安全提示拦截，请在系统提示中确认仍要打开。启动后助手会：

1. 自动寻找 Codex App 和当前用户的 `~/.codex/config.toml`。
2. 打开已登录的 XIASS API 页面，只显示绑定到 OpenAI 分组的有效密钥。
3. 在本机创建原配置的完整备份与 SHA-256 校验记录。
4. 保留已有推理强度、MCP、插件、项目和桌面设置，仅更新 XIASS Provider 必需字段。
5. 原子写入后重新读取并校验；只有校验成功才重启 Codex。
6. 在助手页面选择任意历史备份恢复，恢复前还会再次备份当前配置。

助手只监听随机的 `127.0.0.1` 本机端口。API 密钥通过回环地址的 URL Fragment 交给本机助手，不会进入 XIASS API、Nginx 或浏览器请求日志。

## 域名与 HTTPS

先将域名的 A 记录指向服务器公网 IPv4。以下示例中的 `xiass.example.com` 必须替换为自己的域名。

### Nginx

Ubuntu/Debian 可安装 Nginx 与 Certbot：

```bash
sudo apt update
sudo apt install -y nginx certbot python3-certbot-nginx
```

创建 `/etc/nginx/sites-available/xiass`：

```nginx
server {
    listen 80;
    server_name xiass.example.com;

    client_max_body_size 100m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_buffering off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

启用配置并申请证书：

```bash
sudo ln -s /etc/nginx/sites-available/xiass /etc/nginx/sites-enabled/xiass
sudo nginx -t
sudo systemctl reload nginx
sudo certbot --nginx -d xiass.example.com
```

仓库也提供了可修改的 [Caddyfile](deploy/Caddyfile)。Caddy 会自动申请和续期证书。

## 在线更新

### 管理后台更新（正式镜像模式）

amd64 默认选择正式 GHCR 镜像时，一键安装会同时启动 Watchtower。管理员进入系统更新页面后：

1. 点击“检查更新”，系统读取本仓库最新的稳定 Release。
2. 点击“更新”，Watchtower 拉取 `latest` 镜像。
3. 只重建应用容器 `xiass-api`。
4. PostgreSQL、Redis、`.env` 和三个数据目录保持不变。
5. 新容器启动时只执行向前兼容的数据库迁移。

页面短暂断开属于容器重建过程，通常几十秒内恢复。更新后应在版本位置确认版本号已经变化。

arm64 或手动选择“源码构建”的实例不通过 Watchtower 拉取镜像，请使用下面的命令行更新；脚本会同步源码并重新构建应用容器。

### 带完整备份的命令行更新

需要最稳妥的更新时执行：

```bash
curl -fsSL https://raw.githubusercontent.com/xyf0104/xiass-api/main/deploy/xiass-update.sh | sudo bash
```

该脚本会先验证更新来源，再停止容器并创建一致性完整备份，随后同步部署文件、拉取镜像、重建应用并执行健康检查。旧 NoWind 安装会保留既有 Git `origin`，新增 `xiass-upstream` 作为正式更新来源；失败时会尽量恢复原 Git 状态、旧栈和本次新增的 remote。数据不会被删除。

自定义 Git fork 默认不会被脚本覆盖，且会在停机前退出。确认要切换到 XIASS API 正式发布源时，才显式执行：

```bash
curl -fsSL https://raw.githubusercontent.com/xyf0104/xiass-api/main/deploy/xiass-update.sh | sudo env XIASS_ALLOW_ORIGIN_MIGRATION=1 bash
```

## 备份与恢复

### 创建完整备份

```bash
curl -fsSL https://raw.githubusercontent.com/xyf0104/xiass-api/main/deploy/xiass-backup.sh | sudo bash
```

备份默认保存在：

```text
/root/xiass-backups/xiass-runtime-YYYYmmdd-HHMMSS.tar.gz
```

备份包含 `.env`、应用数据、PostgreSQL 和 Redis。为了保证数据库文件一致，脚本会短暂停止容器，归档完成后自动启动并检查健康状态。默认保留最近 10 份，可这样修改：

```bash
curl -fsSL https://raw.githubusercontent.com/xyf0104/xiass-api/main/deploy/xiass-backup.sh \
  | sudo env KEEP_BACKUPS=20 bash
```

备份包含管理员数据与密钥，传输时应使用 SCP/SFTP，不要上传到公开网盘或 GitHub。

### 恢复到新服务器

1. 在新服务器先运行上面的一键安装命令。
2. 将备份文件和同名 `.sha256` 文件上传到新服务器。
3. 执行恢复：

```bash
curl -fsSL https://raw.githubusercontent.com/xyf0104/xiass-api/main/deploy/xiass-restore.sh -o /tmp/xiass-restore.sh
sudo bash /tmp/xiass-restore.sh /root/xiass-backups/xiass-runtime-YYYYmmdd-HHMMSS.tar.gz
```

恢复脚本会先把新服务器当前数据移动到带时间戳的隔离目录，然后恢复备份并检查健康状态。恢复失败时会尝试自动移回原数据，不会直接永久删除。

## 软路由代理节点

进入管理后台的“代理管理 -> 代理节点”：

1. 设置 FRP 控制端口、Raw FRP 端口范围和公网 SOCKS 端口范围。
2. 点击安装 FRP，按页面输出完成服务器端配置。
3. 在 OpenWrt 安装 `XIASS API 软路由节点` 插件，并填写服务器地址、Agent ID 与密钥。
4. Agent 上报 PassWall/本地 SOCKS 节点后，在后台为每个节点分配独立 Raw 端口和公网端口。
5. 为公网 SOCKS 设置用户名与密码后再对外提供服务。

端口范围变化会影响 Docker 端口映射。按后台提示重建应用容器后再测试，且必须同步修改云安全组。

## 常用维护命令

```bash
cd /opt/xiass-api/deploy

# 查看容器
docker compose -f docker-compose.local.yml ps

# 查看应用日志
docker compose -f docker-compose.local.yml logs -f --tail 200 xiass-api

# 重启应用，不重启数据库
docker compose -f docker-compose.local.yml restart xiass-api

# 停止整套服务，不删除数据
docker compose -f docker-compose.local.yml down

# 再次启动
docker compose -f docker-compose.local.yml up -d

# 查看数据目录占用
du -sh data postgres_data redis_data
```

## 常见问题

### 提示端口被占用

```bash
ss -lntp | grep -E ':(8080|1101|12083|7010)\b'
```

停止冲突服务，或重新运行安装脚本并选择其他端口。端口范围不能互相重叠。

### 镜像拉取失败

```bash
docker pull ghcr.io/xyf0104/xiass-api:latest
```

确认服务器可以访问 `ghcr.io` 与 GitHub。需要代理时可在 `.env` 配置 `UPDATE_PROXY_URL`，然后重建应用容器。

### 更新后仍显示旧版本

```bash
cd /opt/xiass-api/deploy
docker compose -f docker-compose.local.yml pull xiass-api
docker compose -f docker-compose.local.yml up -d --no-deps --force-recreate xiass-api
```

不要只执行 `restart`，因为重启旧容器不会切换到刚拉取的新镜像。

### 忘记管理员密码

先保留完整备份，再通过服务器端管理方式重置。不要删除 PostgreSQL 目录或重新生成 `.env`，否则会破坏现有凭据关系。

### 磁盘不断增长

```bash
docker system df
du -sh /opt/xiass-api/deploy/*
```

可清理未使用镜像，但不要清理正在使用的卷：

```bash
docker image prune -f
```

## 源码开发与验证

```bash
git clone https://github.com/xyf0104/xiass-api.git
cd xiass-api

# 前端
CI=true npx -y pnpm@9 --dir frontend install --frozen-lockfile
CI=true npx -y pnpm@9 --dir frontend run build

# 后端
cd backend
go test ./internal/repository ./internal/service ./internal/handler ./internal/server/routes ./internal/setup
```

## 发布约定

每次发布新版本必须同时满足：

- 更新 `backend/cmd/server/VERSION` 与 README 当前版本。
- 不修改或删除已经发布的数据库迁移，只能新增向前迁移。
- 保留 Docker Compose 的应用、PostgreSQL 和 Redis 持久化挂载。
- 安装和更新脚本不得覆盖已有 `.env`，不得执行 `down -v`。
- 前端测试、类型检查、生产构建、后端测试、静态检查和安全扫描通过。
- Release 与 `ghcr.io/xyf0104/xiass-api:<version>`、`latest` 同步发布。

仓库 CI 会检查上述版本、迁移和持久化契约，防止后续合并破坏数据安全。

## 开源许可

XIASS API 以 [GNU LGPL v3](LICENSE) 公开源码。使用者应自行确认上游服务条款、当地法律法规和数据合规要求，并对自己的部署、账号与数据负责。
