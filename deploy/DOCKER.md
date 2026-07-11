# NoWind API Docker 镜像

NoWind 是 AI API 网关、账号池、用户计费和用量管理平台。

## 一键完整安装

```bash
curl -fsSL https://raw.githubusercontent.com/xyf0104/nowind-api/main/install.sh | sudo bash
```

该安装方式会自动准备 Docker Compose、PostgreSQL、Redis、Watchtower、随机密钥和本地持久化目录，不需要外部数据库或 S3。

## 镜像

```bash
docker pull ghcr.io/xyf0104/nowind-api:latest
```

正式镜像当前发布 `linux/amd64`。版本标签包括：

- `latest`：最新稳定版
- `x.y.z`：指定稳定版本
- `x.y.z-amd64`：指定版本和架构

## 推荐 Compose

```bash
git clone https://github.com/xyf0104/nowind-api.git
cd nowind-api/deploy
cp .env.example .env
mkdir -p data postgres_data redis_data
```

编辑 `.env`，至少设置随机值：

```bash
openssl rand -hex 32
```

必须配置：

```dotenv
POSTGRES_PASSWORD=替换为随机值
REDIS_PASSWORD=替换为随机值
JWT_SECRET=替换为随机值
TOTP_ENCRYPTION_KEY=替换为随机值
NOWIND_WATCHTOWER_TOKEN=替换为随机值
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=替换为强密码
```

启动：

```bash
docker compose -f docker-compose.local.yml up -d
docker compose -f docker-compose.local.yml ps
```

## 数据位置

`docker-compose.local.yml` 使用：

```text
./data
./postgres_data
./redis_data
./.env
```

更新应用容器不会删除这些目录。不要执行 `docker compose down -v`。

## 更新与备份

```bash
# 先备份再更新
curl -fsSL https://raw.githubusercontent.com/xyf0104/nowind-api/main/deploy/nowind-update.sh | sudo bash

# 只创建完整备份
curl -fsSL https://raw.githubusercontent.com/xyf0104/nowind-api/main/deploy/nowind-backup.sh | sudo bash
```

详细说明见项目根目录 [README](../README.md)。
