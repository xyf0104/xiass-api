# XIASS 部署目录

本目录保存 XIASS 的 Docker Compose、二进制安装、在线更新、备份恢复和软路由代理节点脚本。新服务器优先使用仓库根目录的一键安装入口。

## 推荐：Docker 完整安装

```bash
curl -fsSL https://raw.githubusercontent.com/xyf0104/xiass-api/main/install.sh | sudo bash
```

该命令会安装 Docker/Compose 和基础依赖，检查端口，克隆仓库，生成独立密钥，并启动：

- `xiass-api`：XIASS 应用
- PostgreSQL
- Redis
- `xiass-api-watchtower`：后台在线更新

新安装默认目录为 `/opt/xiass-api`，默认使用 `docker-compose.local.yml`，所有运行数据都保存在 `deploy` 下的本地目录，方便备份和迁移。

安装/更新脚本会先探测 `/opt/xiass-api`、`/opt/nowind-api`、`/opt/sub2api` 和运行中的 `xiass-api`、`nowind-api`、`sub2api` 容器。`XIASS_*` 是新配置主变量，已有 `NOWIND_*` 继续作为 fallback。升级会按 PostgreSQL 的实际 mount 选择本地目录或命名卷 Compose，且从不使用 `down -v`。

不要把历史 `docker-compose.nowind.yml` 与 canonical Compose 文件手工混合。它不是跨版本迁移入口；旧 `nowind-api`/`sub2api` 栈必须通过 `xiass-update.sh` 或 `xiass-install.sh` 迁移，脚本会冻结运行中容器记录的 Compose 文件并保留原布局用于失败回滚。

## 文件说明

| 文件 | 用途 |
|---|---|
| `xiass-install.sh` | 完整 Docker 一键安装 |
| `xiass-update.sh` | 先备份再更新镜像/源码 |
| `xiass-backup.sh` | 一致性完整备份 |
| `xiass-restore.sh` | 跨服务器恢复与失败回退 |
| `nowind-*.sh` | v1.0.67 及更早版本使用的兼容入口，会转交到对应 XIASS 脚本 |
| `docker-compose.local.yml` | 推荐，本地目录持久化 |
| `docker-compose.yml` | Docker 命名卷持久化 |
| `docker-compose.standalone.yml` | 使用外部 PostgreSQL/Redis |
| `docker-compose.build.yml` | 本机源码构建覆盖配置 |
| `.env.example` | 环境变量模板，不含真实密钥 |
| `config.example.yaml` | 高级配置模板 |
| `Caddyfile` | HTTPS 反向代理示例 |
| `install.sh` | 二进制/systemd 安装器，需自备 PostgreSQL/Redis |

## 持久化目录

推荐部署的四项核心数据：

```text
/opt/xiass-api/deploy/.env
/opt/xiass-api/deploy/data
/opt/xiass-api/deploy/postgres_data
/opt/xiass-api/deploy/redis_data
```

重新拉取代码、拉取镜像或重建 `xiass-api` 容器不会删除这些目录。旧 `/opt/nowind-api`、`/opt/sub2api` 安装会原地沿用其目录。禁止使用：

```bash
docker compose down -v
```

## 端口

| 默认值 | 用途 |
|---|---|
| `8080/tcp` | Web/API |
| `1101-1120/tcp` | 公网认证 SOCKS |
| `7010/tcp` | FRP 控制端口 |
| `12083-12150/tcp` | Raw FRP 映射 |

安装脚本会检查端口占用并处理 UFW/firewalld。云安全组仍需手动放行。

## 更新

后台在线更新通过 Watchtower 只重建应用容器。需要自动创建更新前备份时执行：

```bash
curl -fsSL https://raw.githubusercontent.com/xyf0104/xiass-api/main/deploy/xiass-update.sh | sudo bash
```

## 备份

```bash
curl -fsSL https://raw.githubusercontent.com/xyf0104/xiass-api/main/deploy/xiass-backup.sh | sudo bash
```

默认输出到 `/root/xiass-backups`，包含 `.env`、应用数据、PostgreSQL 和 Redis，并生成 SHA-256 校验文件。本地目录备份保留目录结构；命名卷备份把三个卷安全归档为独立 tar 文件。

## 恢复

新服务器先完成一键安装，再上传备份并执行：

```bash
curl -fsSL https://raw.githubusercontent.com/xyf0104/xiass-api/main/deploy/xiass-restore.sh -o /tmp/xiass-restore.sh
sudo bash /tmp/xiass-restore.sh /root/xiass-backups/xiass-runtime-YYYYmmdd-HHMMSS.tar.gz
```

恢复前的目标实例数据会移动到带时间戳的隔离目录；命名卷会先完整快照到该目录。为避免误写，恢复拒绝在本地目录与命名卷布局之间交叉执行。

## 常用命令

```bash
cd /opt/xiass-api/deploy

docker compose -f docker-compose.local.yml ps
docker compose -f docker-compose.local.yml logs -f --tail 200 xiass-api
docker compose -f docker-compose.local.yml restart xiass-api
docker compose -f docker-compose.local.yml down
docker compose -f docker-compose.local.yml up -d
```

## 二进制安装

已有独立 PostgreSQL 与 Redis 时，可使用 systemd 二进制安装器：

```bash
curl -fsSL https://raw.githubusercontent.com/xyf0104/xiass-api/main/deploy/install.sh | sudo bash
```

该方式不会自动部署数据库与 Redis，首次启动后需要按设置向导填写连接信息。普通用户应使用 Docker 完整安装。
